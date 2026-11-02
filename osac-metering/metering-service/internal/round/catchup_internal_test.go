/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
)

var mergeBase = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

func mergeKey() database.LedgerKey {
	return database.LedgerKey{
		ResourceType: "ComputeInstance", ResourceID: "vm-7",
		Meter: "compute", Component: "compute", Dimensions: `{"tenant":"acme"}`,
	}
}

func caughtOver(start, end time.Time, quantity int64) *caughtHeartbeat {
	return &caughtHeartbeat{
		key: mergeKey(), start: start, end: end, quantityMicro: quantity,
	}
}

func renderPieces(pieces []database.LedgerPiece) []string {
	var lines []string
	for _, piece := range pieces {
		lines = append(lines, fmt.Sprintf(
			"%s..%s %d", piece.StartTime.Format("15:04:05"), piece.EndTime.Format("15:04:05"),
			piece.QuantityMicro,
		))
	}
	return lines
}

// TestMergeIntoLedgerChecksOverlap checks that a caught heartbeat merges into the stretches
// it overlaps: a correction sharing its start with an existing stretch becomes one stretch
// with the summed quantity instead of a colliding second row.
func TestMergeIntoLedgerChecksOverlap(t *testing.T) {
	key := mergeKey()
	existing := []database.LedgerPiece{{
		Key: key, StartTime: mergeBase, EndTime: mergeBase.Add(time.Minute),
		QuantityMicro: 60_000_000,
	}}

	// A correction over the same interval adds its quantity to the stretch.
	merged, err := mergeIntoLedger(existing, caughtOver(mergeBase, mergeBase.Add(time.Minute), 30_000_000))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10:00:00..10:01:00 90000000"}
	if fmt.Sprint(renderPieces(merged)) != fmt.Sprint(want) {
		t.Fatalf("same-interval correction must merge into one summed stretch:\ngot:\n%s\nwant:\n%s",
			renderPieces(merged), want)
	}

	// A correction crossing a stretch boundary splits and sums on both sides.
	merged, err = mergeIntoLedger(existing, caughtOver(
		mergeBase.Add(30*time.Second), mergeBase.Add(90*time.Second), 30_000_000,
	))
	if err != nil {
		t.Fatal(err)
	}
	want = []string{
		"10:00:00..10:00:30 30000000",
		"10:00:30..10:01:00 45000000",
		"10:01:00..10:01:30 15000000",
	}
	if fmt.Sprint(renderPieces(merged)) != fmt.Sprint(want) {
		t.Fatalf("crossing correction must split and sum:\ngot:\n%s\nwant:\n%s",
			renderPieces(merged), want)
	}

	// Adjacent stretches of the same summed rate rejoin into one piece.
	existing = []database.LedgerPiece{
		{Key: key, StartTime: mergeBase, EndTime: mergeBase.Add(time.Minute),
			QuantityMicro: 60_000_000},
	}
	merged, err = mergeIntoLedger(existing, caughtOver(
		mergeBase.Add(time.Minute), mergeBase.Add(2*time.Minute), 60_000_000,
	))
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"10:00:00..10:02:00 120000000"}
	if fmt.Sprint(renderPieces(merged)) != fmt.Sprint(want) {
		t.Fatalf("adjacent same-rate stretches must rejoin:\ngot:\n%s\nwant:\n%s",
			renderPieces(merged), want)
	}
}

// TestDecodeHeartbeatSelectsTheSchemaInEffectAtWriteTime replays a record with the schema in
// effect when it was written (TC-AC-09). A record naming an unpublished version, or one
// written before any published version took effect, stops the rebuild instead of being read
// with the wrong shape.
func TestDecodeHeartbeatSelectsTheSchemaInEffectAtWriteTime(t *testing.T) {
	key := mergeKey()
	written := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	event, err := heartbeat.New(
		"acme", key, written.Add(-time.Minute), written, 60_000_000, "second", written, 7, "0001",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}

	caught, err := decodeHeartbeat(payload)
	if err != nil {
		t.Fatalf("decoding a v1 record written after v1 took effect: %v", err)
	}
	if caught.quantityMicro != 60_000_000 {
		t.Fatalf("quantity is %d, want 60000000", caught.quantityMicro)
	}
	if !caught.roundTime.Equal(written) {
		t.Fatalf("round time is %s, want %s", caught.roundTime, written)
	}
	if caught.key != key {
		t.Fatalf("key is %+v, want %+v", caught.key, key)
	}

	unknown, err := heartbeat.New(
		"acme", key, written.Add(-time.Minute), written, 60_000_000, "second", written, 7, "0001",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	unknown.SetType("osac.resource.heartbeat.v99")
	payload, err = unknown.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(payload); err == nil ||
		!strings.Contains(err.Error(), "osac.resource.heartbeat.v99") {
		t.Fatalf("an unpublished version must stop the rebuild, got %v", err)
	}

	before := time.Date(1969, 12, 31, 23, 59, 0, 0, time.UTC)
	early, err := heartbeat.New(
		"acme", key, before.Add(-time.Minute), before, 60_000_000, "second", before, 7, "0001",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err = early.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(payload); err == nil ||
		!strings.Contains(err.Error(), "no schema version is in effect") {
		t.Fatalf("a record written before every version must be refused, got %v", err)
	}
}
