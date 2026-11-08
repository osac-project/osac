/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/schema"
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

// TestDecodeHeartbeatReplaysARecordWithItsNamedVersion replays a record with the version its
// CloudEvent names (TC-AC-09). An unknown version stops the rebuild, and so does a record
// written before its own version took effect.
func TestDecodeHeartbeatReplaysARecordWithItsNamedVersion(t *testing.T) {
	versions, err := schema.PublishedVersions()
	if err != nil {
		t.Fatalf("loading the published schema versions: %v", err)
	}
	key := mergeKey()
	written := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	event, err := heartbeat.New(
		"acme", key, written.Add(-time.Minute), written, 60_000_000, "second", written, 7, "0001", "",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}

	caught, err := decodeHeartbeat(payload, versions)
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

	// A record that omits a required field of its own version is corruption, not a record
	// the rebuild may bill.
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decoding the record body: %v", err)
	}
	delete(body["data"].(map[string]any), "rules_revision")
	broken, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the broken record: %v", err)
	}
	if _, err := decodeHeartbeat(broken, versions); err == nil ||
		!strings.Contains(err.Error(), `missing required field "rules_revision"`) {
		t.Fatalf("a record missing a required field must be refused, got %v", err)
	}

	unknown, err := heartbeat.New(
		"acme", key, written.Add(-time.Minute), written, 60_000_000, "second", written, 7, "0001", "",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	unknown.SetType("osac.resource.heartbeat.v99")
	payload, err = unknown.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(payload, versions); err == nil ||
		!strings.Contains(err.Error(), "osac.resource.heartbeat.v99") {
		t.Fatalf("an unpublished version must stop the rebuild, got %v", err)
	}

	// A record may not carry a version that was not published when it was written.
	futureV2 := versions[len(versions)-1]
	futureV2.Version = "v2"
	futureV2.CloudEventType = "osac.resource.heartbeat.v2"
	futureV2.EffectiveAt = written.Add(time.Hour)
	future, err := heartbeat.New(
		"acme", key, written.Add(-time.Minute), written, 60_000_000, "second", written, 7, "0001", "",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	future.SetType(futureV2.CloudEventType)
	payload, err = future.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(payload, []schema.Version{versions[0], futureV2}); err == nil ||
		!strings.Contains(err.Error(), "before that version took effect") {
		t.Fatalf("a record written before its version took effect must be refused, got %v", err)
	}
}

// TestDecodeHeartbeatRejectsRecordsTheSchemaRejects exercises the payload checks replay
// applies on top of the required-field check.
func TestDecodeHeartbeatRejectsRecordsTheSchemaRejects(t *testing.T) {
	versions, err := schema.PublishedVersions()
	if err != nil {
		t.Fatalf("loading the published schema versions: %v", err)
	}
	written := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	event, err := heartbeat.New(
		"acme", mergeKey(), written.Add(-time.Minute), written, 60_000_000, "second",
		written, 7, "0001", "",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	valid, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(valid, versions); err != nil {
		t.Fatalf("the valid record must pass: %v", err)
	}

	tests := []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"unknown resource type", "resource_type", "unknown", "unknown resource_type"},
		{"wrong semantics", "semantics", "cumulative", "usage.semantics"},
		{"wrong precision", "precision", "minute", "usage.precision"},
		{"short quantity", "quantity", "60.0", "six-decimal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal(valid, &body); err != nil {
				t.Fatalf("decoding the record body: %v", err)
			}
			data := body["data"].(map[string]any)
			switch test.field {
			case "semantics", "precision", "quantity":
				data["usage"].(map[string]any)[test.field] = test.value
			default:
				data[test.field] = test.value
			}
			broken, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("encoding the broken record: %v", err)
			}
			if _, err := decodeHeartbeat(broken, versions); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("want an error mentioning %q, got %v", test.want, err)
			}
		})
	}
}

// TestDecodeHeartbeatAcceptsAV1RecordInsideAV2Window pins the rolling-deploy case: once v2 is
// published, older pods keep writing v1 records, and a rebuild must still accept them.
func TestDecodeHeartbeatAcceptsAV1RecordInsideAV2Window(t *testing.T) {
	versions, err := schema.PublishedVersions()
	if err != nil {
		t.Fatalf("loading the published schema versions: %v", err)
	}
	written := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	v2 := versions[len(versions)-1]
	v2.Version = "v2"
	v2.CloudEventType = "osac.resource.heartbeat.v2"
	v2.EffectiveAt = written.Add(-time.Hour)
	withV2 := append(append([]schema.Version{}, versions...), v2)

	event, err := heartbeat.New(
		"acme", mergeKey(), written.Add(-time.Minute), written, 60_000_000, "second",
		written, 7, "0001", "",
	)
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if _, err := decodeHeartbeat(payload, withV2); err != nil {
		t.Fatalf("a v1 record written inside v2's window must be accepted: %v", err)
	}
}
