/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package m360http shares M360 request construction, authentication,
// transport timeout, endpoint construction, and reachability checks between
// fulfillment billing and the metering adapter.
package m360http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout     = 30 * time.Second
	healthCheckTimeout = 3 * time.Second
)

// Config contains the shared M360 connection settings.
type Config struct {
	BaseURL           string
	APIVersion        string
	APIKey            string
	HTTPClient        *http.Client
	AllowInsecureHTTP bool // test-only opt-in for local httptest servers; leave false in production.
}

// Client builds authenticated M360 requests using one configured transport.
type Client struct {
	baseURL    *url.URL
	apiVersion string
	apiKey     string
	httpClient *http.Client
}

// NewClient validates the provider connection and applies the shared default
// HTTP timeout when no custom client is supplied.
func NewClient(config Config) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || baseURL.Host == "" || (baseURL.Scheme != "https" && !(config.AllowInsecureHTTP && baseURL.Scheme == "http")) || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("M360 API URL must be an absolute HTTPS URL without query or fragment")
	}
	if strings.TrimSpace(config.APIVersion) == "" || strings.ContainsAny(config.APIVersion, "/?#") {
		return nil, errors.New("M360 API version is invalid")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("M360 API key is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{
		baseURL: baseURL, apiVersion: strings.TrimSpace(config.APIVersion),
		apiKey: strings.TrimSpace(config.APIKey), httpClient: httpClient,
	}, nil
}

// Endpoint returns a provider URL below /api/{version}/{prefix}. Prefix may
// contain fixed path segments; each additional value is escaped as one path
// segment, so an account reference cannot add another route segment.
func (c *Client) Endpoint(prefix string, segments ...string) *url.URL {
	result := *c.baseURL
	decoded := make([]string, 0, 8+len(segments))
	escaped := make([]string, 0, 8+len(segments))
	for _, segment := range strings.Split(strings.Trim(result.Path, "/"), "/") {
		if segment == "" {
			continue
		}
		decoded = append(decoded, segment)
		escaped = append(escaped, url.PathEscape(segment))
	}
	appendParts := func(value string) {
		for _, part := range strings.Split(strings.Trim(value, "/"), "/") {
			if part == "" {
				continue
			}
			decoded = append(decoded, part)
			escaped = append(escaped, url.PathEscape(part))
		}
	}
	appendParts("api")
	appendParts(c.apiVersion)
	appendParts(prefix)
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		decoded = append(decoded, segment)
		escaped = append(escaped, url.PathEscape(segment))
	}
	result.Path = "/" + strings.Join(decoded, "/")
	result.RawPath = "/" + strings.Join(escaped, "/")
	return &result
}

// NewRequest creates an authenticated JSON-oriented HTTP request for M360.
// Callers select the content type because the metering API and account API
// have different success-body contracts.
func (c *Client) NewRequest(ctx context.Context, method string, endpoint *url.URL, body io.Reader, contentType string) (*http.Request, error) {
	if endpoint == nil {
		return nil, errors.New("M360 endpoint is required")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, errors.New("failed to create M360 request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

// Do sends a request through the shared M360 transport.
func (c *Client) Do(request *http.Request) (*http.Response, error) {
	return c.httpClient.Do(request)
}

// HealthCheck performs a short HEAD request to the configured base URL. Any
// HTTP response means the provider endpoint is reachable; authentication and
// API route failures are handled by the operation-specific calls.
func (c *Client) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL.String(), nil)
	if err != nil {
		return errors.New("failed to create M360 health check request")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return errors.New("M360 health check failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return nil
}
