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
	defaultEventsPath = "/api/v1/events"
	defaultReadyPath  = "/readyz"
	httpTimeout       = 30 * time.Second
	// healthCheckTimeout stays below the Kubernetes readiness probe timeout (5s)
	// even if httpTimeout is increased.
	healthCheckTimeout = min(httpTimeout/10, 3*time.Second)
	// maxBodyLog caps response body excerpts in error messages to avoid leaking
	// large payloads into logs or DLQ headers.
	maxBodyLog = 256
)

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
	eventsURL  string
	readyURL   string
	token      string
	tokenFile  string
	logger     logr.Logger
}

func newClient(baseURL, token, tokenFile string, logger logr.Logger) *client {
	trimmed := strings.TrimRight(baseURL, "/")
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}
	return &client{
		httpClient: &http.Client{Timeout: httpTimeout},
		eventsURL:  trimmed + defaultEventsPath,
		readyURL:   trimmed + defaultReadyPath,
		token:      token,
		tokenFile:  tokenFile,
		logger:     logger,
	}
}

// post sends one translated CloudEventEnvelope to POST /api/v1/events.
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
	errMsg := fmt.Sprintf("cost management %s returned %d: %s", defaultEventsPath, resp.StatusCode, excerpt)
	if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError &&
		resp.StatusCode != http.StatusRequestTimeout &&
		resp.StatusCode != http.StatusTooManyRequests {
		return &adapters.NonRetryableError{Err: errors.New(errMsg)}
	}
	return errors.New(errMsg)
}

// healthCheck verifies the Cost Management readiness endpoint returns 2xx.
func (c *client) healthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

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
