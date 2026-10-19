/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package m360

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/shared/m360http"
)

const maxBodySize = 1 << 20

// m360Client sends events to the M360 Usage API.
type m360Client struct {
	transport *m360http.Client
	configErr error
	logger    logr.Logger
}

// newM360Client creates a client for the M360 Usage API.
func newM360Client(baseURL, apiVersion, apiKey string) *m360Client {
	transport, err := m360http.NewClient(m360http.Config{BaseURL: baseURL, APIVersion: apiVersion, APIKey: apiKey})
	return &m360Client{transport: transport, configErr: err, logger: logr.Discard()}
}

// post sends a flat M360 payload to the given endpoint.
// Returns NonRetryableError for 4xx responses (except 408 and 429,
// which are retryable), plain error for 5xx.
func (c *m360Client) post(ctx context.Context, endpoint string, payload map[string]any) error {
	if c.configErr != nil {
		return &adapters.NonRetryableError{Err: errors.New("M360 client configuration is invalid")}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return &adapters.NonRetryableError{
			Err: fmt.Errorf("marshal M360 payload: %w", err),
		}
	}

	pathSegments := strings.Split(strings.Trim(endpoint, "/"), "/")
	url := c.transport.Endpoint("external/run", pathSegments...)
	req, err := c.transport.NewRequest(ctx, http.MethodPost, url, bytes.NewReader(body), "application/json")
	if err != nil {
		return fmt.Errorf("create M360 request: %w", err)
	}

	resp, err := c.transport.Do(req)
	if err != nil {
		return errors.New("M360 request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if readErr != nil {
		return errors.New("M360 response could not be read")
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.logResponse(respBody)
		return nil
	}

	errMsg := fmt.Sprintf("M360 request returned HTTP %d", resp.StatusCode)
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
	if c.configErr != nil {
		return errors.New("M360 client configuration is invalid")
	}
	return c.transport.HealthCheck(ctx)
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
