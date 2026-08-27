/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/adapters"
)

const (
	httpTimeout = 30 * time.Second
	// healthCheckTimeout is derived from httpTimeout so the invariant
	// (healthCheckTimeout < httpTimeout) is structural. Capped at 3s to
	// stay well below the K8s readiness probe timeoutSeconds (5s) even if
	// httpTimeout is increased.
	healthCheckTimeout = min(httpTimeout/10, 3*time.Second)
	// maxBodyLog caps M360 response body excerpts in error messages
	// to avoid leaking large payloads into logs.
	maxBodyLog = 256
)

// m360Client sends events to the M360 Usage API.
type m360Client struct {
	httpClient *http.Client
	baseURL    string
	apiVersion string
	apiKey     string
	logger     logr.Logger
}

// newM360Client creates a client for the M360 Usage API.
func newM360Client(baseURL, apiVersion, apiKey string) *m360Client {
	return &m360Client{
		httpClient: &http.Client{Timeout: httpTimeout},
		baseURL:    baseURL,
		apiVersion: apiVersion,
		apiKey:     apiKey,
		logger:     logr.Discard(),
	}
}

// post sends a flat M360 payload to the given endpoint.
// Returns NonRetryableError for 4xx responses (except 408 and 429,
// which are retryable), plain error for 5xx.
func (c *m360Client) post(ctx context.Context, endpoint string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return &adapters.NonRetryableError{
			Err: fmt.Errorf("marshal M360 payload: %w", err),
		}
	}

	url := fmt.Sprintf("%s/api/%s/external/run%s", c.baseURL, c.apiVersion, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create M360 request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("M360 request to %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if readErr != nil {
		respBody = []byte("[body read error]")
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.logResponse(respBody)
		return nil
	}

	excerpt := string(respBody)
	if len(excerpt) > maxBodyLog {
		excerpt = excerpt[:maxBodyLog] + "...(truncated)"
	}
	errMsg := fmt.Sprintf("M360 %s returned %d: %s", endpoint, resp.StatusCode, excerpt)
	// 408 (Request Timeout) and 429 (Too Many Requests) are retryable.
	if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
		resp.StatusCode != http.StatusRequestTimeout &&
		resp.StatusCode != http.StatusTooManyRequests {
		return &adapters.NonRetryableError{Err: errors.New(errMsg)}
	}
	return errors.New(errMsg)
}

// healthCheck verifies TCP/TLS connectivity to the M360 base URL.
// Any HTTP response (regardless of status code) means M360 is reachable.
// Only connection-level errors (timeout, refused, DNS) cause failure.
// Auth and API route failures surface as post() errors /
// osac_metering_adapter_events_failed_total, not here.
func (c *m360Client) healthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL, nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("M360 health check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// logResponse extracts event_id from a successful M360 response for debug logging.
func (c *m360Client) logResponse(body []byte) {
	var resp struct {
		Output struct {
			EventID string `json:"event_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && resp.Output.EventID != "" {
		c.logger.V(1).Info("M360 event accepted", "m360_event_id", resp.Output.EventID)
	}
}
