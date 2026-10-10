package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const fulfillmentOAuthRequestTimeout = 10 * time.Second

type fulfillmentOAuthConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	CAFile       string
}

type fulfillmentOIDCMetadata struct {
	Issuer        string `json:"issuer"`
	TokenEndpoint string `json:"token_endpoint"`
}

// fulfillmentTokenSource discovers the token endpoint on demand. A failed
// discovery is not cached, so a temporary Keycloak outage can recover without
// restarting the operator.
type fulfillmentTokenSource struct {
	cfg        fulfillmentOAuthConfig
	httpClient *http.Client
	mu         sync.Mutex
	source     oauth2.TokenSource
}

func newFulfillmentTokenSource(cfg fulfillmentOAuthConfig) (oauth2.TokenSource, error) {
	_, err := parseSecureIssuerURL(cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid fulfillment issuer URL")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("fulfillment OAuth client ID is required")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("fulfillment OAuth client secret is required")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.CAFile != "" {
		pool, err := loadFulfillmentCAPool(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("invalid fulfillment OAuth CA bundle: %w", err)
		}
		if transport.TLSClientConfig != nil {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		} else {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = pool
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   fulfillmentOAuthRequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &fulfillmentTokenSource{cfg: cfg, httpClient: client}, nil
}

func parseSecureIssuerURL(raw string) (*url.URL, error) {
	issuer, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" {
		return nil, fmt.Errorf("issuer must be a secure HTTPS URL without user info, query, or fragment")
	}
	issuer.Path = strings.TrimRight(issuer.Path, "/")
	issuer.RawPath = ""
	return issuer, nil
}

func (s *fulfillmentTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	if s.source == nil {
		source, err := s.discoverTokenSource()
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		s.source = source
	}
	source := s.source
	s.mu.Unlock()

	token, err := source.Token()
	if err != nil {
		// oauth2 error values may include response bodies from the identity
		// provider. Do not propagate those bodies into operator logs.
		return nil, fmt.Errorf("requesting fulfillment access token failed")
	}
	return token, nil
}

func (s *fulfillmentTokenSource) discoverTokenSource() (oauth2.TokenSource, error) {
	issuer, _ := parseSecureIssuerURL(s.cfg.IssuerURL)
	discoveryURL := *issuer
	discoveryURL.Path = strings.TrimRight(discoveryURL.Path, "/") + "/.well-known/openid-configuration"
	discoveryURL.RawPath = ""

	ctx, cancel := context.WithTimeout(context.Background(), fulfillmentOAuthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building fulfillment issuer discovery request failed")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching fulfillment issuer metadata failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching fulfillment issuer metadata failed with status %d", resp.StatusCode)
	}
	var metadata fulfillmentOIDCMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decoding fulfillment issuer metadata failed")
	}
	if !sameIssuer(metadata.Issuer, s.cfg.IssuerURL) {
		return nil, fmt.Errorf("fulfillment issuer metadata did not match configured issuer")
	}
	tokenURL, err := url.Parse(metadata.TokenEndpoint)
	if err != nil || !sameOrigin(issuer, tokenURL) || tokenURL.User != nil ||
		tokenURL.RawQuery != "" || tokenURL.Fragment != "" || tokenURL.Path == "" {
		return nil, fmt.Errorf("fulfillment issuer advertised an invalid token endpoint")
	}

	ctx = context.WithValue(context.Background(), oauth2.HTTPClient, s.httpClient)
	config := clientcredentials.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		TokenURL:     tokenURL.String(),
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	return config.TokenSource(ctx), nil
}

func sameIssuer(discovered, configured string) bool {
	if _, err := parseSecureIssuerURL(discovered); err != nil {
		return false
	}
	if _, err := parseSecureIssuerURL(configured); err != nil {
		return false
	}
	return strings.TrimSpace(discovered) == strings.TrimSpace(configured)
}

func sameOrigin(issuer, endpoint *url.URL) bool {
	return endpoint != nil && endpoint.Scheme == "https" &&
		strings.EqualFold(endpoint.Hostname(), issuer.Hostname()) && effectivePort(endpoint) == effectivePort(issuer)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	return "443"
}
