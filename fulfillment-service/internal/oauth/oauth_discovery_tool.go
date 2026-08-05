/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/osac-project/fulfillment-service/internal/network"
)

// ServerMetadata represents the authorization server metadata structure as defined in RFC 8414 for OAuth 2.0 or in the
// OpenID Connect Discovery 1.0 specification. Note that this only contains the fields that we need, which happen to be
// available in both specifications.
type ServerMetadata struct {
	AuthorizationEndpoint       string   `json:"authorization_endpoint,omitempty"`
	DeviceAuthorizationEndpoint string   `json:"device_authorization_endpoint,omitempty"`
	EndSessionEndpoint          string   `json:"end_session_endpoint,omitempty"`
	Issuer                      string   `json:"issuer,omitempty"`
	ScopesSupported             []string `json:"scopes_supported,omitempty"`
	TokenEndpoint               string   `json:"token_endpoint,omitempty"`
}

// DiscoveryToolBuilder contains the logic needed to create an OAuth discovery tool.
type DiscoveryToolBuilder struct {
	logger   *slog.Logger
	issuer   string
	insecure bool
	caPool   *x509.CertPool
}

// DiscoveryTool contains the logic needed to discover OAuth endpoints from an issuer URL.
type DiscoveryTool struct {
	logger     *slog.Logger
	issuerUrl  string
	httpClient *http.Client
}

// NewDiscoveryTool creates a builder that can then be used to configure and create an OAuth discovery tool.
func NewDiscoveryTool() *DiscoveryToolBuilder {
	return &DiscoveryToolBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *DiscoveryToolBuilder) SetLogger(value *slog.Logger) *DiscoveryToolBuilder {
	b.logger = value
	return b
}

// SetIssuer sets the OAuth issuer URL for discovery. This is mandatory.
func (b *DiscoveryToolBuilder) SetIssuer(value string) *DiscoveryToolBuilder {
	b.issuer = value
	return b
}

// SetInsecure sets whether to skip TLS certificate verification. This is optional and defaults to false.
func (b *DiscoveryToolBuilder) SetInsecure(value bool) *DiscoveryToolBuilder {
	b.insecure = value
	return b
}

// SetCaPool sets the certificate pool that contains the certificates of the certificate authorities that are trusted
// when connecting using TLS. This is optional, and the default is to use trust the certificate authorities trusted by
// the operating system.
func (b *DiscoveryToolBuilder) SetCaPool(value *x509.CertPool) *DiscoveryToolBuilder {
	b.caPool = value
	return b
}

// Build uses the data stored in the builder to build a new OAuth discovery tool.
func (b *DiscoveryToolBuilder) Build() (result *DiscoveryTool, err error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.issuer == "" {
		return nil, errors.New("issuer is mandatory")
	}

	// Clean the issuer URL:
	issuerUrl := strings.TrimSuffix(b.issuer, "/")
	parsed, err := url.Parse(issuerUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid issuer URL: %w", err)
	}
	issuerUrl = parsed.String()

	// Set the default CA pool if needed:
	caPool := b.caPool
	if caPool == nil {
		caPool, err = network.NewCertPool().
			SetLogger(b.logger).
			AddSystemFiles(true).
			AddKubernetesFiles(true).
			Build()
		if err != nil {
			return nil, fmt.Errorf("failed to build CA pool: %w", err)
		}
	}

	// Create the HTTP client:
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	tlsConfig := &tls.Config{
		RootCAs: caPool,
	}
	if b.insecure {
		tlsConfig.InsecureSkipVerify = true
	}
	httpClient.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Create and populate the object:
	result = &DiscoveryTool{
		logger:     b.logger,
		issuerUrl:  issuerUrl,
		httpClient: httpClient,
	}
	return
}

// Discover discovers endpoints from the configured issuer URL using the well-known configuration endpoints. This
// implements the discovery process defined in RFC 8414 for OAuth and the OpenID Connect Discovery 1.0 specification.
func (t *DiscoveryTool) Discover(ctx context.Context) (result *ServerMetadata, err error) {
	result, err = t.discover(ctx, "oauth-authorization-server")
	if err == nil {
		t.logger.DebugContext(
			ctx,
			"Successfully discovered endpoints using OAuth endpoint",
		)
		return
	}
	t.logger.InfoContext(
		ctx,
		"Failed to discover endpoints using OAuth",
		slog.Any("error", err),
	)
	result, err = t.discover(ctx, "openid-configuration")
	if err == nil {
		t.logger.DebugContext(
			ctx,
			"Successfully discovered endpoints using OIDC",
		)
		return
	}
	t.logger.InfoContext(
		ctx,
		"Failed to discover endpoints using OIDC",
		slog.Any("error", err),
	)
	err = fmt.Errorf("failed to discover endpoints using OAuth or OIDC")
	return
}

func (t *DiscoveryTool) discover(ctx context.Context, endpoint string) (result *ServerMetadata, err error) {
	// Construct the well-known configuration URL:
	metadataUrl := fmt.Sprintf("%s/.well-known/%s", t.issuerUrl, endpoint)

	// Make the discovery request
	t.logger.DebugContext(
		ctx,
		"Attempting discovery",
		slog.String("url", metadataUrl),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataUrl, nil)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to create metadata request",
			slog.String("url", metadataUrl),
			slog.Any("error", err),
		)
		err = fmt.Errorf("failed to create metadata request: %w", err)
		return
	}
	response, err := t.httpClient.Do(request)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to fetch metadata",
			slog.String("url", metadataUrl),
			slog.Any("error", err),
		)
		err = fmt.Errorf("failed to fetch metadata from '%s': %w", metadataUrl, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to fetch metadata from '%s': %s", metadataUrl, response.Status)
		return
	}

	// Parse the discovery document:
	var serverMetadata ServerMetadata
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&serverMetadata)
	if err != nil {
		t.logger.ErrorContext(
			ctx,
			"Failed to parse metadata",
			slog.Any("error", err),
		)
		err = fmt.Errorf("failed to parse metadata: %w", err)
		return
	}

	// Validate required fields:
	if serverMetadata.Issuer == "" {
		t.logger.ErrorContext(
			ctx,
			"Discovery document missing required 'issuer' field",
		)
		err = fmt.Errorf("discovery document missing required 'issuer' field")
		return
	}
	if serverMetadata.TokenEndpoint == "" {
		t.logger.ErrorContext(
			ctx,
			"Discovery document missing required 'token_endpoint' field",
		)
		err = fmt.Errorf("discovery document missing required 'token_endpoint' field")
		return
	}

	// Return the result:
	t.logger.DebugContext(
		ctx,
		"Successfully discovered endpoints",
		slog.String("issuer", serverMetadata.Issuer),
		slog.String("token_endpoint", serverMetadata.TokenEndpoint),
		slog.String("authorization_endpoint", serverMetadata.AuthorizationEndpoint),
		slog.String("device_authorization_endpoint", serverMetadata.DeviceAuthorizationEndpoint),
		slog.Any("scopes_supported", serverMetadata.ScopesSupported),
	)
	result = &serverMetadata
	return
}
