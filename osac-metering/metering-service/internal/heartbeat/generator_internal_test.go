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
	state := &projection.ResourceState{
		ResourceID:        "vm-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	windowStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
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
	state := &projection.ResourceState{
		ResourceID:        "vm-1",
		ResourceType:      events.ResourceTypeComputeInstance,
		BillingDimensions: map[string]any{"instance_type": "m5.large"},
	}

	windowStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
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
	state := &projection.ResourceState{
		ResourceID:   "nat-1",
		ResourceType: events.ResourceTypeNATGateway,
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
		if _, ok := data["duration_seconds"]; ok {
			t.Errorf("heartbeat %d unexpectedly includes duration_seconds: %v", i, data["duration_seconds"])
		}
		if dimensions["bm_instance_type"] != "gpu-large" {
			t.Errorf("heartbeat %d lost base billing dimensions: %#v", i, dimensions)
		}
	}
}

func TestBuildHeartbeatEventsBMaaSOmitsDurationAfterPreviousHeartbeat(t *testing.T) {
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

	checkDurationsOmitted := func(at time.Time) {
		t.Helper()
		got, err := g.buildHeartbeatEvents(state, at)
		if err != nil {
			t.Fatalf("build heartbeat at %s: %v", at, err)
		}
		if len(got) != 2 {
			t.Fatalf("heartbeat count = %d, want 2", len(got))
		}
		for i := range got {
			var data map[string]any
			if err := json.Unmarshal(got[i].Data(), &data); err != nil {
				t.Fatalf("heartbeat %d data: %v", i, err)
			}
			if _, ok := data["duration_seconds"]; ok {
				t.Errorf("heartbeat %d unexpectedly includes duration_seconds: %v", i, data["duration_seconds"])
			}
		}
	}

	checkDurationsOmitted(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	lastHeartbeat = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	checkDurationsOmitted(time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC))

	lastHeartbeat = time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	checkDurationsOmitted(time.Date(2026, 1, 1, 12, 4, 0, 0, time.UTC))
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
