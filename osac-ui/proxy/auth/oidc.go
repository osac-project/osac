package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// OIDCConfig is the subset of an OIDC discovery document we need.
type OIDCConfig struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint,omitempty"`
	EndSessionEndpoint    string `json:"end_session_endpoint,omitempty"`
}

// TokenResponse is the OAuth2 token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

var (
	oidcCacheMu    sync.RWMutex
	oidcCacheByURL = map[string]*oidcCacheEntry{}
)

type oidcCacheEntry struct {
	cfg     OIDCConfig
	expires time.Time
}

// sanitizedNetErr wraps a network cause with a hostname-free Error() string
// while preserving the original cause through Unwrap() for errors.Is/As.
type sanitizedNetErr struct {
	op  string
	err error
}

func (e *sanitizedNetErr) Error() string { return e.op + ": " + sanitizeErrMsg(e.err) }
func (e *sanitizedNetErr) Unwrap() error { return e.err }

// sanitizeErrMsg returns a description of err with hostnames and server addresses
// removed. It recurses through *net.OpError and extracts only the reason from
// *net.DNSError, which never includes the queried host or resolver address.
func sanitizeErrMsg(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS lookup: " + dnsErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op + ": " + sanitizeErrMsg(opErr.Err)
	}
	return "network request failed"
}

// sanitizeNetErr strips the request URL from *url.Error so that internal
// endpoint hostnames never appear in returned errors or log messages.
func sanitizeNetErr(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &sanitizedNetErr{op: urlErr.Op, err: urlErr.Err}
	}
	return err
}

// FetchOIDCConfig returns the OIDC discovery document, using a per-issuer 5-minute cache.
// If httpClient is nil, http.DefaultClient is used.
func FetchOIDCConfig(issuerURL string, httpClient *http.Client) (*OIDCConfig, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	oidcCacheMu.RLock()
	if e, ok := oidcCacheByURL[issuerURL]; ok && time.Now().Before(e.expires) {
		cfg := e.cfg
		oidcCacheMu.RUnlock()
		return &cfg, nil
	}
	oidcCacheMu.RUnlock()

	discoveryURL := strings.TrimSuffix(issuerURL, "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build OIDC discovery request: %w", sanitizeNetErr(err))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request: %w", sanitizeNetErr(err))
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery returned HTTP %d", resp.StatusCode)
	}
	var cfg OIDCConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode OIDC config: %w", err)
	}

	oidcCacheMu.Lock()
	oidcCacheByURL[issuerURL] = &oidcCacheEntry{cfg: cfg, expires: time.Now().Add(5 * time.Minute)}
	oidcCacheMu.Unlock()

	return &cfg, nil
}

// BuildAuthorizeURL builds the IdP authorization URL for PKCE Authorization Code flow.
func BuildAuthorizeURL(cfg *OIDCConfig, clientID, redirectURI, state, challenge string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", "openid organization")
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	return cfg.AuthorizationEndpoint + "?" + params.Encode()
}

// ExchangeCode exchanges an authorization code for tokens at the IdP token endpoint.
// If httpClient is nil, http.DefaultClient is used.
func ExchangeCode(cfg *OIDCConfig, clientID, code, verifier, redirectURI string, httpClient *http.Client) (*TokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("client_id", clientID)
	params.Set("code", code)
	params.Set("code_verifier", verifier)
	params.Set("redirect_uri", redirectURI)

	return postTokenEndpoint(cfg.TokenEndpoint, params, httpClient)
}

// EndSession calls the IdP end_session endpoint to invalidate the session server-side.
// It sends client_id and refresh_token (for Keycloak back-channel logout) as well as
// id_token_hint when available.
// If httpClient is nil, http.DefaultClient is used.
func EndSession(cfg *OIDCConfig, clientID, refreshToken, idToken string, httpClient *http.Client) error {
	if cfg.EndSessionEndpoint == "" {
		return fmt.Errorf("OIDC end_session_endpoint not available")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	params := url.Values{}
	params.Set("client_id", clientID)
	if refreshToken != "" {
		params.Set("refresh_token", refreshToken)
	}
	if idToken != "" {
		params.Set("id_token_hint", idToken)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EndSessionEndpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("build end_session request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("end_session request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("failed to close end_session response body")
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("end_session endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ClientCredentialsToken fetches a service account access token using the OAuth2
// client_credentials grant. Use for backend-to-backend calls where no user is involved.
// If httpClient is nil, http.DefaultClient is used.
func ClientCredentialsToken(issuerURL, clientID, clientSecret string, httpClient *http.Client) (*TokenResponse, error) {
	cfg, err := FetchOIDCConfig(issuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	return postTokenEndpoint(cfg.TokenEndpoint, params, httpClient)
}

// RefreshTokens exchanges a refresh token for new tokens at the IdP token endpoint.
// If httpClient is nil, http.DefaultClient is used.
func RefreshTokens(cfg *OIDCConfig, clientID, refreshToken string, httpClient *http.Client) (*TokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("client_id", clientID)
	params.Set("refresh_token", refreshToken)

	return postTokenEndpoint(cfg.TokenEndpoint, params, httpClient)
}

func postTokenEndpoint(endpoint string, params url.Values, httpClient *http.Client) (*TokenResponse, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", sanitizeNetErr(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request: %w", sanitizeNetErr(err))
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("failed to close response body")
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned HTTP %d (body redacted for security)", resp.StatusCode)
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tr, nil
}
