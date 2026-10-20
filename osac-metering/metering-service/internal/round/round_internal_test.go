/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/testenv"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// TestEventsFoldedClearsTargetOnImmediatePass checks that a round whose fold is already at
// the watermark it just captured drops the target: the next round must capture a fresh
// watermark, not compare the fold against a one-round-stale one.
func TestEventsFoldedClearsTargetOnImmediatePass(t *testing.T) {
	const tenant = "targetclear"
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})

	r, err := New(nil, nil, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}

	// The fold is already at the topic's end: the check passes and must not leave a
	// captured target behind.
	folded, err := r.eventsFolded(context.Background(), tenant, int64(total))
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if !folded {
		t.Fatal("a fold at the watermark must pass")
	}
	if _, waiting := r.eventTarget[tenant]; waiting {
		t.Fatal("a check that passed on its first capture must drop the target, " +
			"so the next round captures a fresh watermark")
	}

	// A new event arrives and the fold is one record behind: the next check captures the
	// fresh watermark and waits for the fold to reach it.
	total += testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(time.Minute), base, "web", "RUNNING", base, "large", base.Add(time.Minute)),
	})
	folded, err = r.eventsFolded(context.Background(), tenant, int64(total-1))
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if folded {
		t.Fatal("a fold behind the captured watermark must wait")
	}
	target, waiting := r.eventTarget[tenant]
	if !waiting || target != int64(total) {
		t.Fatalf("the waiting round must hold the fresh watermark %d, got %d (waiting=%v)",
			total, target, waiting)
	}

	// The fold reaches the watermark: the check passes and clears the target again.
	folded, err = r.eventsFolded(context.Background(), tenant, int64(total))
	if err != nil {
		t.Fatalf("third check: %v", err)
	}
	if !folded {
		t.Fatal("a fold that reached the captured watermark must pass")
	}
	if _, waiting := r.eventTarget[tenant]; waiting {
		t.Fatal("a passed check must drop the target")
	}
}

// TestClockSkewTooLarge checks the skew guard's boundaries: a pod clock within the limit in
// either direction runs the round, and one past the limit in either direction skips it.
// The guard exists because a round run at a stale time sends negative corrections and then
// re-bills forward, which the customer sees as oscillating charges.
func TestClockSkewTooLarge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		pod  time.Time
		db   time.Time
		want bool
	}{
		{"in sync", now, now, false},
		{"pod ahead within the limit", now.Add(ClockSkewLimit - time.Minute), now, false},
		{"pod behind within the limit", now.Add(-ClockSkewLimit + time.Minute), now, false},
		{"pod ahead past the limit", now.Add(ClockSkewLimit + time.Minute), now, true},
		{"pod behind past the limit", now.Add(-ClockSkewLimit - time.Minute), now, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clockSkewTooLarge(c.pod, c.db); got != c.want {
				t.Fatalf("clockSkewTooLarge with the pod %s from the database = %v, want %v",
					c.pod.Sub(c.db), got, c.want)
			}
		})
	}
}

// TestRoundAlreadyRan checks the backwards-clock guard: a round waits while the database
// clock has not passed the last round's time, with the boundary at exactly the last round.
func TestRoundAlreadyRan(t *testing.T) {
	roundTime := time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC)
	if roundAlreadyRan(roundTime, nil) {
		t.Fatal("a tenant's first round never waits")
	}
	last := roundTime.Add(-time.Minute)
	if roundAlreadyRan(roundTime, &last) {
		t.Fatal("a round past the last round's time must run")
	}
	equal := roundTime
	if !roundAlreadyRan(roundTime, &equal) {
		t.Fatal("a round at exactly the last round's time must wait")
	}
	future := roundTime.Add(time.Minute)
	if !roundAlreadyRan(roundTime, &future) {
		t.Fatal("a round before the last round's time must wait")
	}
}
