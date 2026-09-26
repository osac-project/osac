/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/projection"
)

type partialHeartbeatPublisher struct {
	failAfter int
	published []cloudevents.Event
}

func (p *partialHeartbeatPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.published = append(p.published, event)
	if len(p.published) > p.failAfter {
		return errors.New("publisher unavailable")
	}
	return nil
}

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

	for i, meterType := range []string{
		events.BMaaSMeterAllocation,
		events.BMaaSMeterConsumption,
	} {
		var data map[string]any
		if err := json.Unmarshal(got[i].Data(), &data); err != nil {
			t.Fatalf("heartbeat %d data: %v", i, err)
		}
		dims := data["billing_dimensions"].(map[string]any)
		if dims["meter_type"] != meterType {
			t.Errorf("heartbeat %d meter_type = %v, want %q", i, dims["meter_type"], meterType)
		}
		if _, ok := data["duration_seconds"]; ok {
			t.Errorf("heartbeat %d unexpectedly includes duration_seconds: %v", i, data["duration_seconds"])
		}
	}
}

func TestBuildSyntheticHeartbeatsBMaaSReusesMeterIDsForSameStaleGap(t *testing.T) {
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	lastHeartbeat := time.Date(2026, 1, 1, 11, 45, 0, 0, time.UTC)
	state := projection.ResourceState{
		ResourceID:      "bmi-1",
		ResourceType:    events.ResourceTypeBareMetalInstance,
		CurrentState:    "RUNNING",
		IsBillable:      true,
		BillableSince:   &allocationSince,
		LastHeartbeatAt: &lastHeartbeat,
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}

	first, err := buildSyntheticHeartbeats(state, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("first stale heartbeat: %v", err)
	}
	retry, err := buildSyntheticHeartbeats(state, time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("retry stale heartbeat: %v", err)
	}
	if len(first) != 2 || len(retry) != 2 {
		t.Fatalf("expected allocation and consumption events on each attempt, got %d and %d", len(first), len(retry))
	}
	for i, suffix := range []string{"/allocation", "/consumption"} {
		if first[i].ID() != retry[i].ID() {
			t.Errorf("event %d ID changed across retry of the same stale gap: %q != %q", i, first[i].ID(), retry[i].ID())
		}
		if len(first[i].ID()) < len(suffix) || first[i].ID()[len(first[i].ID())-len(suffix):] != suffix {
			t.Errorf("event %d ID = %q, want %q suffix", i, first[i].ID(), suffix)
		}
	}

	newGap := state
	newLastHeartbeat := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
	newGap.LastHeartbeatAt = &newLastHeartbeat
	next, err := buildSyntheticHeartbeats(newGap, time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("next stale heartbeat: %v", err)
	}
	for i := range first {
		if first[i].ID() == next[i].ID() {
			t.Errorf("event %d ID did not change after LastHeartbeatAt advanced: %q", i, first[i].ID())
		}
	}
}

func TestBuildSyntheticHeartbeatsBMaaSUsesConsumptionBoundaryWithoutAllocationCheckpoint(t *testing.T) {
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	state := projection.ResourceState{
		ResourceID:   "bmi-consumption-only",
		ResourceType: events.ResourceTypeBareMetalInstance,
		CurrentState: "RUNNING",
		IsBillable:   true,
		BMaaSMeterState: projection.BMaaSMeterState{
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}
	first, err := buildSyntheticHeartbeats(state, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("first stale heartbeat: %v", err)
	}
	retry, err := buildSyntheticHeartbeats(state, time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("retry stale heartbeat: %v", err)
	}
	if len(first) != 1 || len(retry) != 1 {
		t.Fatalf("expected one consumption heartbeat per attempt, got %d and %d", len(first), len(retry))
	}
	if first[0].ID() != retry[0].ID() {
		t.Errorf("same consumption-only stale gap changed IDs across retries: %q != %q", first[0].ID(), retry[0].ID())
	}
	if strings.HasSuffix(first[0].ID(), "/allocation") || !strings.HasSuffix(first[0].ID(), "/consumption") {
		t.Errorf("stale consumption-only heartbeat ID = %q, want only /consumption suffix", first[0].ID())
	}
	var data map[string]any
	if err := json.Unmarshal(first[0].Data(), &data); err != nil {
		t.Fatalf("heartbeat data: %v", err)
	}
	if _, ok := data["duration_seconds"]; ok {
		t.Errorf("consumption-only heartbeat unexpectedly includes duration_seconds: %v", data["duration_seconds"])
	}
}

func TestReconcileStaleBMaaSHeartbeatStateFanout(t *testing.T) {
	lastHeartbeat := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	allocationSince := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name                string
		state               string
		isBillable          bool
		hasAllocationSince  bool
		hasConsumptionSince bool
		wantEvents          int
		wantCheckpoint      bool
	}{
		{name: "running", state: "RUNNING", isBillable: true, hasAllocationSince: true, hasConsumptionSince: true, wantEvents: 2, wantCheckpoint: true},
		{name: "running without allocation boundary", state: "RUNNING", isBillable: true, hasConsumptionSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "running without consumption boundary", state: "RUNNING", isBillable: true, hasAllocationSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "stopped", state: "STOPPED", isBillable: true, hasAllocationSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "starting", state: "STARTING", isBillable: true, hasAllocationSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "stopping", state: "STOPPING", isBillable: true, hasAllocationSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "deleting", state: "DELETING", isBillable: true, hasAllocationSince: true, wantEvents: 1, wantCheckpoint: true},
		{name: "stopped without allocation boundary", state: "STOPPED", isBillable: true, wantEvents: 0},
		{name: "failed", state: "FAILED", isBillable: false, wantEvents: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stateAllocationSince *time.Time
			if test.hasAllocationSince {
				stateAllocationSince = &allocationSince
			}
			store := newMockStore()
			state := projection.ResourceState{
				ResourceID:        "bmi-1",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				TenantID:          "tenant-1",
				ProjectID:         "project-1",
				CurrentState:      test.state,
				IsBillable:        test.isBillable,
				BillableSince:     stateAllocationSince,
				LastHeartbeatAt:   &lastHeartbeat,
				BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
			}
			if test.hasAllocationSince {
				state.BMaaSMeterState.Allocation.ActiveSince = &allocationSince
			}
			if test.hasConsumptionSince {
				state.BMaaSMeterState.Consumption.ActiveSince = &consumptionSince
			}
			store.states["bmi-1"] = state
			publisher := &mockPublisher{}
			reconciler := newReconcilerForTest(nil, nil, &mockBareMetalInstancesClient{}, store, publisher, logr.Discard(), time.Minute)

			corrections, err := reconciler.reconcileStaleHeartbeats(context.Background(), map[string]fulfillmentResource{"bmi-1": {}}, now)
			if err != nil {
				t.Fatalf("reconcileStaleHeartbeats() error = %v", err)
			}
			wantCorrections := 0
			if test.wantCheckpoint {
				wantCorrections = 1
			}
			if corrections != wantCorrections {
				t.Errorf("reconcileStaleHeartbeats() corrections = %d, want %d", corrections, wantCorrections)
			}

			publisher.mu.Lock()
			defer publisher.mu.Unlock()
			if len(publisher.published) != test.wantEvents {
				t.Fatalf("published %d heartbeat events, want %d", len(publisher.published), test.wantEvents)
			}
			for i, event := range publisher.published {
				var data map[string]any
				if err := json.Unmarshal(event.Data(), &data); err != nil {
					t.Fatalf("event %d data: %v", i, err)
				}
				if data["tenant_id"] != "tenant-1" || data["project_id"] != "project-1" {
					t.Errorf("event %d lost tenant/project dimensions: %#v", i, data)
				}
			}
			wantCheckpoints := 0
			if test.wantCheckpoint {
				wantCheckpoints = 1
			}
			if len(store.lastHeartbeatUpdateIDs) != wantCheckpoints {
				t.Errorf("checkpoint batches = %d, want %d", len(store.lastHeartbeatUpdateIDs), wantCheckpoints)
			}
		})
	}
}

func TestReconcileStaleBMaaSHeartbeatDoesNotCheckpointPartialFanout(t *testing.T) {
	lastHeartbeat := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	allocationSince := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	store := newMockStore()
	store.states["bmi-1"] = projection.ResourceState{
		ResourceID:      "bmi-1",
		ResourceType:    events.ResourceTypeBareMetalInstance,
		CurrentState:    "RUNNING",
		IsBillable:      true,
		BillableSince:   &allocationSince,
		LastHeartbeatAt: &lastHeartbeat,
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}
	failingPublisher := &partialHeartbeatPublisher{failAfter: 1}
	reconciler := newReconcilerForTest(nil, nil, &mockBareMetalInstancesClient{}, store, failingPublisher, logr.Discard(), time.Minute)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	corrections, err := reconciler.reconcileStaleHeartbeats(context.Background(), map[string]fulfillmentResource{"bmi-1": {}}, now)
	if err != nil {
		t.Fatalf("first reconcileStaleHeartbeats() error = %v", err)
	}
	if corrections != 0 {
		t.Errorf("first reconcileStaleHeartbeats() corrections = %d, want 0 after partial publish", corrections)
	}
	if len(failingPublisher.published) != 2 {
		t.Fatalf("first attempt published %d event attempts, want 2", len(failingPublisher.published))
	}
	if len(store.lastHeartbeatUpdateIDs) != 0 {
		t.Fatalf("first attempt checkpointed %v after partial publish", store.lastHeartbeatUpdateIDs)
	}

	retryPublisher := &partialHeartbeatPublisher{failAfter: 2}
	reconciler.publisher = retryPublisher
	corrections, err = reconciler.reconcileStaleHeartbeats(context.Background(), map[string]fulfillmentResource{"bmi-1": {}}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("retry reconcileStaleHeartbeats() error = %v", err)
	}
	if corrections != 1 {
		t.Errorf("retry reconcileStaleHeartbeats() corrections = %d, want 1", corrections)
	}
	if len(retryPublisher.published) != 2 {
		t.Fatalf("retry published %d events, want 2", len(retryPublisher.published))
	}
	for i := range retryPublisher.published {
		if failingPublisher.published[i].ID() != retryPublisher.published[i].ID() {
			t.Errorf("event %d ID changed after retrying the same stale gap: %q != %q", i, failingPublisher.published[i].ID(), retryPublisher.published[i].ID())
		}
	}
	if len(store.lastHeartbeatUpdateIDs) != 1 {
		t.Errorf("retry checkpoint batches = %d, want 1", len(store.lastHeartbeatUpdateIDs))
	}
}

func TestReconcileStaleBMaaSHeartbeatMutesConsumptionForHeldHost(t *testing.T) {
	allocationSince := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	lastHeartbeat := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	store := newMockStore()
	store.states["bmi-stopped"] = projection.ResourceState{
		ResourceID:      "bmi-stopped",
		ResourceType:    events.ResourceTypeBareMetalInstance,
		CurrentState:    "RUNNING",
		IsBillable:      true,
		BillableSince:   &allocationSince,
		LastHeartbeatAt: &lastHeartbeat,
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}
	publisher := &mockPublisher{}
	presence := heartbeat.NewBMaaSPresence()
	presence.Replace([]string{"bmi-stopped"})
	presence.SetMeterMutes(map[string]heartbeat.BMaaSMeterMute{
		"bmi-stopped": {Consumption: true},
	})
	reconciler := newReconcilerForTest(nil, nil, &mockBareMetalInstancesClient{}, store, publisher, logr.Discard(), time.Minute, presence)
	reconciler.bmaasHolds = map[string]struct{}{"bmi-stopped": {}}

	corrections, err := reconciler.reconcileStaleHeartbeats(
		context.Background(), map[string]fulfillmentResource{"bmi-stopped": {}}, lastHeartbeat.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("reconcileStaleHeartbeats() error = %v", err)
	}
	if corrections != 1 {
		t.Fatalf("reconcileStaleHeartbeats() corrections = %d, want 1", corrections)
	}
	if len(publisher.published) != 1 || !strings.HasSuffix(publisher.published[0].ID(), "/allocation") {
		t.Fatalf("published events = %v, want one allocation heartbeat", publisher.published)
	}
	if store.states["bmi-stopped"].CurrentState != "RUNNING" {
		t.Fatalf("held projection state changed to %q", store.states["bmi-stopped"].CurrentState)
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
				"node_set":   "_control_plane",
				"component":  "control_plane",
				"host_type":  "_control_plane",
				"node_count": int32(1),
			},
			map[string]any{
				"node_set":   "gpu-workers",
				"component":  "worker",
				"host_type":  "gpu-h100",
				"node_count": int32(2),
			},
		},
	}
	secondOrder := map[string]any{
		"components": []any{
			map[string]any{
				"node_count": int32(2),
				"host_type":  "gpu-h100",
				"component":  "worker",
				"node_set":   "gpu-workers",
			},
			map[string]any{
				"node_count": int32(1),
				"host_type":  "_control_plane",
				"component":  "control_plane",
				"node_set":   "_control_plane",
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
