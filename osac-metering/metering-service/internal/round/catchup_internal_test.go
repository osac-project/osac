/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"fmt"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
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
