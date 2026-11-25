/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
)

const (
	// DefaultEventsPath is the default Cost Management single-event ingestion
	// path appended to BaseURL when EventsPath is not explicitly configured.
	DefaultEventsPath = "/api/v1/events"
	// DefaultReadyPath is the default readiness probe path appended to BaseURL.
	DefaultReadyPath = "/readyz"
	httpTimeout      = 30 * time.Second
	// healthCheckTimeout stays below the Kubernetes readiness probe timeout (5s)
	// even if httpTimeout is increased.
	healthCheckTimeout = min(httpTimeout/10, 3*time.Second)
	// maxBodyLog caps response body excerpts in error messages to avoid leaking
	// large payloads into logs or DLQ headers.
	maxBodyLog = 256
)

// ClientConfig configures the HTTP transport and endpoints for the Cost
// Management adapter. It supports both single-cluster direct Service calls and
// two-cluster HTTPS delivery through an authenticating gateway with a custom CA.
type ClientConfig struct {
	// BaseURL is the absolute HTTP(S) base URL of the Cost Management receiver
	// or gateway (for example http://cost-event-consumer.cost-mgmt.svc:8020 or
	// https://cost.apps.cluster-b.example.com/api/cost-events).
	BaseURL string
	// EventsPath is appended to BaseURL for event POSTs. Defaults to
	// DefaultEventsPath ("/api/v1/events"). Set to "/v1/events" when BaseURL
	// already includes an Envoy prefix rewrite such as "/api/cost-events".
	EventsPath string
	// ReadyPath is appended to BaseURL for GET readiness checks. When
	// ReadyPathSet is false, DefaultReadyPath ("/readyz") is used. When
	// ReadyPathSet is true and ReadyPath is "", healthCheck performs a
	// TCP/TLS reachability HEAD probe against BaseURL (useful behind an
	// authenticating gateway in a two-cluster deployment).
	ReadyPath    string
	ReadyPathSet bool
	// Token is an optional static Bearer token (used in tests or local dev).
	Token string
	// TokenFile is an optional path to a file containing a Bearer token,
	// re-read on every request to support Kubernetes Secret rotation.
	TokenFile string
	// CACertFile is an optional PEM CA bundle appended to the system cert pool
	// for verifying HTTPS endpoints served by a private or remote cluster CA.
	CACertFile string
	// Logger receives debug delivery logs; defaults to logr.Discard().
	Logger logr.Logger
}

// ValidateURL verifies that rawURL is an absolute http or https URL.
func ValidateURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("COST_MANAGEMENT_API_URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("COST_MANAGEMENT_API_URL must use http or https")
	}
	return nil
}

// client sends translated Cost Management CloudEvents over HTTP.
type client struct {
	httpClient *http.Client
	baseURL    string
	eventsPath string
	eventsURL  string
	readyURL   string
	token      string
	tokenFile  string
	logger     logr.Logger
}

func newClientFromConfig(cfg ClientConfig) (*client, error) {
	trimmed := strings.TrimRight(cfg.BaseURL, "/")
	eventsPath := cfg.EventsPath
	if eventsPath == "" {
		eventsPath = DefaultEventsPath
	}
	if !strings.HasPrefix(eventsPath, "/") {
		eventsPath = "/" + eventsPath
	}

	var readyURL string
	if !cfg.ReadyPathSet {
		readyURL = trimmed + DefaultReadyPath
	} else if cfg.ReadyPath != "" {
		rp := cfg.ReadyPath
		if !strings.HasPrefix(rp, "/") {
			rp = "/" + rp
		}
		readyURL = trimmed + rp
	}

	httpClient := &http.Client{Timeout: httpTimeout}
	if cfg.CACertFile != "" {
		transport, err := buildTLSTransport(cfg.CACertFile)
		if err != nil {
			return nil, err
		}
		httpClient.Transport = transport
	}

	logger := cfg.Logger
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}
	return &client{
		httpClient: httpClient,
		baseURL:    trimmed,
		eventsPath: eventsPath,
		eventsURL:  trimmed + eventsPath,
		readyURL:   readyURL,
		token:      cfg.Token,
		tokenFile:  cfg.TokenFile,
		logger:     logger,
	}, nil
}

func newClient(baseURL, token, tokenFile string, logger logr.Logger) *client {
	cl, err := newClientFromConfig(ClientConfig{
		BaseURL:   baseURL,
		Token:     token,
		TokenFile: tokenFile,
		Logger:    logger,
	})
	if err != nil {
		// Only CACertFile can cause newClientFromConfig to fail, and it is empty here.
		panic(err)
	}
	return cl
}

func buildTLSTransport(caCertFile string) (*http.Transport, error) {
	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("reading cost management CA cert %s: %w", caCertFile, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parsing cost management CA cert %s: no valid PEM certificates found", caCertFile)
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	tr := base.Clone()
	tr.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}
	return tr, nil
}

// post sends one translated CloudEventEnvelope to the configured events path.
// Returns *adapters.NonRetryableError for 4xx responses except 408 and 429;
// returns a retryable error for 408, 429, 5xx, and transport errors.
func (c *client) post(ctx context.Context, event *CloudEventEnvelope) error {
	body, err := json.Marshal(event)
	if err != nil {
		return &adapters.NonRetryableError{
			Err: fmt.Errorf("marshal Cost Management event: %w", err),
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.eventsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create cost management request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	token, err := c.authorizationToken()
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cost management request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		respBody = []byte("[body read error]")
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		c.logger.V(1).Info("Cost Management event accepted", "event_id", event.ID, "status", resp.StatusCode)
		return nil
	}

	excerpt := strings.TrimSpace(string(respBody))
	if len(excerpt) > maxBodyLog {
		excerpt = excerpt[:maxBodyLog] + "...(truncated)"
	}
	errMsg := fmt.Sprintf("cost management %s returned %d: %s", c.eventsPath, resp.StatusCode, excerpt)
	if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError &&
		resp.StatusCode != http.StatusRequestTimeout &&
		resp.StatusCode != http.StatusTooManyRequests {
		return &adapters.NonRetryableError{Err: errors.New(errMsg)}
	}
	return errors.New(errMsg)
}

// healthCheck verifies connectivity to Cost Management. When readyURL is set
// (default "/readyz"), it requires a 2xx response on GET. When readyURL is
// explicitly empty (for example behind an authenticating gateway in a
// two-cluster setup), any HTTP response to HEAD BaseURL confirms TCP/TLS
// reachability.
func (c *client) healthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	if c.readyURL == "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL, nil)
		if err != nil {
			return fmt.Errorf("create cost management reachability check: %w", err)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("cost management health check: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.readyURL, nil)
	if err != nil {
		return fmt.Errorf("create cost management health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cost management health check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("cost management readiness endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// authorizationToken resolves the current Bearer token. Reading tokenFile on
// every request ensures Kubernetes Secret rotation takes effect without pod
// restarts, and error messages never expose token bytes.
func (c *client) authorizationToken() (string, error) {
	if c.tokenFile == "" {
		return c.token, nil
	}
	raw, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("reading cost management API token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("cost management API token file is empty")
	}
	return token, nil
}
