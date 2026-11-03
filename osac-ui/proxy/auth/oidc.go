package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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

const maxOIDCMetadataBytes = 1 << 20

// netip.IsGlobalUnicast includes these special-use ranges.
var oidcSpecialUsePrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // Shared address space.
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments.
	netip.MustParsePrefix("192.0.2.0/24"),  // Documentation.
	netip.MustParsePrefix("198.18.0.0/15"), // Benchmarking.
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("64:ff9b:1::/48"), // Local-use NAT64.
	netip.MustParsePrefix("2001:2::/48"),    // Benchmarking.
	netip.MustParsePrefix("2001::/32"),      // Teredo transition.
	netip.MustParsePrefix("2001:db8::/32"),  // Documentation.
}

var (
	oidcNAT64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	oidc6to4Prefix  = netip.MustParsePrefix("2002::/16")
)

// NewOIDCHTTPClient builds the proxy's OIDC client. It pins each DNS lookup to
// the address actually dialed and rejects special-use destinations such as
// loopback, link-local, unspecified, and multicast addresses. Private unicast
// addresses remain supported for cluster-local and private Keycloak deployments.
// Shared, documentation, and benchmarking prefixes are rejected separately.
func NewOIDCHTTPClient(tlsConfig *tls.Config) *http.Client {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	clientTLSConfig := tlsConfig.Clone()
	if clientTLSConfig.MinVersion < tls.VersionTLS12 {
		clientTLSConfig.MinVersion = tls.VersionTLS12
	}
	transport := &http.Transport{
		TLSClientConfig:     clientTLSConfig,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 2,
		DialContext:         safeOIDCDialContext,
	}

	return &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: rejectOIDCRedirect,
	}
}

func rejectOIDCRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func withOIDCIssuerRedirects(client *http.Client, issuer *url.URL) *http.Client {
	if client == nil {
		client = NewOIDCHTTPClient(nil)
	}
	clone := *client
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 OIDC redirects")
		}
		if request.URL.User != nil || request.URL.Fragment != "" || !sameOrigin(issuer, request.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &clone
}

type oidcIPLookup func(context.Context, string) ([]net.IPAddr, error)
type oidcDialContext func(context.Context, string, string) (net.Conn, error)

func safeOIDCDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return dialResolvedOIDCAddress(
		ctx,
		network,
		address,
		net.DefaultResolver.LookupIPAddr,
		dialer.DialContext,
	)
}

func dialResolvedOIDCAddress(
	ctx context.Context,
	network, address string,
	lookup oidcIPLookup,
	dial oidcDialContext,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid OIDC destination")
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("OIDC destination lookup failed")
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("OIDC destination has no resolved addresses")
	}
	for _, address := range addresses {
		if isUnsafeOIDCAddress(address.IP) {
			return nil, fmt.Errorf("OIDC destination resolved to a non-routable address")
		}
	}

	var lastErr error
	for _, address := range addresses {
		dialHost := address.IP.String()
		if address.Zone != "" {
			dialHost += "%" + address.Zone
		}
		conn, err := dial(ctx, network, net.JoinHostPort(dialHost, port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("OIDC destination could not be dialed")
}

func isUnsafeOIDCAddress(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return true
	}
	if address.Is4() {
		ipv4 := address.As4()
		if ipv4[0] == 0 || ipv4[0] >= 240 {
			return true
		}
	}
	for _, prefix := range oidcSpecialUsePrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	// These transition prefixes can carry an IPv4 destination inside an
	// otherwise global-unicast IPv6 address.
	if oidcNAT64Prefix.Contains(address) {
		bytes := address.As16()
		return isUnsafeTranslatedIPv4(netip.AddrFrom4([4]byte(bytes[12:16])))
	}
	if oidc6to4Prefix.Contains(address) {
		bytes := address.As16()
		return isUnsafeTranslatedIPv4(netip.AddrFrom4([4]byte(bytes[2:6])))
	}
	return false
}

func isUnsafeTranslatedIPv4(address netip.Addr) bool {
	return !address.Is4() || address.IsPrivate() || isUnsafeOIDCAddress(net.IP(address.AsSlice()))
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

func parseOIDCIssuer(raw string) (*url.URL, error) {
	issuer, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(issuer.Scheme, "https") || issuer.Host == "" || issuer.Hostname() == "" ||
		issuer.User != nil || issuer.Opaque != "" || issuer.RawQuery != "" || issuer.ForceQuery ||
		issuer.Fragment != "" || issuer.RawFragment != "" {
		return nil, errors.New("OIDC issuer must be an HTTPS URL without credentials, query, or fragment")
	}
	issuer.Scheme = "https"
	return issuer, nil
}

func parseOIDCEndpoint(raw, label string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.Opaque != "" || endpoint.Fragment != "" || endpoint.RawFragment != "" {
		return nil, fmt.Errorf("OIDC %s must be an HTTPS URL without credentials or fragment", label)
	}
	endpoint.Scheme = "https"
	return endpoint, nil
}

func sameOIDCIssuer(left, right string) bool {
	leftURL, err := parseOIDCIssuer(left)
	if err != nil {
		return false
	}
	rightURL, err := parseOIDCIssuer(right)
	if err != nil {
		return false
	}
	return sameOrigin(leftURL, rightURL) &&
		strings.TrimSuffix(leftURL.EscapedPath(), "/") == strings.TrimSuffix(rightURL.EscapedPath(), "/")
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) && effectivePort(left) == effectivePort(right)
}

func effectivePort(endpoint *url.URL) string {
	if port := endpoint.Port(); port != "" {
		return port
	}
	if strings.EqualFold(endpoint.Scheme, "https") {
		return "443"
	}
	return "80"
}

func validateOIDCMetadata(issuerURL string, cfg *OIDCConfig) error {
	issuer, err := parseOIDCIssuer(issuerURL)
	if err != nil {
		return err
	}
	if cfg == nil || !sameOIDCIssuer(cfg.Issuer, issuerURL) {
		return errors.New("OIDC discovery issuer did not match the configured issuer")
	}
	if cfg.AuthorizationEndpoint != "" {
		if _, err := parseOIDCEndpoint(cfg.AuthorizationEndpoint, "authorization endpoint"); err != nil {
			return err
		}
	}
	if cfg.TokenEndpoint == "" {
		return errors.New("OIDC discovery did not contain a token endpoint")
	}
	if err := validateIssuerOriginEndpoint(issuer, cfg.TokenEndpoint, "token endpoint"); err != nil {
		return err
	}
	if cfg.EndSessionEndpoint != "" {
		if _, err := parseOIDCEndpoint(cfg.EndSessionEndpoint, "end-session endpoint"); err != nil {
			return err
		}
	}
	return nil
}

func validateIssuerOriginEndpoint(issuer *url.URL, raw, label string) error {
	endpoint, err := parseOIDCEndpoint(raw, label)
	if err != nil {
		return err
	}
	if !sameOrigin(issuer, endpoint) {
		return fmt.Errorf("OIDC %s must use the configured issuer origin", label)
	}
	return nil
}

// FetchOIDCConfig returns the OIDC discovery document, using a per-issuer 5-minute cache.
// If httpClient is nil, the proxy's safe OIDC client is used.
func FetchOIDCConfig(issuerURL string, httpClient *http.Client) (*OIDCConfig, error) {
	issuerURL = strings.TrimSpace(issuerURL)
	issuer, err := parseOIDCIssuer(issuerURL)
	if err != nil {
		return nil, err
	}

	oidcCacheMu.RLock()
	if e, ok := oidcCacheByURL[issuerURL]; ok && time.Now().Before(e.expires) {
		cfg := e.cfg
		oidcCacheMu.RUnlock()
		return &cfg, nil
	}
	oidcCacheMu.RUnlock()

	discoveryURL := strings.TrimSuffix(issuer.String(), "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build OIDC discovery request: %w", sanitizeNetErr(err))
	}
	resp, err := withOIDCIssuerRedirects(httpClient, issuer).Do(req)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOIDCMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read OIDC discovery response: %w", err)
	}
	if len(body) > maxOIDCMetadataBytes {
		return nil, errors.New("OIDC discovery response was too large")
	}
	var cfg OIDCConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("decode OIDC config: %w", err)
	}
	if err := validateOIDCMetadata(issuerURL, &cfg); err != nil {
		return nil, err
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
// If httpClient is nil, the proxy's safe OIDC client is used.
func ExchangeCode(cfg *OIDCConfig, clientID, code, verifier, redirectURI string, httpClient *http.Client) (*TokenResponse, error) {
	if cfg == nil {
		return nil, errors.New("OIDC configuration is required")
	}
	params := url.Values{}
	params.Set("grant_type", "authorization_code")
	params.Set("client_id", clientID)
	params.Set("code", code)
	params.Set("code_verifier", verifier)
	params.Set("redirect_uri", redirectURI)

	return postTokenEndpoint(cfg.Issuer, cfg.TokenEndpoint, params, httpClient)
}

// EndSession calls the IdP end_session endpoint to invalidate the session server-side.
// It sends client_id and refresh_token (for Keycloak back-channel logout) as well as
// id_token_hint when available.
// If httpClient is nil, the proxy's safe OIDC client is used.
func EndSession(cfg *OIDCConfig, clientID, refreshToken, idToken string, httpClient *http.Client) error {
	if cfg == nil {
		return errors.New("OIDC configuration is required")
	}
	if cfg.EndSessionEndpoint == "" {
		return fmt.Errorf("OIDC end_session_endpoint not available")
	}
	issuer, err := parseOIDCIssuer(cfg.Issuer)
	if err != nil {
		return err
	}
	if err := validateIssuerOriginEndpoint(issuer, cfg.EndSessionEndpoint, "end-session endpoint"); err != nil {
		return err
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

	resp, err := withOIDCIssuerRedirects(httpClient, issuer).Do(req)
	if err != nil {
		return fmt.Errorf("end_session request: %w", sanitizeNetErr(err))
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
// If httpClient is nil, the proxy's safe OIDC client is used.
func ClientCredentialsToken(issuerURL, clientID, clientSecret string, httpClient *http.Client) (*TokenResponse, error) {
	cfg, err := FetchOIDCConfig(issuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", clientID)
	params.Set("client_secret", clientSecret)
	return postTokenEndpoint(cfg.Issuer, cfg.TokenEndpoint, params, httpClient)
}

// RefreshTokens exchanges a refresh token for new tokens at the IdP token endpoint.
// If httpClient is nil, the proxy's safe OIDC client is used.
func RefreshTokens(cfg *OIDCConfig, clientID, refreshToken string, httpClient *http.Client) (*TokenResponse, error) {
	if cfg == nil {
		return nil, errors.New("OIDC configuration is required")
	}
	params := url.Values{}
	params.Set("grant_type", "refresh_token")
	params.Set("client_id", clientID)
	params.Set("refresh_token", refreshToken)

	return postTokenEndpoint(cfg.Issuer, cfg.TokenEndpoint, params, httpClient)
}

func postTokenEndpoint(issuerURL, endpoint string, params url.Values, httpClient *http.Client) (*TokenResponse, error) {
	issuer, err := parseOIDCIssuer(issuerURL)
	if err != nil {
		return nil, err
	}
	if err := validateIssuerOriginEndpoint(issuer, endpoint, "token endpoint"); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", sanitizeNetErr(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := withOIDCIssuerRedirects(httpClient, issuer).Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request: %w", sanitizeNetErr(err))
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("failed to close response body")
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOIDCMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response body: %w", err)
	}
	if len(body) > maxOIDCMetadataBytes {
		return nil, errors.New("token endpoint response was too large")
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
