/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

// SchemaVersion is the current version of the lifecycle event schema.
const SchemaVersion = "v1"

const (
	UsageSemanticsInterval    = "interval"
	UsageSemanticsCumulative  = "cumulative"
	UsageUnitGiByteSecond     = "gibibyte_second"
	UsagePrecisionMicrosecond = "microsecond"
)

// LifecycleData is the canonical JSON payload for lifecycle and scaling events
// across all resource types. It defines the contract between the metering-service
// producer and adapter consumers.
type LifecycleData struct {
	ResourceID        string         `json:"resource_id"`
	ResourceType      string         `json:"resource_type"`
	TenantID          string         `json:"tenant_id"`
	ProjectID         *string        `json:"project_id"`
	CatalogItemID     *string        `json:"catalog_item_id"`
	TemplateID        *string        `json:"template_id"`
	PreviousState     *string        `json:"previous_state"`
	CurrentState      string         `json:"current_state"`
	TransitionTime    string         `json:"transition_time"`
	DurationSeconds   *float64       `json:"duration_seconds"`
	Usage             *Usage         `json:"usage,omitempty"`
	BillingDimensions map[string]any `json:"billing_dimensions"`
	SchemaVersion     string         `json:"schema_version"`
}

// Usage is the canonical allocation usage record. Timestamps are UTC and
// normalized to microsecond precision. Quantity is fixed-point text so JSON
// decoding cannot turn billing values into binary floating-point numbers.
type Usage struct {
	Semantics string `json:"semantics"`
	From      string `json:"from"`
	To        string `json:"to"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
	Precision string `json:"precision"`
}

// lifecycleDataFields holds the canonical field list. Unexported to prevent
// mutation; callers use LifecycleDataFields().
var lifecycleDataFields = [...]string{
	"resource_id",
	"resource_type",
	"tenant_id",
	"project_id",
	"catalog_item_id",
	"template_id",
	"previous_state",
	"current_state",
	"transition_time",
	"duration_seconds",
	"usage",
	"schema_version",
}

// LifecycleDataFields returns the JSON field names of LifecycleData in struct
// field order, excluding billing_dimensions (which adapters merge separately).
// Returns a fresh copy on each call so callers cannot mutate the canonical list.
func LifecycleDataFields() []string {
	out := make([]string, len(lifecycleDataFields))
	copy(out, lifecycleDataFields[:])
	return out
}
