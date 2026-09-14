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

	"github.com/osac-project/osac-metering/internal/events"
)

func buildBMaaSCorrectionEvents(
	resourceID, tenantID, projectID string,
	reason CorrectionReason,
	projectionState, sourceState string,
	billingDimensions map[string]any,
	intervals events.BMaaSMeterIntervals,
	allocationEffect, consumptionEffect string,
	allocationEverStarted, consumptionEverStarted bool,
	transitionTime time.Time,
) ([]cloudevents.Event, error) {
	fingerprint, err := bmaasCorrectionFingerprint(billingDimensions, intervals)
	if err != nil {
		return nil, fmt.Errorf("building BMaaS correction identity: %w", err)
	}
	baseID := fmt.Sprintf("correction/%s/%s/%s/%s/%s", resourceID, reason, projectionState, sourceState, fingerprint)
	return events.DecomposeBMIEvents(
		billingDimensions,
		baseID,
		transitionTime,
		intervals,
		func(request events.BMaaSEventBuildRequest) (cloudevents.Event, error) {
			interval := bmaasAffectedInterval(request, intervals, transitionTime)
			ce, err := buildCorrectionEvent(
				resourceID,
				events.ResourceTypeBareMetalInstance,
				tenantID,
				projectID,
				reason,
				projectionState,
				sourceState,
				request.BillingDims,
				interval,
				transitionTime,
			)
			if err != nil {
				return ce, err
			}
			ce.SetID(request.EventID)
			return ce, nil
		},
		events.BMaaSEffectEventType(allocationEffect, allocationEverStarted),
		events.BMaaSEffectEventType(consumptionEffect, consumptionEverStarted),
	)
}

func bmaasAffectedInterval(request events.BMaaSEventBuildRequest, intervals events.BMaaSMeterIntervals, transitionTime time.Time) *AffectedInterval {
	var since *time.Time
	switch request.MeterType {
	case events.BMaaSMeterAllocation:
		since = intervals.AllocationSince
	case events.BMaaSMeterConsumption:
		since = intervals.ConsumptionSince
	}
	if request.EventType != events.EventSuspended || since == nil || request.DurationSeconds == nil {
		return nil
	}
	return &AffectedInterval{
		From:              *since,
		To:                transitionTime,
		OverbilledSeconds: *request.DurationSeconds,
	}
}

func bmaasCorrectionFingerprint(billingDimensions map[string]any, intervals events.BMaaSMeterIntervals) (string, error) {
	canonicalDimensions, err := canonicalCorrectionDimensions(events.ResourceTypeBareMetalInstance, billingDimensions)
	if err != nil {
		return "", err
	}
	enc, err := json.Marshal(struct {
		Dims      map[string]any `json:"dims"`
		Intervals struct {
			AllocationSince  *time.Time `json:"allocation_since,omitempty"`
			ConsumptionSince *time.Time `json:"consumption_since,omitempty"`
		} `json:"intervals"`
	}{
		Dims: canonicalDimensions,
		Intervals: struct {
			AllocationSince  *time.Time `json:"allocation_since,omitempty"`
			ConsumptionSince *time.Time `json:"consumption_since,omitempty"`
		}{intervals.AllocationSince, intervals.ConsumptionSince},
	})
	if err != nil {
		return "", fmt.Errorf("marshalling canonical correction content: %w", err)
	}
	h := fnv.New64a()
	if _, err := h.Write(enc); err != nil {
		return "", fmt.Errorf("hashing canonical correction content: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum64()), nil
}
