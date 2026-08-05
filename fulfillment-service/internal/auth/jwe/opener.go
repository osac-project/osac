/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jwe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"golang.org/x/sync/singleflight"
)

// Claims contains the parsed standard and custom claims from a verified token.
// Issuer and audience are validated during Open and not returned.
type Claims struct {
	Subject   string
	JTI       string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Custom    map[string]any
}

// OpenerBuilder contains the data and logic needed to create a token opener. Don't create instances of this type
// directly, use the NewOpener function instead.
type OpenerBuilder struct {
	ctx               context.Context
	logger            *slog.Logger
	decryptionKeyFile string
	jwksURL           string
	issuer            string
	audience          string
	cache             *jwk.Cache
}

// Opener decrypts nested JWTs (JWE, RSA-OAEP-256) and verifies the inner
// JWS (RS256) against a JWKS fetched on demand when the token's kid is
// unknown. Uses jwk.Cache for periodic background refresh and forces an
// immediate re-fetch when a token's kid is not in the cached set.
// Enforces single-use via JTI tracking.
type Opener struct {
	logger        *slog.Logger
	decryptionKey *privateKeyReloader
	jwksURL       string
	issuer        string
	audience      string
	cache         *jwk.Cache         // periodically refreshes JWKS in background
	refreshGroup  singleflight.Group // coalesces concurrent forced JWKS refreshes
	mu            sync.Mutex
	seen          map[string]time.Time // jti -> cleanup expiry
}

// NewOpener creates a builder that can then be used to configure and create a new token opener.
func NewOpener() *OpenerBuilder {
	return &OpenerBuilder{}
}

// SetContext sets the context used for loading the decryption key and the JTI cleanup goroutine. This is mandatory.
func (b *OpenerBuilder) SetContext(value context.Context) *OpenerBuilder {
	b.ctx = value
	return b
}

// SetLogger sets the logger. This is mandatory.
func (b *OpenerBuilder) SetLogger(value *slog.Logger) *OpenerBuilder {
	b.logger = value
	return b
}

// SetDecryptionKeyFile sets the path to the PEM private key for JWE decryption. This is mandatory.
func (b *OpenerBuilder) SetDecryptionKeyFile(value string) *OpenerBuilder {
	b.decryptionKeyFile = value
	return b
}

// SetJWKSURL sets the HTTPS URL of the JWKS endpoint for JWS verification. This is mandatory.
func (b *OpenerBuilder) SetJWKSURL(value string) *OpenerBuilder {
	b.jwksURL = value
	return b
}

// SetIssuer sets the expected JWT iss claim value. This is mandatory.
func (b *OpenerBuilder) SetIssuer(value string) *OpenerBuilder {
	b.issuer = value
	return b
}

// SetAudience sets the expected JWT aud claim value. This is mandatory.
func (b *OpenerBuilder) SetAudience(value string) *OpenerBuilder {
	b.audience = value
	return b
}

// SetCache sets the JWKS cache used for key discovery and periodic refresh. The caller must
// create the cache, register the JWKS URL, and configure TLS before passing it in. This is mandatory.
func (b *OpenerBuilder) SetCache(value *jwk.Cache) *OpenerBuilder {
	b.cache = value
	return b
}

// Build uses the data stored in the builder to create and configure a new token opener.
func (b *OpenerBuilder) Build() (*Opener, error) {
	// Check parameters:
	if b.ctx == nil {
		return nil, errors.New("context is mandatory")
	}
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.decryptionKeyFile == "" {
		return nil, errors.New("decryption key file is mandatory")
	}
	if b.jwksURL == "" {
		return nil, errors.New("JWKS URL is mandatory")
	}
	// OIDC Discovery (RFC 8414) requires jwks_uri to use HTTPS.
	parsed, err := url.Parse(b.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("parse JWKS URL %q: %w", b.jwksURL, err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("JWKS URL %q must use HTTPS scheme", b.jwksURL)
	}
	if b.issuer == "" {
		return nil, errors.New("issuer is mandatory")
	}
	if b.audience == "" {
		return nil, errors.New("audience is mandatory")
	}
	if b.cache == nil {
		return nil, errors.New("cache is mandatory")
	}

	// Load the decryption key:
	decryptionKey := &privateKeyReloader{
		logger:  b.logger.With(slog.String("component", "token_opener_decryption")),
		keyFile: b.decryptionKeyFile,
	}
	if err := decryptionKey.ensureLoaded(b.ctx); err != nil {
		return nil, fmt.Errorf("initial load of decryption key: %w", err)
	}

	// Create and populate the object:
	o := &Opener{
		logger:        b.logger,
		decryptionKey: decryptionKey,
		jwksURL:       b.jwksURL,
		issuer:        b.issuer,
		audience:      b.audience,
		cache:         b.cache,
		seen:          make(map[string]time.Time),
	}
	go o.cleanupLoop(b.ctx)
	return o, nil
}

// Open decrypts the JWE outer layer, verifies the JWS inner layer against
// the JWKS (refreshing on unknown kid), validates standard claims (iss, aud,
// exp with 5s skew), and enforces JTI single-use. Returns parsed claims on success.
func (o *Opener) Open(ctx context.Context, tokenString string) (*Claims, error) {
	if err := o.decryptionKey.ensureLoaded(ctx); err != nil {
		return nil, fmt.Errorf("reload decryption key: %w", err)
	}

	o.decryptionKey.mu.Lock()
	decPrivKey := o.decryptionKey.privateKey
	o.decryptionKey.mu.Unlock()

	if decPrivKey == nil {
		return nil, errors.New("decryption private key not loaded")
	}

	// Decrypt JWE outer layer.
	jwsPayload, err := jwe.Decrypt([]byte(tokenString),
		jwe.WithKey(jwa.RSA_OAEP_256(), decPrivKey),
	)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	// Resolve the signing keyset (refresh on unknown kid).
	set, err := o.resolveKeySet(ctx, jwsPayload)
	if err != nil {
		return nil, err
	}

	// Verify JWS and validate standard claims.
	tok, err := jwt.Parse(jwsPayload,
		jwt.WithKeySet(set),
		jwt.WithIssuer(o.issuer),
		jwt.WithAcceptableSkew(5*time.Second),
		jwt.WithAudience(o.audience),
	)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}

	// Extract claims from verified token.
	claims, err := extractClaims(tok)
	if err != nil {
		return nil, err
	}

	// JTI single-use enforcement.
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, exists := o.seen[claims.JTI]; exists {
		return nil, fmt.Errorf("token already used (jti: %s)", claims.JTI)
	}
	o.seen[claims.JTI] = claims.ExpiresAt.Add(5 * time.Second)

	return claims, nil
}

// resolveKeySet returns the JWKS to verify against, refreshing from
// the endpoint if the token's kid is not in the cached set.
func (o *Opener) resolveKeySet(ctx context.Context, jwsPayload []byte) (jwk.Set, error) {
	jwsMsg, err := jws.Parse(jwsPayload)
	if err != nil {
		return nil, fmt.Errorf("parse JWS: %w", err)
	}

	set, err := o.cache.Lookup(ctx, o.jwksURL)
	if err != nil {
		return o.refreshJWKS(ctx)
	}

	sigs := jwsMsg.Signatures()
	if len(sigs) == 0 {
		return set, nil
	}
	kid, ok := sigs[0].ProtectedHeaders().KeyID()
	if !ok {
		return set, nil
	}
	if _, found := set.LookupKeyID(kid); found {
		return set, nil
	}
	return o.refreshJWKS(ctx)
}

// refreshJWKS forces an immediate JWKS re-fetch via the cache, coalescing
// concurrent callers via singleflight.
func (o *Opener) refreshJWKS(ctx context.Context) (jwk.Set, error) {
	// Detach from the caller's context so one cancelled request does
	// not abort the shared fetch. Also strips deadline intentionally —
	// the HTTP transport timeout bounds the fetch.
	ctx = context.WithoutCancel(ctx)

	v, err, _ := o.refreshGroup.Do("jwks", func() (any, error) {
		set, err := o.cache.Refresh(ctx, o.jwksURL)
		if err != nil {
			o.logger.WarnContext(ctx, "JWKS refresh failed, using cached keyset",
				slog.String("jwks_url", o.jwksURL),
				slog.Any("error", err),
			)
			cached, lookupErr := o.cache.Lookup(ctx, o.jwksURL)
			if lookupErr != nil {
				return nil, fmt.Errorf("JWKS refresh failed and no cached keyset available: %w", err)
			}
			return cached, nil
		}
		return set, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(jwk.Set), nil
}

// extractClaims reads standard and custom claims from a verified JWT token.
func extractClaims(tok jwt.Token) (*Claims, error) {
	jti, ok := tok.JwtID()
	if !ok || jti == "" {
		return nil, errors.New("token missing jti")
	}
	sub, ok := tok.Subject()
	if !ok || sub == "" {
		return nil, errors.New("token missing sub")
	}
	exp, ok := tok.Expiration()
	if !ok {
		return nil, errors.New("token missing exp")
	}
	iat, _ := tok.IssuedAt()

	custom := make(map[string]any)
	for _, key := range tok.Keys() {
		switch key {
		case "iss", "sub", "aud", "jti", "iat", "exp", "nbf":
		default:
			var val any
			if err := tok.Get(key, &val); err == nil {
				custom[key] = val
			}
		}
	}
	return &Claims{
		Subject:   sub,
		JTI:       jti,
		IssuedAt:  iat,
		ExpiresAt: exp,
		Custom:    custom,
	}, nil
}

// cleanupLoop periodically removes expired JTI entries. It stops when ctx
// is cancelled (graceful shutdown).
func (o *Opener) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.mu.Lock()
			now := time.Now()
			for jti, expiry := range o.seen {
				if now.After(expiry) {
					delete(o.seen, jti)
				}
			}
			o.mu.Unlock()
		}
	}
}
