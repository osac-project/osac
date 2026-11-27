/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package costmanagement implements the OSAC ProviderAdapter for Red Hat Cost
// Management. It maps published OSAC metering CloudEvents into an explicit,
// adapter-owned CloudEvents 1.0 contract so OSAC schema evolution and internal
// pipeline fields cannot leak across the provider boundary without a deliberate
// mapping update.
package costmanagement

// ContractVersion identifies the Cost Management ingestion payload schema
// version locked by this adapter.
const ContractVersion = "v1"

// EventTypeInferenceUsage is the CloudEvent type for MaaS inference usage
// delivered on the shared inference topic.
const EventTypeInferenceUsage = "osac.inference.usage.v1"

// ResourceTypeMaaSInference is the resource type carried by MaaS inference
// usage events.
const ResourceTypeMaaSInference = "maas_inference"

// CloudEventEnvelope is the locked CloudEvents 1.0 structured-mode wire
// contract sent to Red Hat Cost Management's POST /api/v1/events endpoint.
//
// Owning these explicit target structs instead of marshaling raw OSAC
// CloudEvent bytes ensures every field crossing the provider boundary is
// intentionally mapped and verified by unit and golden tests.
type CloudEventEnvelope struct {
	SpecVersion     string    `json:"specversion"`
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	Type            string    `json:"type"`
	DataContentType string    `json:"datacontenttype"`
	Time            string    `json:"time"`
	Subject         string    `json:"subject,omitempty"`
	ResourceID      string    `json:"osacresourceid"`
	ResourceType    string    `json:"osacresourcetype"`
	Tenant          string    `json:"osactenant"`
	Project         string    `json:"osacproject,omitempty"`
	Data            UsageData `json:"data"`
}

// UsageData is the locked Cost Management usage payload inside
// CloudEventEnvelope. OSAC-internal bookkeeping fields (such as
// last_applied_offset and rules_revision) are intentionally excluded.
type UsageData struct {
	SchemaVersion            string            `json:"schema_version"`
	TenantID                 string            `json:"tenant_id"`
	ExternalBillingAccountID string            `json:"external_billing_account_id,omitempty"`
	ProjectID                string            `json:"project_id,omitempty"`
	ResourceType             string            `json:"resource_type"`
	ResourceID               string            `json:"resource_id"`
	Meter                    string            `json:"meter"`
	Component                string            `json:"component"`
	HeartbeatTime            string            `json:"heartbeat_time,omitempty"`
	Usage                    UsageInterval     `json:"usage"`
	BillingDimensions        map[string]string `json:"billing_dimensions,omitempty"`
}

// UsageInterval carries one measured usage slice in Cost Management's
// contract. Quantity is fixed-point decimal text; negative quantities represent
// billing corrections that Cost Management adds to prior usage.
type UsageInterval struct {
	Semantics string `json:"semantics"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
	Precision string `json:"precision,omitempty"`
}
