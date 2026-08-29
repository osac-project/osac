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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const (
	costBatchPath    = "/api/v1/events/batch"
	costHTTPTimeout  = 30 * time.Second
	maxResponseBytes = 256
)

// batchRequest is the Cost Management batch ingestion contract. Events stay
// in CloudEvents structured form; no provider-specific mapping is performed.
type batchRequest struct {
	Events []cloudevents.Event `json:"events"`
}

type costManagementClient struct {
	httpClient *http.Client
	batchURL   string
	token      string
	baseURL    string
}

func newCostManagementClient(baseURL, token string) *costManagementClient {
	return &costManagementClient{
		httpClient: &http.Client{Timeout: costHTTPTimeout},
		batchURL:   strings.TrimRight(baseURL, "/") + costBatchPath,
		token:      token,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func validateCostManagementURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("COST_MANAGEMENT_API_URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("COST_MANAGEMENT_API_URL must use http or https")
	}
	return nil
}

// postBatch returns only retryable errors. Receiver 4xx values are retained
// for operator correction because a batch response has no per-event attribution
// that would make DLQ routing safe.
func (c *costManagementClient) postBatch(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.batchURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Cost Management batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cost management batch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		body = []byte("[response body unreadable]")
	}
	return fmt.Errorf("cost management batch endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *costManagementClient) healthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL, nil)
	if err != nil {
		return fmt.Errorf("create Cost Management health check request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cost management health check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func marshalBatch(events []bufferedEvent) ([]byte, error) {
	rawEvents := make([]json.RawMessage, len(events))
	for i := range events {
		rawEvents[i] = events[i].encoded
	}
	return json.Marshal(struct {
		Events []json.RawMessage `json:"events"`
	}{Events: rawEvents})
}
