/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package heartbeat

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
)

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
