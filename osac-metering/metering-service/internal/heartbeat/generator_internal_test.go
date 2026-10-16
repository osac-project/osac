/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package heartbeat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
)

type tickStore struct {
	billable   []projection.ResourceState
	updatedIDs []string
}

func (s *tickStore) Get(context.Context, string) (*projection.ResourceState, error) {
	return nil, nil
}

func (s *tickStore) Upsert(context.Context, projection.ResourceState) error { return nil }

func (s *tickStore) DeleteIfVersion(context.Context, string, int32) (bool, error) {
	return true, nil
}

func (s *tickStore) ListBillable(context.Context) ([]projection.ResourceState, error) {
	return s.billable, nil
}

func (s *tickStore) ListAll(context.Context) ([]projection.ResourceState, error) {
	return nil, nil
}

func (s *tickStore) UpdateLastHeartbeat(_ context.Context, resourceIDs []string, _ time.Time) error {
	s.updatedIDs = append([]string(nil), resourceIDs...)
	return nil
}

type tickPublisher struct {
	published []cloudevents.Event
}

func (p *tickPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.published = append(p.published, event)
	return nil
}

func bmaasTickState(id, currentState string, activeSince time.Time) projection.ResourceState {
	return projection.ResourceState{
		ResourceID:   id,
		ResourceType: events.ResourceTypeBareMetalInstance,
		CurrentState: currentState,
		BillingDimensions: map[string]any{
			"bm_instance_type": "gpu-large",
		},
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation: projection.MeterState{ActiveSince: &activeSince},
		},
	}
}

func TestBuildHeartbeatEventsStableIDWithinSameWindow(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	windowStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	activeSince := windowStart.Add(-time.Hour)
	state := &projection.ResourceState{
		ResourceID:        "vm-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillableSince:     &activeSince,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	first, err := g.buildHeartbeatEvents(state, windowStart.Add(5*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := g.buildHeartbeatEvents(state, windowStart.Add(45*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first[0].ID() != second[0].ID() {
		t.Errorf("two builds within the same %s heartbeat window should share a CloudEvent ID, got %q and %q", g.interval, first[0].ID(), second[0].ID())
	}
}

func TestBuildHeartbeatEventsNewIDInNextWindow(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	windowStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	activeSince := windowStart.Add(-time.Hour)
	state := &projection.ResourceState{
		ResourceID:        "vm-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillableSince:     &activeSince,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	first, err := g.buildHeartbeatEvents(state, windowStart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := g.buildHeartbeatEvents(state, windowStart.Add(g.interval))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first[0].ID() == second[0].ID() {
		t.Errorf("builds in different heartbeat windows must not share a CloudEvent ID, both were %q", first[0].ID())
	}
}

func TestBuildNetworkingHeartbeatEvents(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	activeSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	state := &projection.ResourceState{
		ResourceID:    "nat-1",
		ResourceType:  events.ResourceTypeNATGateway,
		BillableSince: &activeSince,
		BillingDimensions: map[string]any{
			"deployment":      "installation-1",
			"virtual_network": "vnet-1",
			"external_ip":     "ip-1",
			"tenant_id":       "tenant-1",
			"project_id":      "project-1",
		},
	}

	heartbeat, err := g.buildHeartbeatEvents(state, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(heartbeat) != 1 {
		t.Fatalf("expected one NATGateway heartbeat, got %d", len(heartbeat))
	}
	if got := heartbeat[0].Extensions()["osacresourcetype"]; got != events.ResourceTypeNATGateway {
		t.Errorf("expected NATGateway resource type, got %v", got)
	}
}

func TestBuildHeartbeatEventsUseDeltasForSingleResourceFamilies(t *testing.T) {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lastHeartbeat := start.Add(45 * time.Minute)
	now := start.Add(time.Hour)
	for _, test := range []struct {
		name         string
		resourceType string
		dimensions   map[string]any
	}{
		{name: "VMaaS", resourceType: events.ResourceTypeComputeInstance, dimensions: map[string]any{"instance_type": "m6.large"}},
		{name: "ExternalIP", resourceType: events.ResourceTypeExternalIP, dimensions: map[string]any{"external_ip": "192.0.2.1"}},
		{name: "NATGateway", resourceType: events.ResourceTypeNATGateway, dimensions: map[string]any{"virtual_network": "vnet-1"}},
		{name: "storage", resourceType: events.ResourceTypeVolume, dimensions: map[string]any{"volume_id": "vol-1", "size_gib": int64(10)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &projection.ResourceState{
				ResourceID:        "resource-1",
				ResourceType:      test.resourceType,
				TenantID:          "tenant-1",
				CurrentState:      "ACTIVE",
				BillableSince:     &start,
				LastHeartbeatAt:   &lastHeartbeat,
				BillingDimensions: test.dimensions,
			}
			got, err := BuildHeartbeatEvents(state, "hb/resource-1", now, "test")
			if err != nil {
				t.Fatalf("BuildHeartbeatEvents() error = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("heartbeat count = %d, want 1", len(got))
			}
			var data map[string]any
			if err := json.Unmarshal(got[0].Data(), &data); err != nil {
				t.Fatalf("unmarshal heartbeat data: %v", err)
			}
			if got := data["duration_seconds"]; got != float64(900) {
				t.Errorf("duration_seconds = %v, want 900", got)
			}
		})
	}

	state := &projection.ResourceState{
		ResourceID:        "resource-first-heartbeat",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillableSince:     &start,
		BillingDimensions: map[string]any{"instance_type": "m6.large"},
	}
	got, err := BuildHeartbeatEvents(state, "hb/first", now, "test")
	if err != nil {
		t.Fatalf("BuildHeartbeatEvents() first heartbeat error = %v", err)
	}
	var firstData map[string]any
	if err := json.Unmarshal(got[0].Data(), &firstData); err != nil {
		t.Fatalf("unmarshal first heartbeat data: %v", err)
	}
	if got := firstData["duration_seconds"]; got != float64(3600) {
		t.Errorf("first heartbeat duration_seconds = %v, want 3600 from active start", got)
	}
}

func TestBuildClusterHeartbeatUsesComponentStartAndHeartbeatBounds(t *testing.T) {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lastHeartbeat := start.Add(30 * time.Minute)
	componentSince := start.Add(50 * time.Minute)
	state := &projection.ResourceState{
		ResourceID:      "cluster-1",
		ResourceType:    events.ResourceTypeClusterOrder,
		CurrentState:    "READY",
		BillableSince:   &start,
		LastHeartbeatAt: &lastHeartbeat,
		ComponentBillableSince: map[string]time.Time{
			"cpu-workers": start,
			"gpu-workers": componentSince,
		},
		BillingDimensions: map[string]any{
			"components": []any{
				map[string]any{"node_set": "cpu-workers", "component": "worker", "host_type": "cpu", "node_count": 2},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu", "node_count": 1},
			},
		},
	}
	got, err := BuildHeartbeatEvents(state, "hb/cluster-1", start.Add(time.Hour), "test")
	if err != nil {
		t.Fatalf("BuildHeartbeatEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("heartbeat count = %d, want 2", len(got))
	}
	for _, event := range got {
		var data map[string]any
		if err := json.Unmarshal(event.Data(), &data); err != nil {
			t.Fatalf("unmarshal heartbeat data: %v", err)
		}
		dimensions := data["billing_dimensions"].(map[string]any)
		want := float64(1800)
		if dimensions["node_set"] == "gpu-workers" {
			want = 600
		}
		if got := data["duration_seconds"]; got != want {
			t.Errorf("%s duration_seconds = %v, want %v", dimensions["node_set"], got, want)
		}
	}
}

func TestBuildClusterHeartbeatRequiresComponentStart(t *testing.T) {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lastHeartbeat := start.Add(30 * time.Minute)
	state := &projection.ResourceState{
		ResourceID:      "cluster-missing-start",
		ResourceType:    events.ResourceTypeClusterOrder,
		CurrentState:    "READY",
		BillableSince:   &start,
		LastHeartbeatAt: &lastHeartbeat,
		BillingDimensions: map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": 1},
			},
		},
	}

	got, err := BuildHeartbeatEvents(state, "hb/missing-start", start.Add(time.Hour), "test")
	if err == nil {
		t.Fatalf("BuildHeartbeatEvents() events = %d, want missing component start error", len(got))
	}
	const want = `cluster cluster-missing-start node_set "_control_plane" has no component billable-since timestamp`
	if err.Error() != want {
		t.Fatalf("BuildHeartbeatEvents() error = %q, want %q", err, want)
	}
}

func TestFirstClusterHeartbeatUsesComponentStartWithoutLastHeartbeat(t *testing.T) {
	clusterStart := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	componentStart := clusterStart.Add(20 * time.Minute)
	now := clusterStart.Add(time.Hour)
	state := &projection.ResourceState{
		ResourceID:    "cluster-first-heartbeat",
		ResourceType:  events.ResourceTypeClusterOrder,
		CurrentState:  "READY",
		BillableSince: &clusterStart,
		ComponentBillableSince: map[string]time.Time{
			"_control_plane": componentStart,
		},
		BillingDimensions: map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": 1},
			},
		},
	}

	got, err := BuildHeartbeatEvents(state, "hb/first-cluster", now, "test")
	if err != nil {
		t.Fatalf("BuildHeartbeatEvents() error = %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(got[0].Data(), &data); err != nil {
		t.Fatalf("unmarshal heartbeat data: %v", err)
	}
	if got, want := data["duration_seconds"], float64(now.Sub(componentStart).Seconds()); got != want {
		t.Errorf("first cluster heartbeat duration_seconds = %v, want %v from component start", got, want)
	}
}

func TestBuildHeartbeatEventsRequiresBillableStart(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	lastHeartbeat := now.Add(-time.Minute)
	state := &projection.ResourceState{
		ResourceID:        "missing-heartbeat-start",
		ResourceType:      events.ResourceTypeComputeInstance,
		LastHeartbeatAt:   &lastHeartbeat,
		BillingDimensions: map[string]any{"instance_type": "m6.large"},
	}

	got, err := BuildHeartbeatEvents(state, "hb/missing-start", now, "test")
	if err == nil {
		t.Fatalf("BuildHeartbeatEvents() events = %d, want missing active start error", len(got))
	}
	if !strings.Contains(err.Error(), "active start timestamp is required") {
		t.Fatalf("BuildHeartbeatEvents() error = %q, want active start error", err)
	}
}

func TestBuildHeartbeatEventsBMaaSUsesIndependentMeters(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	state := &projection.ResourceState{
		ResourceID:   "bmi-1",
		ResourceType: events.ResourceTypeBareMetalInstance,
		CurrentState: "RUNNING",
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	first, err := g.buildHeartbeatEvents(state, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := g.buildHeartbeatEvents(state, now.Add(45*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	next, err := g.buildHeartbeatEvents(state, now.Add(g.interval))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(first) != 2 {
		t.Fatalf("expected allocation and consumption heartbeats, got %d", len(first))
	}
	if first[0].ID() != second[0].ID() || first[1].ID() != second[1].ID() {
		t.Fatalf("expected stable per-meter IDs within a heartbeat window, got %q/%q and %q/%q",
			first[0].ID(), first[1].ID(), second[0].ID(), second[1].ID())
	}
	if first[0].ID() == next[0].ID() || first[1].ID() == next[1].ID() {
		t.Fatalf("expected new per-meter IDs in the next heartbeat window")
	}

	expectations := []string{
		events.BMaaSMeterAllocation,
		events.BMaaSMeterConsumption,
	}
	for i, expectation := range expectations {
		var data map[string]any
		if err := json.Unmarshal(first[i].Data(), &data); err != nil {
			t.Fatalf("heartbeat %d data: %v", i, err)
		}
		dimensions, ok := data["billing_dimensions"].(map[string]any)
		if !ok || dimensions["meter_type"] != expectation {
			t.Errorf("heartbeat %d meter_type = %v, want %q", i, dimensions["meter_type"], expectation)
		}
		want := float64(3600)
		if expectation == events.BMaaSMeterConsumption {
			want = 1800
		}
		if got := data["duration_seconds"]; got != want {
			t.Errorf("heartbeat %d duration_seconds = %v, want %v", i, got, want)
		}
		if dimensions["bm_instance_type"] != "gpu-large" {
			t.Errorf("heartbeat %d lost base billing dimensions: %#v", i, dimensions)
		}
	}
}

func TestBuildHeartbeatEventsBMaaSUsesDeltaAfterPreviousHeartbeat(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 55, 0, 0, time.UTC)
	lastHeartbeat := time.Date(2026, 1, 1, 11, 45, 0, 0, time.UTC)
	state := &projection.ResourceState{
		ResourceID:      "bmi-delta-1",
		ResourceType:    events.ResourceTypeBareMetalInstance,
		CurrentState:    "RUNNING",
		LastHeartbeatAt: &lastHeartbeat,
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
		BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
	}

	checkDurations := func(at time.Time, allocationWant, consumptionWant float64) {
		t.Helper()
		got, err := g.buildHeartbeatEvents(state, at)
		if err != nil {
			t.Fatalf("build heartbeat at %s: %v", at, err)
		}
		if len(got) != 2 {
			t.Fatalf("heartbeat count = %d, want 2", len(got))
		}
		for i, want := range []float64{allocationWant, consumptionWant} {
			var data map[string]any
			if err := json.Unmarshal(got[i].Data(), &data); err != nil {
				t.Fatalf("heartbeat %d data: %v", i, err)
			}
			if got := data["duration_seconds"]; got != want {
				t.Errorf("heartbeat %d duration_seconds = %v, want %v", i, got, want)
			}
		}
	}

	checkDurations(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), 900, 300)

	lastHeartbeat = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	checkDurations(time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC), 60, 60)

	lastHeartbeat = time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	checkDurations(time.Date(2026, 1, 1, 12, 4, 0, 0, time.UTC), 180, 180)
}

func TestBuildHeartbeatEventsBMaaSStateCardinality(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	for _, test := range []struct {
		state string
		want  int
	}{
		{state: "RUNNING", want: 2},
		{state: "STOPPED", want: 1},
		{state: "STARTING", want: 1},
		{state: "STOPPING", want: 1},
		{state: "DELETING", want: 1},
		{state: "FAILED", want: 0},
		{state: "PROVISIONING", want: 0},
		{state: "UNSPECIFIED", want: 0},
	} {
		t.Run(test.state, func(t *testing.T) {
			var allocationSince *time.Time
			if events.IsAllocationBillableState(test.state) {
				since := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
				allocationSince = &since
			}
			meterState := projection.BMaaSMeterState{}
			if allocationSince != nil {
				meterState.Allocation.ActiveSince = allocationSince
			}
			if events.IsConsumptionBillableState(test.state) {
				since := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
				meterState.Consumption.ActiveSince = &since
			}
			state := &projection.ResourceState{
				ResourceID:        "bmi-1",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				CurrentState:      test.state,
				BillableSince:     allocationSince,
				BMaaSMeterState:   meterState,
				BillingDimensions: map[string]any{"bm_instance_type": "gpu-large"},
			}
			got, err := g.buildHeartbeatEvents(state, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != test.want {
				t.Fatalf("got %d heartbeat events, want %d", len(got), test.want)
			}
		})
	}
}

func TestBuildHeartbeatEventsBMaaSSkipsMissingIntervals(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name             string
		state            string
		allocationSince  *time.Time
		consumptionSince *time.Time
		want             int
		wantMeterTypes   []string
	}{
		{name: "running without intervals", state: "RUNNING", want: 0},
		{name: "running with allocation only", state: "RUNNING", allocationSince: &now, want: 1, wantMeterTypes: []string{events.BMaaSMeterAllocation}},
		{name: "stopped without allocation", state: "STOPPED", want: 0},
		{name: "deleting without allocation", state: "DELETING", want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &projection.ResourceState{
				ResourceID:   "bmi-1",
				ResourceType: events.ResourceTypeBareMetalInstance,
				CurrentState: test.state,
				BillingDimensions: map[string]any{
					"bm_instance_type": "gpu-large",
				},
				BillableSince: test.allocationSince,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{ActiveSince: test.allocationSince},
				},
			}
			if test.consumptionSince != nil {
				state.BMaaSMeterState.Consumption.ActiveSince = test.consumptionSince
			}

			got, err := g.buildHeartbeatEvents(state, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != test.want {
				t.Fatalf("got %d heartbeat events, want %d", len(got), test.want)
			}
			for i, wantMeterType := range test.wantMeterTypes {
				var data heartbeatData
				if err := json.Unmarshal(got[i].Data(), &data); err != nil {
					t.Fatalf("heartbeat %d data: %v", i, err)
				}
				if data.BillingDimensions["meter_type"] != wantMeterType {
					t.Errorf("heartbeat %d meter_type = %v, want %q", i, data.BillingDimensions["meter_type"], wantMeterType)
				}
			}
		})
	}
}

func TestTickDoesNotCheckpointBMaaSWhenNoHeartbeatEventsAreBuilt(t *testing.T) {
	activeSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	store := &tickStore{billable: []projection.ResourceState{
		bmaasTickState("bmi-failed", "BARE_METAL_INSTANCE_STATE_FAILED", activeSince),
	}}
	publisher := &tickPublisher{}
	generator := &Generator{store: store, publisher: publisher, interval: time.Minute}

	if err := generator.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if len(publisher.published) != 0 {
		t.Fatalf("published %d heartbeat events, want 0", len(publisher.published))
	}
	if len(store.updatedIDs) != 0 {
		t.Fatalf("checkpointed resource IDs %v, want none", store.updatedIDs)
	}
}

func TestTickCheckpointsBMaaSAfterPublishingHeartbeat(t *testing.T) {
	activeSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	store := &tickStore{billable: []projection.ResourceState{
		bmaasTickState("bmi-stopped", "BARE_METAL_INSTANCE_STATE_STOPPED", activeSince),
	}}
	publisher := &tickPublisher{}
	generator := &Generator{store: store, publisher: publisher, interval: time.Minute}

	if err := generator.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published %d heartbeat events, want 1", len(publisher.published))
	}
	if len(store.updatedIDs) != 1 || store.updatedIDs[0] != "bmi-stopped" {
		t.Fatalf("checkpointed resource IDs %v, want [bmi-stopped]", store.updatedIDs)
	}
}

func TestVolumeHeartbeatIDsIncludeBillingSliceIdentity(t *testing.T) {
	start := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	makeState := func(size int64) projection.ResourceState {
		return projection.ResourceState{
			ResourceID:    "volume-1",
			ResourceType:  events.ResourceTypeVolume,
			TenantID:      "tenant-1",
			CurrentState:  events.VolumeStateAvailable,
			IsBillable:    true,
			BillableSince: &start,
			BillingDimensions: map[string]any{
				"volume_id": "volume-1", "tenant_id": "tenant-1", "project_id": "project-1",
				"storage_tier": "gold", "size_gib": size,
			},
		}
	}
	g := &Generator{interval: time.Minute}
	first, err := g.buildHeartbeatEvents(&[]projection.ResourceState{makeState(10)}[0], start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.buildHeartbeatEvents(&[]projection.ResourceState{makeState(20)}[0], start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ID() == second[0].ID() {
		t.Fatalf("heartbeat IDs collided across size slices: %q", first[0].ID())
	}
}

func TestVolumeHeartbeatCarriesCumulativeUsage(t *testing.T) {
	start := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	state := &projection.ResourceState{
		ResourceID: "volume-1", ResourceType: events.ResourceTypeVolume, TenantID: "tenant-1",
		CurrentState: events.VolumeStateAvailable, IsBillable: true, BillableSince: &start,
		BillingDimensions: map[string]any{
			"volume_id": "volume-1", "tenant_id": "tenant-1", "project_id": "project-1",
			"storage_tier": "gold", "size_gib": int64(10),
		},
	}
	g := &Generator{interval: time.Minute}
	items, err := g.buildHeartbeatEvents(state, start.Add(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(items[0].Data(), &data); err != nil {
		t.Fatal(err)
	}
	usage := data["usage"].(map[string]any)
	if usage["semantics"] != "cumulative" || usage["unit"] != "gibibyte_second" || usage["quantity"] != "600.000000" {
		t.Fatalf("heartbeat usage = %#v", usage)
	}
}

func TestTickBMaaSMuteSuppressesOnlyConsumption(t *testing.T) {
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	store := &tickStore{billable: []projection.ResourceState{{
		ResourceID:   "bmi-stopped",
		ResourceType: events.ResourceTypeBareMetalInstance,
		CurrentState: "RUNNING",
		BillingDimensions: map[string]any{
			"bm_instance_type": "gpu-large",
		},
		BMaaSMeterState: projection.BMaaSMeterState{
			Allocation:  projection.MeterState{ActiveSince: &allocationSince},
			Consumption: projection.MeterState{ActiveSince: &consumptionSince},
		},
	}}}
	publisher := &tickPublisher{}
	presence := NewBMaaSPresence()
	presence.Replace([]string{"bmi-stopped"})
	presence.SetMeterMutes(map[string]BMaaSMeterMute{
		"bmi-stopped": {Consumption: true},
	})
	generator := NewGenerator(store, publisher, logr.Discard(), time.Minute, presence)

	if err := generator.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published %d heartbeat events, want one allocation heartbeat", len(publisher.published))
	}
	var data heartbeatData
	if err := json.Unmarshal(publisher.published[0].Data(), &data); err != nil {
		t.Fatalf("heartbeat data: %v", err)
	}
	if got := data.BillingDimensions["meter_type"]; got != events.BMaaSMeterAllocation {
		t.Fatalf("meter_type = %v, want %q", got, events.BMaaSMeterAllocation)
	}
	if len(store.updatedIDs) != 1 || store.updatedIDs[0] != "bmi-stopped" {
		t.Fatalf("checkpointed resource IDs %v, want [bmi-stopped]", store.updatedIDs)
	}
}
