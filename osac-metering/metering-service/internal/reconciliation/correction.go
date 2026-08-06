/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"fmt"
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

func correctionDescription(reason CorrectionReason) string {
	switch reason {
	case MissedCreation:
		return "Resource found in fulfillment-service but missing from metering projection"
	case StateDrift:
		return "Resource state in fulfillment-service differs from metering projection"
	case BillingDimensionsDrift:
		return "Billing dimensions in fulfillment-service differ from metering projection"
	case MissedDeletion:
		return "Resource found in metering projection but missing from fulfillment-service"
	default:
		return fmt.Sprintf("unknown correction reason: %s", reason)
	}
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
	ce.SetType("osac.resource.correction.v1")
	ce.SetTime(now)
	events.SetOSACExtensions(&ce, resourceID, resourceType, tenantID, projectID)

	data := correctionData{
		ResourceID:              resourceID,
		ResourceType:            resourceType,
		TenantID:                tenantID,
		ProjectID:               events.NilIfEmpty(projectID),
		Reason:                  reason,
		Description:             correctionDescription(reason),
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
