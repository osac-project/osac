/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"encoding/json"
	"errors"
	"fmt"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/osac-project/osac-metering/adapters"
)

// resourceTypeEndpoints maps OSAC resource types to M360 API endpoints.
var resourceTypeEndpoints = map[string]string{
	"compute_instance": "/vmaas/event",
	"cluster_order":    "/caas/event",
	"maas_inference":   "/maas/event",
}

// spaceString is the M360 convention for non-applicable fields.
const spaceString = " "

// translateEvent converts a canonical OSAC CloudEvent to a flat M360
// Usage API payload and returns the target endpoint path.
func translateEvent(ce cloudevents.Event) (string, map[string]any, error) {
	if ce.ID() == "" {
		return "", nil, &adapters.NonRetryableError{
			Err: errors.New("CloudEvent has empty ID"),
		}
	}
	if ce.Time().IsZero() {
		return "", nil, &adapters.NonRetryableError{
			Err: errors.New("CloudEvent has zero timestamp"),
		}
	}

	var data map[string]any
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		return "", nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unmarshal CloudEvent data: %w", err),
		}
	}

	resourceType, _ := data["resource_type"].(string)
	endpoint, ok := resourceTypeEndpoints[resourceType]
	if !ok {
		return "", nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unknown resource_type %q", resourceType),
		}
	}

	payload := map[string]any{
		"specversion":     ce.SpecVersion(),
		"type":            ce.Type(),
		"source":          ce.Source(),
		"subject":         ce.Subject(),
		"event_time":      ce.Time().UTC().Format("2006-01-02T15:04:05Z"),
		"datacontenttype": ce.DataContentType(),
		"event_id":        ce.ID(),
	}

	// Copy data fields to top level.
	dataFields := []string{
		"resource_id", "resource_type", "tenant_id", "project_id",
		"catalog_item_id", "template_id", "previous_state", "current_state",
		"transition_time", "duration_seconds", "schema_version",
	}
	for _, key := range dataFields {
		payload[key] = nullToSpace(data[key])
	}

	// Merge billing_dimensions to top level, skipping non-billable
	// (nil, empty string, zero) values and protecting canonical fields
	// from being overwritten.
	if rawDims, hasDims := data["billing_dimensions"]; hasDims && rawDims != nil {
		dims, ok := rawDims.(map[string]any)
		if !ok {
			return "", nil, &adapters.NonRetryableError{
				Err: errors.New("billing_dimensions is not a map"),
			}
		}
		for k, v := range dims {
			if isZeroValue(v) {
				continue
			}
			if _, exists := payload[k]; exists {
				continue
			}
			payload[k] = v
		}
	}

	return endpoint, payload, nil
}

// nullToSpace replaces nil and empty string with the M360 space string.
func nullToSpace(v any) any {
	if v == nil {
		return spaceString
	}
	if s, ok := v.(string); ok && s == "" {
		return spaceString
	}
	return v
}

// isZeroValue returns true for nil, empty string, and numeric zero.
// JSON numbers are always decoded as float64 by encoding/json.
func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case float64:
		return val == 0
	}
	return false
}
