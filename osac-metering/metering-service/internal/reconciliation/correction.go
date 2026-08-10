/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/osac-project/osac-metering/internal/events"
)

type CorrectionReason string

const (
	MissedCreation         CorrectionReason = "missed_creation"
	StateDrift             CorrectionReason = "state_drift"
	BillingDimensionsDrift CorrectionReason = "billing_dimensions_drift"
	MissedDeletion         CorrectionReason = "missed_deletion"
)

// TODO: populate AffectedInterval once adapters consume it (deferred from Phase 2).
type AffectedInterval struct {
	From              time.Time `json:"from"`
	To                time.Time `json:"to"`
	OverbilledSeconds float64   `json:"overbilled_seconds"`
}

type correctionData struct {
	ResourceID              string            `json:"resource_id"`
	ResourceType            string            `json:"resource_type"`
	TenantID                string            `json:"tenant_id"`
	ProjectID               *string           `json:"project_id"`
	Reason                  CorrectionReason  `json:"reason"`
	Description             string            `json:"description"`
	CorrectedState          *string           `json:"corrected_state"`
	PreviousStateProjection *string           `json:"previous_state_in_projection"`
	ActualStateFromSource   *string           `json:"actual_state_from_source"`
	BillingDimensions       map[string]any    `json:"billing_dimensions,omitempty"`
	AffectedInterval        *AffectedInterval `json:"affected_interval,omitempty"`
	SchemaVersion           string            `json:"schema_version"`
}

var correctionDescriptions = map[CorrectionReason]string{
	MissedCreation:         "Resource found in fulfillment-service but missing from metering projection",
	StateDrift:             "Resource state in fulfillment-service differs from metering projection",
	BillingDimensionsDrift: "Billing dimensions in fulfillment-service differ from metering projection",
	MissedDeletion:         "Resource found in metering projection but missing from fulfillment-service",
}

func correctionDescription(reason CorrectionReason) (string, error) {
	desc, ok := correctionDescriptions[reason]
	if !ok {
		return "", fmt.Errorf("unknown correction reason: %s", reason)
	}
	return desc, nil
}

func buildCorrectionEvents(
	resourceID, resourceType, tenantID, projectID string,
	reason CorrectionReason,
	projectionState, sourceState string,
	billingDimensions map[string]any,
	interval *AffectedInterval,
	now time.Time,
) ([]cloudevents.Event, error) {
	baseID := fmt.Sprintf("correction/%s/%s/%s/%s/%s", resourceID, reason, projectionState, sourceState,
		correctionFingerprint(billingDimensions, interval))
	buildFn := func(dims map[string]any, eventID string) (cloudevents.Event, error) {
		ce, err := buildCorrectionEvent(resourceID, resourceType, tenantID, projectID,
			reason, projectionState, sourceState, dims, interval, now)
		if err != nil {
			return ce, err
		}
		ce.SetID(eventID)
		return ce, nil
	}

	return events.BuildResourceEvents(resourceType, billingDimensions, baseID, buildFn)
}

// correctionFingerprint discriminates corrections that share resourceID,
// reason, and states but describe different discrepancies (e.g. two separate
// billing_dimensions_drift detections for the same resource while it stays
// in the same state). Content-based rather than time-based: repeat detection
// of the SAME unresolved discrepancy across reconciliation cycles must keep
// producing the SAME ID so it dedups as the design intends ("duplicate
// corrections... are acceptable"), while a genuinely different discrepancy
// must not collide with a prior one and get silently dropped.
func correctionFingerprint(billingDimensions map[string]any, interval *AffectedInterval) string {
	enc, _ := json.Marshal(struct {
		Dims     map[string]any    `json:"dims"`
		Interval *AffectedInterval `json:"interval,omitempty"`
	}{billingDimensions, interval})
	h := fnv.New64a()
	_, _ = h.Write(enc)
	return fmt.Sprintf("%x", h.Sum64())
}

func buildCorrectionEvent(
	resourceID, resourceType, tenantID, projectID string,
	reason CorrectionReason,
	projectionState, sourceState string,
	billingDimensions map[string]any,
	interval *AffectedInterval,
	now time.Time,
) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(uuid.NewString())
	ce.SetSource("osac-metering/reconciler")
	ce.SetType(events.EventCorrection)
	ce.SetTime(now)
	events.SetOSACExtensions(&ce, resourceID, resourceType, tenantID, projectID)

	description, err := correctionDescription(reason)
	if err != nil {
		return ce, err
	}

	data := correctionData{
		ResourceID:              resourceID,
		ResourceType:            resourceType,
		TenantID:                tenantID,
		ProjectID:               events.NilIfEmpty(projectID),
		Reason:                  reason,
		Description:             description,
		CorrectedState:          events.NilIfEmpty(sourceState),
		PreviousStateProjection: events.NilIfEmpty(projectionState),
		ActualStateFromSource:   events.NilIfEmpty(sourceState),
		BillingDimensions:       billingDimensions,
		AffectedInterval:        interval,
		SchemaVersion:           "v1",
	}
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return ce, fmt.Errorf("setting correction CloudEvent data: %w", err)
	}

	return ce, nil
}
