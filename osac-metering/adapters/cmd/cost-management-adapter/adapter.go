/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

const (
	maxBatchEvents            = 100
	maxBatchBytes             = 1 << 20
	resourceTypeMaaSInference = "maas_inference"
)

type bufferedEvent struct {
	encoded json.RawMessage
}

type costManagementAdapter struct {
	client *costManagementClient

	mu      sync.Mutex
	flushMu sync.Mutex
	pending []bufferedEvent
}

func newCostManagementAdapter(client *costManagementClient) *costManagementAdapter {
	return &costManagementAdapter{client: client}
}

func (a *costManagementAdapter) Name() string { return "cost-management" }

// Submit performs deterministic validation before retaining the structured
// CloudEvent. A validation error is deliberately non-retryable: the Runner
// can route exactly that Kafka record to its DLQ.
func (a *costManagementAdapter) Submit(_ context.Context, event adapters.MeteringEvent) error {
	if err := validateCloudEvent(event.CloudEvent); err != nil {
		return &adapters.NonRetryableError{Err: err}
	}
	encoded, err := json.Marshal(event.CloudEvent)
	if err != nil {
		return &adapters.NonRetryableError{Err: fmt.Errorf("marshal CloudEvent: %w", err)}
	}
	// A valid single event must fit inside the Cost contract once JSON envelope
	// bytes are included. It could never be delivered in a valid batch.
	if len(encoded)+len(`{"events":[]}`) > maxBatchBytes {
		return &adapters.NonRetryableError{Err: fmt.Errorf("CloudEvent exceeds %d-byte batch limit", maxBatchBytes)}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = append(a.pending, bufferedEvent{encoded: encoded})
	return nil
}

func (a *costManagementAdapter) Flush(ctx context.Context) (adapters.SubmitResult, error) {
	a.flushMu.Lock()
	defer a.flushMu.Unlock()

	for {
		a.mu.Lock()
		batch := nextBatch(a.pending)
		a.mu.Unlock()
		if len(batch) == 0 {
			return adapters.SubmitResult{Idempotent: true}, nil
		}

		payload, err := marshalBatch(batch)
		if err != nil {
			return adapters.SubmitResult{}, fmt.Errorf("marshal Cost Management batch: %w", err)
		}
		if err := a.client.postBatch(ctx, payload); err != nil {
			// Keep every buffered record. The receiver's receipt ledger makes a
			// replay after a partial network failure safe.
			return adapters.SubmitResult{}, err
		}

		a.mu.Lock()
		// Flush is serialized and Submit appends only, so the accepted batch is
		// always the current prefix even while new events arrive.
		a.pending = a.pending[len(batch):]
		a.mu.Unlock()
	}
}

func (a *costManagementAdapter) HealthCheck(ctx context.Context) error {
	return a.client.healthCheck(ctx)
}

func (a *costManagementAdapter) Close() error { return nil }

func (a *costManagementAdapter) pendingCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

func nextBatch(pending []bufferedEvent) []bufferedEvent {
	if len(pending) == 0 {
		return nil
	}
	// The empty batch is {"events":[]}; each member contributes its encoded
	// bytes plus a comma except the first member.
	size := len(`{"events":[]}`)
	batch := make([]bufferedEvent, 0, min(len(pending), maxBatchEvents))
	for _, event := range pending {
		additional := len(event.encoded)
		if len(batch) > 0 {
			additional++
		}
		if len(batch) == maxBatchEvents || size+additional > maxBatchBytes {
			break
		}
		size += additional
		batch = append(batch, event)
	}
	return batch
}

func validateCloudEvent(ce cloudevents.Event) error {
	if ce.SpecVersion() != "1.0" {
		return fmt.Errorf("CloudEvent must use specversion 1.0")
	}
	if ce.ID() == "" || ce.Type() == "" || ce.Source() == "" || ce.Time().IsZero() {
		return errors.New("CloudEvent must include id, type, source, and time")
	}
	if ce.DataContentType() != "application/json" {
		return fmt.Errorf("CloudEvent datacontenttype must be application/json")
	}

	var data struct {
		ResourceID     string `json:"resource_id"`
		ResourceType   string `json:"resource_type"`
		TenantID       string `json:"tenant_id"`
		TransitionTime string `json:"transition_time"`
		SchemaVersion  string `json:"schema_version"`
	}
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		return fmt.Errorf("CloudEvent data must be JSON: %w", err)
	}
	if data.ResourceID == "" || data.ResourceType == "" || data.TenantID == "" ||
		data.TransitionTime == "" || data.SchemaVersion == "" {
		return errors.New("CloudEvent data is missing required canonical identity or lifecycle fields")
	}
	if _, err := time.Parse(time.RFC3339, data.TransitionTime); err != nil {
		return fmt.Errorf("CloudEvent transition_time must be RFC3339: %w", err)
	}
	if data.SchemaVersion != schema.SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", data.SchemaVersion)
	}
	if !isSupportedResourceType(data.ResourceType) {
		return fmt.Errorf("unsupported resource_type %q", data.ResourceType)
	}
	if extensionString(ce, schema.ExtResourceID) != data.ResourceID ||
		extensionString(ce, schema.ExtResourceType) != data.ResourceType ||
		extensionString(ce, schema.ExtTenant) != data.TenantID {
		return errors.New("CloudEvent OSAC identity extensions must match payload identity")
	}
	return nil
}

func extensionString(ce cloudevents.Event, key string) string {
	value, ok := ce.Extensions()[key]
	if !ok {
		return ""
	}
	valueString, _ := value.(string)
	return valueString
}

func isSupportedResourceType(resourceType string) bool {
	return resourceType == schema.ResourceTypeComputeInstance ||
		resourceType == schema.ResourceTypeClusterOrder ||
		resourceType == resourceTypeMaaSInference
}
