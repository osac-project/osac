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
