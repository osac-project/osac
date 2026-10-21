/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package heartbeat_test

import (
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
)

// TestParseMicroRejectsOutOfRangeQuantities checks that a quantity whose micro units do not
// fit in an int64 is rejected: catch-up decodes topic records with this function, and
// big.Int.Int64 is undefined when the numerator is out of range, so a wrapped value would
// apply garbage usage to the ledger.
func TestParseMicroRejectsOutOfRangeQuantities(t *testing.T) {
	for _, text := range []string{
		"9223372036854.775808",  // math.MaxInt64 micro units, plus one
		"-9223372036854.775809", // math.MinInt64 micro units, minus one
		"99999999999999999999",
	} {
		if quantity, err := heartbeat.ParseMicro(text); err == nil {
			t.Fatalf("ParseMicro(%q) = %d, want an error: the value overflows micro units",
				text, quantity)
		}
	}
}

// TestParseMicroAcceptsBoundaryQuantities checks the largest magnitudes that do fit.
func TestParseMicroAcceptsBoundaryQuantities(t *testing.T) {
	cases := map[string]int64{
		"9223372036854.775807":  9223372036854775807,
		"-9223372036854.775808": -9223372036854775808,
		"0.000001":              1,
	}
	for text, want := range cases {
		got, err := heartbeat.ParseMicro(text)
		if err != nil {
			t.Fatalf("ParseMicro(%q) failed: %v", text, err)
		}
		if got != want {
			t.Fatalf("ParseMicro(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestNewRejectsCorruptedDimensions checks that dimension text that is not JSON fails the
// heartbeat instead of billing an empty dimension set: the key's dimensions are the billing
// identity, and silently dropping them would re-key every piece.
func TestNewRejectsCorruptedDimensions(t *testing.T) {
	key := database.LedgerKey{
		ResourceType: "compute_instance", ResourceID: "vm-1",
		Meter: "compute", Component: "compute", Dimensions: `{"tenant":`,
	}
	from := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	_, err := heartbeat.New(
		"acme", key, from, from.Add(time.Minute), 60_000_000, "second",
		from.Add(2*time.Minute), 1, "0001",
	)
	if err == nil {
		t.Fatal("corrupted dimension JSON must fail the heartbeat")
	}
}

// TestIDIsStableAcrossQuantitiesAndUniqueAcrossRounds pins the once-semantics key's inputs.
// The ID hashes the identity, the interval, and the round time, and deliberately not the
// quantity: a heartbeat the relay resends within one round keeps its ID, so the provider
// drops the duplicate instead of billing the piece twice, while a corrected piece of a
// later round gets a new ID and bills as new. Quantity is not even a parameter, so a
// quantity change can never re-key a heartbeat; every other input must.
func TestIDIsStableAcrossQuantitiesAndUniqueAcrossRounds(t *testing.T) {
	key := database.LedgerKey{
		ResourceType: "compute_instance", ResourceID: "vm-1",
		Meter: "compute", Component: "compute", Dimensions: `{"instance_type":"small"}`,
	}
	from := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	roundTime := from.Add(2 * time.Minute)

	same := heartbeat.ID("acme", key, from, to, roundTime)
	if again := heartbeat.ID("acme", key, from, to, roundTime); again != same {
		t.Fatal("the same inputs must produce the same ID")
	}

	with := func(mod func(*database.LedgerKey)) database.LedgerKey {
		copied := key
		mod(&copied)
		return copied
	}
	changes := map[string]string{
		"the tenant":         heartbeat.ID("other", key, from, to, roundTime),
		"the resource type":  heartbeat.ID("acme", with(func(k *database.LedgerKey) { k.ResourceType = "volume" }), from, to, roundTime),
		"the resource id":    heartbeat.ID("acme", with(func(k *database.LedgerKey) { k.ResourceID = "vm-2" }), from, to, roundTime),
		"the meter":          heartbeat.ID("acme", with(func(k *database.LedgerKey) { k.Meter = "capacity" }), from, to, roundTime),
		"the component":      heartbeat.ID("acme", with(func(k *database.LedgerKey) { k.Component = "capacity" }), from, to, roundTime),
		"the dimensions":     heartbeat.ID("acme", with(func(k *database.LedgerKey) { k.Dimensions = `{"instance_type":"large"}` }), from, to, roundTime),
		"the interval start": heartbeat.ID("acme", key, from.Add(time.Second), to, roundTime),
		"the interval end":   heartbeat.ID("acme", key, from, to.Add(time.Second), roundTime),
		"the round time":     heartbeat.ID("acme", key, from, to, roundTime.Add(time.Minute)),
	}
	for name, id := range changes {
		if id == same {
			t.Fatalf("changing %s must change the ID", name)
		}
	}
}
