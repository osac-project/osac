/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package costmanagement

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

// supportedHeartbeatResourceTypes enumerates the OSAC resource types accepted
// on per-tenant heartbeat topics.
var supportedHeartbeatResourceTypes = map[string]struct{}{
	schema.ResourceTypeComputeInstance:   {},
	schema.ResourceTypeClusterOrder:      {},
	schema.ResourceTypeBareMetalInstance: {},
	schema.ResourceTypeExternalIP:        {},
	schema.ResourceTypeNATGateway:        {},
	schema.ResourceTypeVolume:            {},
}

// osacInferenceData is the input shape decoded from osac.inference.usage.v1
// events on the shared MaaS inference topic.
type osacInferenceData struct {
	TenantID                 string         `json:"tenant_id"`
	ExternalBillingAccountID *string        `json:"external_billing_account_id,omitempty"`
	ProjectID                string         `json:"project_id,omitempty"`
	ResourceType             string         `json:"resource_type"`
	ResourceID               string         `json:"resource_id"`
	Meter                    string         `json:"meter,omitempty"`
	Component                string         `json:"component,omitempty"`
	BillingDimensions        map[string]any `json:"billing_dimensions,omitempty"`
	Usage                    schema.Usage   `json:"usage"`
}

// TranslateEvent validates an incoming OSAC CloudEvent and maps it into the
// locked Cost Management CloudEventEnvelope contract.
//
// All schema, identity, and unsupported-type failures return
// *adapters.NonRetryableError so the Runner routes the exact poison record to
// its dead-letter topic without retrying.
func TranslateEvent(ce cloudevents.Event) (*CloudEventEnvelope, error) {
	if err := validateEnvelope(ce); err != nil {
		return nil, &adapters.NonRetryableError{Err: err}
	}

	switch ce.Type() {
	case schema.HeartbeatType:
		return translateHeartbeat(ce)
	case EventTypeInferenceUsage:
		return translateInference(ce)
	default:
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unsupported CloudEvent type %q", ce.Type()),
		}
	}
}

func validateEnvelope(ce cloudevents.Event) error {
	if ce.SpecVersion() != cloudevents.VersionV1 {
		return fmt.Errorf("CloudEvent specversion must be %s, got %q", cloudevents.VersionV1, ce.SpecVersion())
	}
	if ce.ID() == "" {
		return errors.New("CloudEvent has empty ID")
	}
	if ce.Source() == "" {
		return errors.New("CloudEvent has empty source")
	}
	if ce.Type() == "" {
		return errors.New("CloudEvent has empty type")
	}
	if ce.Time().IsZero() {
		return errors.New("CloudEvent has zero timestamp")
	}
	if ct := ce.DataContentType(); ct != "" && ct != cloudevents.ApplicationJSON {
		return fmt.Errorf("CloudEvent datacontenttype must be %s, got %q", cloudevents.ApplicationJSON, ct)
	}
	return nil
}

func translateHeartbeat(ce cloudevents.Event) (*CloudEventEnvelope, error) {
	var data schema.HeartbeatData
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unmarshal heartbeat data: %w", err),
		}
	}

	if data.TenantID == "" || data.ResourceType == "" || data.ResourceID == "" {
		return nil, &adapters.NonRetryableError{
			Err: errors.New("heartbeat data missing required identity fields (tenant_id, resource_type, resource_id)"),
		}
	}
	if _, ok := supportedHeartbeatResourceTypes[data.ResourceType]; !ok {
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unsupported resource_type %q", data.ResourceType),
		}
	}
	if data.Meter == "" || data.Component == "" {
		return nil, &adapters.NonRetryableError{
			Err: errors.New("heartbeat data missing required meter or component"),
		}
	}
	if data.HeartbeatTime == "" {
		return nil, &adapters.NonRetryableError{
			Err: errors.New("heartbeat data missing heartbeat_time"),
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, data.HeartbeatTime); err != nil {
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("invalid heartbeat_time %q: %w", data.HeartbeatTime, err),
		}
	}
	if err := validateIntervalUsage(data.Usage); err != nil {
		return nil, &adapters.NonRetryableError{Err: err}
	}
	if err := validateMatchingExtensions(ce, data.ResourceID, data.ResourceType, data.TenantID); err != nil {
		return nil, &adapters.NonRetryableError{Err: err}
	}

	dims := copyStringDimensions(data.BillingDimensions)
	project := extensionString(ce, schema.ExtProject)
	if project == "" {
		project = dims["project"]
	}

	var billingAcct string
	if data.ExternalBillingAccountID != nil {
		billingAcct = strings.TrimSpace(*data.ExternalBillingAccountID)
	}

	return &CloudEventEnvelope{
		SpecVersion:     cloudevents.VersionV1,
		ID:              ce.ID(),
		Source:          ce.Source(),
		Type:            ce.Type(),
		DataContentType: cloudevents.ApplicationJSON,
		Time:            ce.Time().UTC().Format(time.RFC3339),
		Subject:         ce.Subject(),
		ResourceID:      data.ResourceID,
		ResourceType:    data.ResourceType,
		Tenant:          data.TenantID,
		Project:         project,
		Data: UsageData{
			SchemaVersion:            ContractVersion,
			TenantID:                 data.TenantID,
			ExternalBillingAccountID: billingAcct,
			ProjectID:                project,
			ResourceType:             data.ResourceType,
			ResourceID:               data.ResourceID,
			Meter:                    data.Meter,
			Component:                data.Component,
			HeartbeatTime:            data.HeartbeatTime,
			Usage: UsageInterval{
				Semantics: data.Usage.Semantics,
				From:      data.Usage.From,
				To:        data.Usage.To,
				Quantity:  data.Usage.Quantity,
				Unit:      data.Usage.Unit,
				Precision: data.Usage.Precision,
			},
			BillingDimensions: dims,
		},
	}, nil
}

func translateInference(ce cloudevents.Event) (*CloudEventEnvelope, error) {
	var data osacInferenceData
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unmarshal inference data: %w", err),
		}
	}

	if data.TenantID == "" || data.ResourceID == "" {
		return nil, &adapters.NonRetryableError{
			Err: errors.New("inference data missing required identity fields (tenant_id, resource_id)"),
		}
	}
	if data.ResourceType != ResourceTypeMaaSInference {
		return nil, &adapters.NonRetryableError{
			Err: fmt.Errorf("unexpected inference resource_type %q, want %q", data.ResourceType, ResourceTypeMaaSInference),
		}
	}
	if err := validateUsageQuantityAndUnit(data.Usage); err != nil {
		return nil, &adapters.NonRetryableError{Err: err}
	}
	if err := validateMatchingExtensions(ce, data.ResourceID, data.ResourceType, data.TenantID); err != nil {
		return nil, &adapters.NonRetryableError{Err: err}
	}

	dims := normalizeAnyDimensions(data.BillingDimensions)
	project := data.ProjectID
	if project == "" {
		project = extensionString(ce, schema.ExtProject)
	}
	if project == "" {
		project = dims["project"]
	}

	meter := data.Meter
	if meter == "" {
		meter = "inference"
	}
	component := data.Component
	if component == "" {
		component = meter
	}
	semantics := data.Usage.Semantics
	if semantics == "" {
		semantics = "delta"
	}

	var billingAcct string
	if data.ExternalBillingAccountID != nil {
		billingAcct = strings.TrimSpace(*data.ExternalBillingAccountID)
	}

	return &CloudEventEnvelope{
		SpecVersion:     cloudevents.VersionV1,
		ID:              ce.ID(),
		Source:          ce.Source(),
		Type:            ce.Type(),
		DataContentType: cloudevents.ApplicationJSON,
		Time:            ce.Time().UTC().Format(time.RFC3339),
		Subject:         ce.Subject(),
		ResourceID:      data.ResourceID,
		ResourceType:    data.ResourceType,
		Tenant:          data.TenantID,
		Project:         project,
		Data: UsageData{
			SchemaVersion:            ContractVersion,
			TenantID:                 data.TenantID,
			ExternalBillingAccountID: billingAcct,
			ProjectID:                project,
			ResourceType:             data.ResourceType,
			ResourceID:               data.ResourceID,
			Meter:                    meter,
			Component:                component,
			Usage: UsageInterval{
				Semantics: semantics,
				From:      data.Usage.From,
				To:        data.Usage.To,
				Quantity:  data.Usage.Quantity,
				Unit:      data.Usage.Unit,
				Precision: data.Usage.Precision,
			},
			BillingDimensions: dims,
		},
	}, nil
}

func validateIntervalUsage(u schema.Usage) error {
	if u.Semantics != schema.UsageSemanticsInterval {
		return fmt.Errorf("heartbeat usage.semantics must be %q, got %q", schema.UsageSemanticsInterval, u.Semantics)
	}
	if u.From == "" || u.To == "" {
		return errors.New("heartbeat usage missing from or to timestamp")
	}
	fromTime, err := time.Parse(time.RFC3339Nano, u.From)
	if err != nil {
		return fmt.Errorf("invalid usage.from %q: %w", u.From, err)
	}
	toTime, err := time.Parse(time.RFC3339Nano, u.To)
	if err != nil {
		return fmt.Errorf("invalid usage.to %q: %w", u.To, err)
	}
	if toTime.Before(fromTime) {
		return fmt.Errorf("usage.to %q precedes usage.from %q", u.To, u.From)
	}
	if err := validateUsageQuantityAndUnit(u); err != nil {
		return err
	}
	if u.Precision == "" {
		return errors.New("heartbeat usage missing precision")
	}
	return nil
}

func validateUsageQuantityAndUnit(u schema.Usage) error {
	if u.Quantity == "" {
		return errors.New("usage missing quantity")
	}
	if _, ok := new(big.Rat).SetString(u.Quantity); !ok {
		return fmt.Errorf("invalid decimal usage.quantity %q", u.Quantity)
	}
	if u.Unit == "" {
		return errors.New("usage missing unit")
	}
	return nil
}

func validateMatchingExtensions(ce cloudevents.Event, resourceID, resourceType, tenantID string) error {
	if ext := extensionString(ce, schema.ExtResourceID); ext != "" && ext != resourceID {
		return fmt.Errorf("extension %s %q does not match data.resource_id %q", schema.ExtResourceID, ext, resourceID)
	}
	if ext := extensionString(ce, schema.ExtResourceType); ext != "" && ext != resourceType {
		return fmt.Errorf("extension %s %q does not match data.resource_type %q", schema.ExtResourceType, ext, resourceType)
	}
	if ext := extensionString(ce, schema.ExtTenant); ext != "" && ext != tenantID {
		return fmt.Errorf("extension %s %q does not match data.tenant_id %q", schema.ExtTenant, ext, tenantID)
	}
	return nil
}

func extensionString(ce cloudevents.Event, key string) string {
	val, ok := ce.Extensions()[key]
	if !ok {
		return ""
	}
	s, _ := val.(string)
	return s
}

func copyStringDimensions(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeAnyDimensions(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, raw := range in {
		if k == "" || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if v != "" {
				out[k] = v
			}
		case bool:
			out[k] = strconv.FormatBool(v)
		case float64:
			if v != 0 {
				out[k] = strconv.FormatFloat(v, 'f', -1, 64)
			}
		default:
			if s := fmt.Sprint(v); s != "" {
				out[k] = s
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
