/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
)

func TestCorrectionDescription(t *testing.T) {
	tests := []struct {
		reason   CorrectionReason
		expected string
	}{
		{MissedCreation, "Resource found in fulfillment-service but missing from metering projection"},
		{StateDrift, "Resource state in fulfillment-service differs from metering projection"},
		{BillingDimensionsDrift, "Billing dimensions in fulfillment-service differ from metering projection"},
		{MissedDeletion, "Resource found in metering projection but missing from fulfillment-service"},
	}

	for _, tc := range tests {
		t.Run(string(tc.reason), func(t *testing.T) {
			desc, err := correctionDescription(tc.reason)
			if err != nil {
				t.Fatalf("unexpected error for reason %s: %v", tc.reason, err)
			}
			if desc != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, desc)
			}
		})
	}
}

func TestCorrectionDescriptionUnknownReason(t *testing.T) {
	_, err := correctionDescription("unknown_reason")
	if err == nil {
		t.Fatal("expected error for unknown correction reason, got nil")
	}
	expected := "unknown correction reason: unknown_reason"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestBuildSyntheticHeartbeatsStableIDAcrossRetryOfSameGap(t *testing.T) {
	lastHeartbeat := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ps := projection.ResourceState{
		ResourceID:        "res-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		LastHeartbeatAt:   &lastHeartbeat,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	firstAttempt := lastHeartbeat.Add(65 * time.Minute)
	secondAttempt := lastHeartbeat.Add(125 * time.Minute)

	first, err := buildSyntheticHeartbeats(ps, firstAttempt)
	if err != nil {
		t.Fatalf("first attempt: unexpected error: %v", err)
	}
	second, err := buildSyntheticHeartbeats(ps, secondAttempt)
	if err != nil {
		t.Fatalf("second attempt: unexpected error: %v", err)
	}

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 event per attempt, got %d and %d", len(first), len(second))
	}
	if first[0].ID() != second[0].ID() {
		t.Errorf("expected the same CloudEvent ID for two attempts at closing the same unresolved gap (LastHeartbeatAt unchanged), got %q and %q", first[0].ID(), second[0].ID())
	}
}

func TestBuildSyntheticHeartbeatsBMaaSUsesIndependentMeters(t *testing.T) {
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	ps := projection.ResourceState{
		ResourceID:   "bmi-1",
		ResourceType: events.ResourceTypeBareMetalInstance,
		CurrentState: "RUNNING",
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}

	got, err := buildSyntheticHeartbeats(ps, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected allocation and consumption heartbeats, got %d", len(got))
	}

	for i, meterType := range []string{events.BMaaSMeterAllocation, events.BMaaSMeterConsumption} {
		var data map[string]any
		if err := json.Unmarshal(got[i].Data(), &data); err != nil {
			t.Fatalf("heartbeat %d data: %v", i, err)
		}
		dims := data["billing_dimensions"].(map[string]any)
		if dims["meter_type"] != meterType {
			t.Errorf("heartbeat %d meter_type = %v, want %q", i, dims["meter_type"], meterType)
		}
	}
}

func TestBuildSyntheticHeartbeatsNewIDOnceGapResolves(t *testing.T) {
	// Same reconciliation run (same "now"); only LastHeartbeatAt differs.
	// Isolates the ID's dependency on LastHeartbeatAt from any dependency on now.
	now := time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC)
	firstGap := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	secondGap := time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)

	psBefore := projection.ResourceState{
		ResourceID:        "res-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		LastHeartbeatAt:   &firstGap,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}
	psAfter := psBefore
	psAfter.LastHeartbeatAt = &secondGap

	before, err := buildSyntheticHeartbeats(psBefore, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after, err := buildSyntheticHeartbeats(psAfter, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if before[0].ID() == after[0].ID() {
		t.Errorf("expected a different CloudEvent ID once LastHeartbeatAt advances to a new gap, both were %q", before[0].ID())
	}
}

func TestBuildCorrectionEventsDifferentDimensionsGetDifferentIDs(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	dimsA := map[string]any{"instance_type": "m5.large"}
	dimsB := map[string]any{"instance_type": "m5.xlarge"}

	a, err := buildCorrectionEvents("res-1", events.ResourceTypeComputeInstance, "tenant-1", "",
		BillingDimensionsDrift, "RUNNING", "RUNNING", dimsA, nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := buildCorrectionEvents("res-1", events.ResourceTypeComputeInstance, "tenant-1", "",
		BillingDimensionsDrift, "RUNNING", "RUNNING", dimsB, nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a[0].ID() == b[0].ID() {
		t.Errorf("two distinct billing_dimensions_drift corrections (different dimensions) for the same resource/state must not share a CloudEvent ID, both were %q — the second would be silently dropped by adapter-side ID dedup", a[0].ID())
	}
}

func TestBuildCorrectionEventsSameDimensionsGetSameID(t *testing.T) {
	now1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now2 := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC) // a later reconciliation cycle re-detecting the same unresolved drift
	dims := map[string]any{"instance_type": "m5.large"}

	a, err := buildCorrectionEvents("res-1", events.ResourceTypeComputeInstance, "tenant-1", "",
		BillingDimensionsDrift, "RUNNING", "RUNNING", dims, nil, now1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := buildCorrectionEvents("res-1", events.ResourceTypeComputeInstance, "tenant-1", "",
		BillingDimensionsDrift, "RUNNING", "RUNNING", dims, nil, now2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a[0].ID() != b[0].ID() {
		t.Errorf("repeat detection of the SAME unresolved drift across reconciliation cycles should dedup (design.md: duplicate corrections for the same state are acceptable/harmless), got %q and %q", a[0].ID(), b[0].ID())
	}
}

func TestBuildCorrectionEventsCanonicalizesAdjustmentOrder(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	firstOrder := map[string]any{
		"cluster_template": "ocp-ci-small",
		"release_image":    "4.17.0",
		"components": []any{
			map[string]any{
				"node_set":                "_control_plane",
				"component":               "control_plane",
				"baremetal_instance_type": "_control_plane",
				"node_count":              int32(1),
			},
			map[string]any{
				"node_set":                "gpu-workers",
				"component":               "worker",
				"baremetal_instance_type": "gpu-h100",
				"node_count":              int32(2),
			},
		},
	}
	secondOrder := map[string]any{
		"components": []any{
			map[string]any{
				"node_count":              int32(2),
				"baremetal_instance_type": "gpu-h100",
				"component":               "worker",
				"node_set":                "gpu-workers",
			},
			map[string]any{
				"node_count":              int32(1),
				"baremetal_instance_type": "_control_plane",
				"component":               "control_plane",
				"node_set":                "_control_plane",
			},
		},
		"release_image":    "4.17.0",
		"cluster_template": "ocp-ci-small",
	}

	first, err := buildCorrectionEvents("cluster-1", events.ResourceTypeClusterOrder, "tenant-1", "",
		BillingDimensionsDrift, "READY", "READY", firstOrder, nil, now)
	if err != nil {
		t.Fatalf("first correction: unexpected error: %v", err)
	}
	replay, err := buildCorrectionEvents("cluster-1", events.ResourceTypeClusterOrder, "tenant-1", "",
		BillingDimensionsDrift, "READY", "READY", secondOrder, nil, now)
	if err != nil {
		t.Fatalf("replayed correction: unexpected error: %v", err)
	}

	if len(first) != 2 || len(replay) != 2 {
		t.Fatalf("expected two adjustment events per correction, got %d and %d", len(first), len(replay))
	}
	for i := range first {
		if first[i].ID() != replay[i].ID() {
			t.Errorf("semantic correction adjustment %d changed provider identity across order-only replay: %q != %q", i, first[i].ID(), replay[i].ID())
		}
	}
}

func TestTransientCheckersCoversAllBillabilityCheckerKeys(t *testing.T) {
	for resourceType := range billabilityCheckers {
		if _, ok := transientCheckers[resourceType]; !ok {
			t.Errorf("billabilityCheckers has resource type %q but transientCheckers does not — "+
				"transient states for this type will silently pass through as CurrentState", resourceType)
		}
	}
}

func TestCorrectionResourceTypesIncludesNetworking(t *testing.T) {
	for _, resourceType := range []string{events.ResourceTypeExternalIP, events.ResourceTypeNATGateway, events.ResourceTypeVolume} {
		if _, ok := correctionResourceTypes[resourceType]; !ok {
			t.Errorf("corrections do not support networking resource type %q", resourceType)
		}
	}
}

func TestBuildSyntheticHeartbeatsFallsBackToBillableSinceWhenNeverHeartbeated(t *testing.T) {
	billableSince := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ps := projection.ResourceState{
		ResourceID:        "res-never-hb",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillableSince:     &billableSince,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	first, err := buildSyntheticHeartbeats(ps, billableSince.Add(65*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := buildSyntheticHeartbeats(ps, billableSince.Add(125*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first[0].ID() != second[0].ID() {
		t.Errorf("expected a stable ID keyed off BillableSince when LastHeartbeatAt is nil, got %q and %q", first[0].ID(), second[0].ID())
	}
}
