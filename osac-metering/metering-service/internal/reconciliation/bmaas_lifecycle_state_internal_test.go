/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
)

func TestBmaasIntervalsUseNormalizedMeterState(t *testing.T) {
	allocationSince := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	legacySince := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

	state := projection.ResourceState{
		ResourceType:  events.ResourceTypeBareMetalInstance,
		BillableSince: &legacySince,
		ComponentBillableSince: map[string]time.Time{
			events.BMaaSMeterConsumption: legacySince,
		},
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
	}

	got := bmaasIntervals(state)
	if got.AllocationSince == nil || !got.AllocationSince.Equal(allocationSince) {
		t.Fatalf("allocation interval = %v, want %v", got.AllocationSince, allocationSince)
	}
	if got.ConsumptionSince == nil || !got.ConsumptionSince.Equal(consumptionSince) {
		t.Fatalf("consumption interval = %v, want %v", got.ConsumptionSince, consumptionSince)
	}
}

func TestReconciledBmaasStateDoesNotRecreateLegacyComponentState(t *testing.T) {
	allocationSince := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	legacySince := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	transitionTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	existing := projection.ResourceState{
		CurrentState:  "RUNNING",
		BillableSince: &legacySince,
		ComponentBillableSince: map[string]time.Time{
			events.BMaaSMeterConsumption: legacySince,
		},
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
	}
	fs := fulfillmentResource{
		state:             "RUNNING",
		billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
	}

	got := reconciledBMaaSState(
		"bmi-1", existing, fs,
		events.BMaaSMeterIntervals{AllocationSince: &allocationSince, ConsumptionSince: &consumptionSince},
		events.BMaaSEffectSkip, events.BMaaSEffectSkip, transitionTime,
	)
	if got.ComponentBillableSince != nil {
		t.Fatalf("component lifecycle state = %#v, want nil for BMaaS", got.ComponentBillableSince)
	}
	if got.BMaaSMeterState.Allocation.ActiveSince == nil || !got.BMaaSMeterState.Allocation.ActiveSince.Equal(allocationSince) {
		t.Fatalf("allocation active interval = %v, want %v", got.BMaaSMeterState.Allocation.ActiveSince, allocationSince)
	}
	if got.BMaaSMeterState.Consumption.ActiveSince == nil || !got.BMaaSMeterState.Consumption.ActiveSince.Equal(consumptionSince) {
		t.Fatalf("consumption active interval = %v, want %v", got.BMaaSMeterState.Consumption.ActiveSince, consumptionSince)
	}
}

func TestStaleReferencePointUsesNormalizedBmaasMeterState(t *testing.T) {
	allocationSince := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	legacySince := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	state := projection.ResourceState{
		ResourceType:  events.ResourceTypeBareMetalInstance,
		BillableSince: &legacySince,
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
	}

	if got := staleReferencePoint(state, now); !got.Equal(allocationSince) {
		t.Fatalf("stale reference point = %v, want allocation interval %v", got, allocationSince)
	}

	state.BMaaSMeterState.Allocation.ActiveSince = nil
	if got := staleReferencePoint(state, now); !got.Equal(consumptionSince) {
		t.Fatalf("stale reference point without allocation = %v, want consumption interval %v", got, consumptionSince)
	}
}
