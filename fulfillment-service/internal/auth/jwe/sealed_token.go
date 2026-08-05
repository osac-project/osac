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
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// SealerBuilder contains the data and logic needed to create a token sealer. Don't create instances of this type
// directly, use the NewSealer function instead.
type SealerBuilder struct {
	logger             *slog.Logger
	signingCertFile    string
	signingKeyFile     string
	encryptionCertFile string
	issuer             string
}

// Sealer signs JWT claims (JWS, RS256) and encrypts the result (JWE,
// RSA-OAEP-256, A256GCM, DEFLATE) to produce nested JWTs per RFC 7519
// Section 11.2. Keys are hot-reloaded from PEM files on mtime change.
type Sealer struct {
	signingKey    *certKeyReloader
	encryptionKey *certKeyReloader
	issuer        string
}

// NewSealer creates a builder that can then be used to configure and create a new token sealer.
func NewSealer() *SealerBuilder {
	return &SealerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *SealerBuilder) SetLogger(value *slog.Logger) *SealerBuilder {
	b.logger = value
	return b
}

// SetSigningCertFile sets the path to the PEM certificate used for JWS signing. This is mandatory.
func (b *SealerBuilder) SetSigningCertFile(value string) *SealerBuilder {
	b.signingCertFile = value
	return b
}

// SetSigningKeyFile sets the path to the PEM private key used for JWS signing. This is mandatory.
func (b *SealerBuilder) SetSigningKeyFile(value string) *SealerBuilder {
	b.signingKeyFile = value
	return b
}

// SetEncryptionCertFile sets the path to the recipient's PEM certificate used for JWE encryption. This is mandatory.
func (b *SealerBuilder) SetEncryptionCertFile(value string) *SealerBuilder {
	b.encryptionCertFile = value
	return b
}

// SetIssuer sets the URL used as the JWT iss claim. This is mandatory.
func (b *SealerBuilder) SetIssuer(value string) *SealerBuilder {
	b.issuer = value
	return b
}

// Build uses the data stored in the builder to create and configure a new token sealer.
func (b *SealerBuilder) Build() (*Sealer, error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.signingCertFile == "" {
		return nil, errors.New("signing certificate file is mandatory")
	}
	if b.signingKeyFile == "" {
		return nil, errors.New("signing key file is mandatory")
	}
	if b.encryptionCertFile == "" {
		return nil, errors.New("encryption certificate file is mandatory")
	}
	if b.issuer == "" {
		return nil, errors.New("issuer is mandatory")
	}

	// Load the signing key pair:
	signingKey := &certKeyReloader{
		logger:   b.logger.With(slog.String("component", "token_sealer_signing")),
		certFile: b.signingCertFile,
		keyFile:  b.signingKeyFile,
	}
	if err := signingKey.ensureLoaded(context.Background()); err != nil {
		return nil, fmt.Errorf("initial load of signing keypair: %w", err)
	}

	// Load the encryption certificate:
	encryptionKey := &certKeyReloader{
		logger:   b.logger.With(slog.String("component", "token_sealer_encryption")),
		certFile: b.encryptionCertFile,
	}
	if err := encryptionKey.ensureLoaded(context.Background()); err != nil {
		return nil, fmt.Errorf("initial load of encryption certificate: %w", err)
	}

	// Create and populate the object:
	return &Sealer{
		signingKey:    signingKey,
		encryptionKey: encryptionKey,
		issuer:        b.issuer,
	}, nil
}

// Seal creates a nested JWT. Builds JWT claims (iss, aud, sub, jti, iat,
// exp + custom), signs as JWS (RS256), then encrypts as JWE (RSA-OAEP-256,
// A256GCM, DEFLATE). Returns the compact JWE serialization and expiry time.
// audience: the intended recipient identifier, set as the JWT aud claim.
func (s *Sealer) Seal(ctx context.Context, subject string, audience string, claims map[string]any, ttl time.Duration) (string, time.Time, error) {
	if err := s.signingKey.ensureLoaded(ctx); err != nil {
		return "", time.Time{}, fmt.Errorf("reload signing key: %w", err)
	}
	if err := s.encryptionKey.ensureLoaded(ctx); err != nil {
		return "", time.Time{}, fmt.Errorf("reload encryption key: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(ttl)
	jti := uuid.New().String()

	builder := jwt.NewBuilder().
		Issuer(s.issuer).
		Audience([]string{audience}).
		Subject(subject).
		JwtID(jti).
		IssuedAt(now).
		Expiration(expiresAt)

	for k, v := range claims {
		switch k {
		case "iss", "aud", "sub", "jti", "iat", "exp", "nbf":
			continue
		}
		builder = builder.Claim(k, v)
	}

	tok, err := builder.Build()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build token: %w", err)
	}

	// Sign (JWS) then encrypt (JWE) -> nested JWT.
	s.signingKey.mu.Lock()
	privKey := s.signingKey.privateKey
	sigJWK := s.signingKey.jwkKey
	s.signingKey.mu.Unlock()

	s.encryptionKey.mu.Lock()
	encPubKey := s.encryptionKey.publicKey
	s.encryptionKey.mu.Unlock()

	// Build the encryption JWK with kid for the JWE header.
	encJWK, err := jwk.Import(encPubKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("import encryption public key: %w", err)
	}
	if err := jwk.AssignKeyID(encJWK); err != nil {
		return "", time.Time{}, fmt.Errorf("assign encryption kid: %w", err)
	}

	// Use the signing JWK (with kid) for the JWS header.
	sigPrivJWK, err := jwk.Import(privKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("import signing private key: %w", err)
	}
	kid, ok := sigJWK.KeyID()
	if !ok {
		return "", time.Time{}, fmt.Errorf("signing JWK missing key ID")
	}
	if err := sigPrivJWK.Set(jwk.KeyIDKey, kid); err != nil {
		return "", time.Time{}, fmt.Errorf("set kid on signing key: %w", err)
	}

	serialized, err := jwt.NewSerializer().
		Sign(jwt.WithKey(jwa.RS256(), sigPrivJWK)).
		Encrypt(
			jwt.WithKey(jwa.RSA_OAEP_256(), encJWK),
			jwt.WithEncryptOption(jwe.WithContentEncryption(jwa.A256GCM())),
			jwt.WithEncryptOption(jwe.WithCompress(jwa.Deflate())),
		).
		Serialize(tok)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign and encrypt token: %w", err)
	}

	return string(serialized), expiresAt, nil
}

// JWKSet returns the JWS signing public key as a JWK Set for JWKS discovery.
func (s *Sealer) JWKSet(ctx context.Context) (jwk.Set, error) {
	if err := s.signingKey.ensureLoaded(ctx); err != nil {
		return nil, fmt.Errorf("reload signing key: %w", err)
	}

	s.signingKey.mu.Lock()
	k := s.signingKey.jwkKey
	s.signingKey.mu.Unlock()

	set := jwk.NewSet()
	if err := set.AddKey(k); err != nil {
		return nil, fmt.Errorf("add key to set: %w", err)
	}
	return set, nil
}
