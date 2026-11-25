package authgate

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// KeycloakConfig is the realm the admin Route trusts. Audiences lists the
// clients an OSAC login is minted for; a token whose audience or authorized
// party is none of them is not an OSAC login and is rejected.
type KeycloakConfig struct {
	Issuer    string
	Audiences []string
	AdminRole string
	Client    *http.Client
	Now       func() time.Time
}

// KeycloakAuthenticator verifies a Keycloak access token against the realm's
// published JWKS and requires the configured realm role. It does not need the
// OAuth proxy's session or cookie.
type KeycloakAuthenticator struct {
	issuer    string
	audiences []string
	adminRole string
	client    *http.Client
	now       func() time.Time
	certsURL  string

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

const keyCacheTTL = 5 * time.Minute

// NewKeycloakAuthenticator validates its configuration and returns the
// authenticator. An empty issuer would accept tokens from any realm, so it is
// an error rather than a bypass.
func NewKeycloakAuthenticator(cfg KeycloakConfig) (*KeycloakAuthenticator, error) {
	issuer := strings.TrimSuffix(strings.TrimSpace(cfg.Issuer), "/")
	if issuer == "" {
		return nil, errors.New("OIDC_ISSUER_URL is required")
	}
	if cfg.AdminRole == "" {
		return nil, errors.New("ADMIN_ROLE is required")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &KeycloakAuthenticator{
		issuer:    issuer,
		audiences: cfg.Audiences,
		adminRole: cfg.AdminRole,
		client:    client,
		now:       now,
		certsURL:  issuer + "/protocol/openid-connect/certs",
	}, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type keycloakClaims struct {
	Iss         string          `json:"iss"`
	Aud         json.RawMessage `json:"aud"`
	Azp         string          `json:"azp"`
	Exp         int64           `json:"exp"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Authenticate verifies signature, issuer, audience and expiry, then requires
// the realm role. A valid token without the role is ErrForbidden; everything
// else is ErrUnauthenticated.
func (a *KeycloakAuthenticator) Authenticate(ctx context.Context, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrUnauthenticated
	}
	header, err := decodeSegment[jwtHeader](parts[0])
	if err != nil || header.Alg != "RS256" || header.Kid == "" {
		return ErrUnauthenticated
	}
	key, err := a.key(ctx, header.Kid)
	if err != nil || key == nil {
		return ErrUnauthenticated
	}
	signing := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(signing))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return ErrUnauthenticated
	}
	claims, err := decodeSegment[keycloakClaims](parts[1])
	if err != nil {
		return ErrUnauthenticated
	}
	if strings.TrimSuffix(claims.Iss, "/") != a.issuer {
		return ErrUnauthenticated
	}
	if claims.Exp == 0 || a.now().Unix() >= claims.Exp {
		return ErrUnauthenticated
	}
	if !a.isLoginToken(claims) {
		return ErrUnauthenticated
	}
	if !contains(claims.RealmAccess.Roles, a.adminRole) {
		return ErrForbidden
	}
	return nil
}

func decodeSegment[T any](segment string) (T, error) {
	var value T
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(raw, &value)
	return value, err
}

// isLoginToken reports whether the token was minted for one of the OSAC login
// clients. Keycloak always sets azp to the requesting client; aud only carries
// an audience when a mapper added it, so both are checked.
func (a *KeycloakAuthenticator) isLoginToken(claims keycloakClaims) bool {
	if len(a.audiences) == 0 {
		return true
	}
	for _, wanted := range a.audiences {
		if wanted == claims.Azp || audienceContains(claims.Aud, wanted) {
			return true
		}
	}
	return false
}

func audienceContains(raw json.RawMessage, audience string) bool {
	if audience == "" || len(raw) == 0 {
		return audience == ""
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == audience
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return contains(many, audience)
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// key returns the signing key for kid, refreshing the realm's JWKS when the
// cache is cold, stale, or does not know the kid (Keycloak rotates keys).
func (a *KeycloakAuthenticator) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if key, ok := a.keys[kid]; ok && a.now().Sub(a.fetchedAt) < keyCacheTTL {
		return key, nil
	}
	if err := a.refreshLocked(ctx); err != nil {
		// A cached key is still the best answer if the refresh fails.
		return a.keys[kid], err
	}
	return a.keys[kid], nil
}

type jwksDocument struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (a *KeycloakAuthenticator) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.certsURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("fetch %s: status %d", a.certsURL, resp.StatusCode)
	}
	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, entry := range doc.Keys {
		if entry.Kty != "RSA" || entry.Kid == "" {
			continue
		}
		key, err := rsaKey(entry.N, entry.E)
		if err != nil {
			continue
		}
		keys[entry.Kid] = key
	}
	if len(keys) == 0 {
		return errors.New("the realm published no usable RSA keys")
	}
	a.keys = keys
	a.fetchedAt = a.now()
	return nil
}

func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	if len(e) == 0 || len(e) > 4 {
		return nil, errors.New("invalid RSA exponent")
	}
	exponentValue := 0
	for _, b := range e {
		exponentValue = exponentValue<<8 | int(b)
	}
	if exponentValue == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponentValue}, nil
}
