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
)

// This catches a regression to the generic correction fan-out, which cannot
// represent independent BMaaS allocation and consumption boundaries.
func TestBuildBMaaSCorrectionEventsClosesActiveMetersIndependently(t *testing.T) {
	allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	eventsOut, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", StateDrift, "RUNNING", "FAILED",
		map[string]any{"bm_instance_type": "bm.large"},
		events.BMaaSMeterIntervals{AllocationSince: &allocationSince, ConsumptionSince: &consumptionSince},
		events.BMaaSEffectSuspend, events.BMaaSEffectSuspend, true, true, transitionTime,
	)
	if err != nil {
		t.Fatalf("build BMaaS corrections: %v", err)
	}
	if len(eventsOut) != 2 {
		t.Fatalf("expected allocation and consumption corrections, got %d", len(eventsOut))
	}

	for i, want := range []struct {
		meter    string
		suffix   string
		duration float64
		from     time.Time
	}{
		{events.BMaaSMeterAllocation, "/allocation", 7200, allocationSince},
		{events.BMaaSMeterConsumption, "/consumption", 3600, consumptionSince},
	} {
		if got := eventsOut[i].ID(); len(got) < len(want.suffix) || got[len(got)-len(want.suffix):] != want.suffix {
			t.Errorf("event %d ID = %q, want %q suffix", i, got, want.suffix)
		}
		var data correctionData
		if err := eventsOut[i].DataAs(&data); err != nil {
			t.Fatalf("read correction %d: %v", i, err)
		}
		if data.BillingDimensions["meter_type"] != want.meter {
			t.Errorf("event %d meter_type = %v, want %q", i, data.BillingDimensions["meter_type"], want.meter)
		}
		if data.AffectedInterval == nil {
			t.Errorf("event %d missing affected interval", i)
			continue
		}
		if !data.AffectedInterval.From.Equal(want.from) || !data.AffectedInterval.To.Equal(transitionTime) || data.AffectedInterval.OverbilledSeconds != want.duration {
			t.Errorf("event %d interval = %#v, want %s-%s (%.0fs)", i, data.AffectedInterval, want.from, transitionTime, want.duration)
		}
	}

	if eventsOut[0].ID() == eventsOut[1].ID() {
		t.Error("allocation and consumption corrections must have distinct deterministic IDs")
	}
	retry, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", StateDrift, "RUNNING", "FAILED",
		map[string]any{"bm_instance_type": "bm.large"},
		events.BMaaSMeterIntervals{AllocationSince: &allocationSince, ConsumptionSince: &consumptionSince},
		events.BMaaSEffectSuspend, events.BMaaSEffectSuspend, true, true, transitionTime,
	)
	if err != nil {
		t.Fatalf("build retry BMaaS corrections: %v", err)
	}
	for i := range eventsOut {
		if eventsOut[i].ID() != retry[i].ID() {
			t.Errorf("retry correction %d ID changed: %q != %q", i, eventsOut[i].ID(), retry[i].ID())
		}
	}
}

// This catches a correction builder that mutates the caller's shared dimensions
// while adding meter_type, which would leak one meter into the other event.
func TestBuildBMaaSCorrectionEventsClonesMeterDimensions(t *testing.T) {
	transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dims := map[string]any{"bm_instance_type": "bm.large"}

	eventsOut, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", MissedCreation, "", "RUNNING", dims,
		events.BMaaSMeterIntervals{}, events.BMaaSEffectStart, events.BMaaSEffectStart, false, false, transitionTime,
	)
	if err != nil {
		t.Fatalf("build BMaaS corrections: %v", err)
	}
	if _, found := dims["meter_type"]; found {
		t.Error("building meter corrections mutated the source dimensions")
	}
	if len(eventsOut) != 2 {
		t.Fatalf("expected two meter corrections, got %d", len(eventsOut))
	}
	if eventsOut[0].ID() == eventsOut[1].ID() {
		t.Errorf("meter corrections reused ID %q", eventsOut[0].ID())
	}

	for i, event := range eventsOut {
		var data correctionData
		if err := event.DataAs(&data); err != nil {
			t.Fatalf("read missed-creation correction %d: %v", i, err)
		}
		if data.AffectedInterval != nil {
			t.Errorf("missed-creation correction %d unexpectedly carried affected interval: %#v", i, data.AffectedInterval)
		}
	}
}

func TestBuildBMaaSCorrectionEventsDistinctTransitionsGetDistinctIDs(t *testing.T) {
	allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	firstTransition := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	secondTransition := firstTransition.Add(2 * time.Hour)
	intervals := events.BMaaSMeterIntervals{AllocationSince: &allocationSince, ConsumptionSince: &consumptionSince}
	dims := map[string]any{"bm_instance_type": "bm.large"}

	first, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", StateDrift, "STOPPED", "RUNNING", dims,
		intervals, events.BMaaSEffectSkip, events.BMaaSEffectStart, true, true, firstTransition,
	)
	if err != nil {
		t.Fatalf("build first BMaaS correction: %v", err)
	}
	second, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", StateDrift, "STOPPED", "RUNNING", dims,
		intervals, events.BMaaSEffectSkip, events.BMaaSEffectStart, true, true, secondTransition,
	)
	if err != nil {
		t.Fatalf("build second BMaaS correction: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected one consumption correction per transition, got %d and %d", len(first), len(second))
	}
	if first[0].ID() == second[0].ID() {
		t.Errorf("distinct authoritative transitions reused correction ID %q", first[0].ID())
	}
}

func TestBuildBMaaSCorrectionEventsRejectsUnknownEffects(t *testing.T) {
	transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	_, err := buildBMaaSCorrectionEvents(
		"bmi-1", "tenant-1", "project-1", StateDrift, "STOPPED", "RUNNING",
		map[string]any{"bm_instance_type": "bm.large"},
		events.BMaaSMeterIntervals{},
		"start_typo", events.BMaaSEffectSkip, false, false, transitionTime,
	)
	if err == nil {
		t.Fatal("expected unknown BMaaS effect to be rejected")
	}
}
