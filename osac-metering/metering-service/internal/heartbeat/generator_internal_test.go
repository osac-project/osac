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

func TestBuildHeartbeatEventsBMaaSUsesIndependentMeters(t *testing.T) {
	g := &Generator{interval: 60 * time.Second}
	allocationSince := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	state := &projection.ResourceState{
		ResourceID:    "bmi-1",
		ResourceType:  events.ResourceTypeBareMetalInstance,
		CurrentState:  "RUNNING",
		BillableSince: &allocationSince,
		ComponentBillableSince: map[string]time.Time{
			events.BMaaSMeterConsumption: consumptionSince,
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

	expectations := []struct {
		meterType string
		duration  float64
	}{
		{events.BMaaSMeterAllocation, 3600},
		{events.BMaaSMeterConsumption, 1800},
	}
	for i, expectation := range expectations {
		var data heartbeatData
		if err := json.Unmarshal(first[i].Data(), &data); err != nil {
			t.Fatalf("heartbeat %d data: %v", i, err)
		}
		if data.BillingDimensions["meter_type"] != expectation.meterType {
			t.Errorf("heartbeat %d meter_type = %v, want %q", i, data.BillingDimensions["meter_type"], expectation.meterType)
		}
		if data.DurationSeconds != expectation.duration {
			t.Errorf("heartbeat %d duration_seconds = %v, want %v", i, data.DurationSeconds, expectation.duration)
		}
		if data.BillingDimensions["bm_instance_type"] != "gpu-large" {
			t.Errorf("heartbeat %d lost base billing dimensions: %#v", i, data.BillingDimensions)
		}
	}
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
			state := &projection.ResourceState{
				ResourceID:        "bmi-1",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				CurrentState:      test.state,
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
