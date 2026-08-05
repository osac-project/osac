/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/osac-project/fulfillment-service/internal/auth"
)

// APIError represents an HTTP API error with status code and response body.
type APIError struct {
	StatusCode int
	Body       string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("API error: status=%d body=%s", e.StatusCode, e.Body)
}

// Client is a reusable HTTP client with authentication and error handling.
// It's designed for calling external REST APIs that require bearer token authentication.
type Client struct {
	baseURL     string
	httpClient  *http.Client
	tokenSource auth.TokenSource
	logger      *slog.Logger
}

// ClientBuilder builds an authenticated HTTP client.
type ClientBuilder struct {
	baseURL     string
	tokenSource auth.TokenSource
	caPool      *x509.CertPool
	httpClient  *http.Client
	logger      *slog.Logger
}

// NewClient creates a builder for an HTTP client.
func NewClient() *ClientBuilder {
	return &ClientBuilder{}
}

// SetLogger sets the logger.
func (b *ClientBuilder) SetLogger(value *slog.Logger) *ClientBuilder {
	b.logger = value
	return b
}

// SetBaseURL sets the base URL for the API.
func (b *ClientBuilder) SetBaseURL(value string) *ClientBuilder {
	b.baseURL = value
	return b
}

// SetTokenSource sets the authentication token source.
func (b *ClientBuilder) SetTokenSource(value auth.TokenSource) *ClientBuilder {
	b.tokenSource = value
	return b
}

// SetCaPool sets a custom CA pool for TLS verification.
func (b *ClientBuilder) SetCaPool(value *x509.CertPool) *ClientBuilder {
	b.caPool = value
	return b
}

// SetHTTPClient sets a custom HTTP client.
func (b *ClientBuilder) SetHTTPClient(value *http.Client) *ClientBuilder {
	b.httpClient = value
	return b
}

// Build creates the HTTP client.
func (b *ClientBuilder) Build() (result *Client, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.baseURL == "" {
		err = errors.New("base URL is mandatory")
		return
	}
	if b.tokenSource == nil {
		err = errors.New("token source is mandatory")
		return
	}

	httpClient := b.httpClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if b.caPool != nil {
			transport.TLSClientConfig = &tls.Config{
				RootCAs: b.caPool,
			}
		}
		httpClient = &http.Client{
			Transport: transport,
		}
	}

	result = &Client{
		baseURL:     b.baseURL,
		httpClient:  httpClient,
		tokenSource: b.tokenSource,
		logger:      b.logger,
	}
	return
}

// DoRequest performs an authenticated HTTP request with JSON handling.
//
// Parameters:
//   - method: HTTP method (GET, POST, PUT, DELETE, etc.)
//   - path: API path (will be appended to baseURL)
//   - body: Request body (will be JSON-encoded), or nil for no body
//
// Returns:
//   - response: HTTP response when successful (caller must close response.Body)
//   - error: Error if request fails or response status >= 400
//
// IMPORTANT: Returns nil response when an error occurs (including HTTP status >= 400).
// Always check err before accessing response to avoid nil pointer dereference.
//
// On success, the response body is NOT automatically closed - the caller must defer response.Body.Close().
func (c *Client) DoRequest(ctx context.Context, method, path string, body any) (response *http.Response, err error) {
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		err = fmt.Errorf("failed to get authentication token: %w", err)
		return
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			err = fmt.Errorf("failed to marshal request body: %w", marshalErr)
			return
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	requestURL := c.baseURL + path
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		err = fmt.Errorf("failed to create HTTP request: %w", err)
		return
	}

	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	c.logger.DebugContext(ctx, "HTTP API request",
		slog.String("method", method),
		slog.String("path", path),
	)

	response, err = c.httpClient.Do(request)
	if err != nil {
		err = fmt.Errorf("failed to send HTTP request: %w", err)
		return
	}

	if response.StatusCode >= 400 {
		// Limit error response body to 1MB to prevent memory exhaustion
		// from malicious or misbehaving upstream servers
		bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()

		err = &APIError{
			StatusCode: response.StatusCode,
			Body:       string(bodyBytes),
		}
		// Set response to nil so callers can't accidentally access a closed response.
		// This enforces checking err before using response.
		response = nil
		return
	}

	return
}

// RefreshToken invalidates the cached token, forcing a fresh token to be generated on the next request.
// This is useful when external changes require a new token (e.g., after creating a new realm).
func (c *Client) RefreshToken(ctx context.Context) error {
	return c.tokenSource.Invalidate(ctx)
}
