/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/usage"
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

	r, err := New(nil, nil, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(), &sync.RWMutex{})
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

// TestOverflowingQuantityFailsTheRoundNamingTheResource checks that a quantity no int64 can
// hold fails the whole round naming the resource it belongs to: a wrapped value would
// silently bill negative usage.
func TestOverflowingQuantityFailsTheRoundNamingTheResource(t *testing.T) {
	const tenant = "overflow"
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	store := testenv.Database(t)
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	facts := []database.Fact{
		created("Volume", "vol-1", base),
		state("Volume", "vol-1", base, "AVAILABLE"),
		fact("Volume", "vol-1", "status.protocol", base, "BLOCK"),
		fact("Volume", "vol-1", "status.vendor_volume_id", base, "vend-9"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", base, "200000000"),
	}
	if err := database.InsertFacts(ctx, store.Pool(), tenant, facts, 1); err != nil {
		t.Fatal(err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatal(err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{testenv.DefaultRevision(t)}, "us-east", owners)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(store, calculator, nil, nil, logr.Discard(), &sync.RWMutex{})
	if err != nil {
		t.Fatal(err)
	}
	progress := database.Progress{EventOffset: 1}
	err = r.compare(ctx, store.Pool(), tenant, progress, base.Add(14*time.Hour))
	if err == nil {
		ledger, ledgerErr := database.LedgerPiecesWhere(ctx, store.Pool(), tenant, "")
		if ledgerErr != nil {
			t.Fatal(ledgerErr)
		}
		for _, row := range ledger {
			if row.QuantityMicro < 0 {
				t.Fatalf("the round committed a wrapped negative quantity %d", row.QuantityMicro)
			}
		}
		t.Fatal("the round must fail on a quantity no int64 can hold")
	}
	if !strings.Contains(err.Error(), "vol-1") {
		t.Fatalf("the failure must name the resource, got %q", err.Error())
	}
}

// TestFractionalRoundsConserveTheCommittedQuantity checks that a fractional rate cannot make
// the sent total disagree with the committed ledger: the quantity a difference publishes and
// the row the ledger commits must come from one rounding, so rounds at 1.5 and 2.5 seconds
// publish 2 then 1 micro units and commit 3. "1e-6" is a valid DNS-label instance type name
// whose numeric value the multiplier parser accepts.
func TestFractionalRoundsConserveTheCommittedQuantity(t *testing.T) {
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	revision, err := rules.Parse(rules.NamePrefix+"0001", "1970-01-01T00:00:00Z", `- resource: ComputeInstance
  meter: weighted
  unit: second
  billable_when:
    states: [RUNNING]
  multiplier: instance_type
`)
	if err != nil {
		t.Fatal(err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", nil)
	if err != nil {
		t.Fatal(err)
	}
	facts := []database.Fact{
		created("ComputeInstance", "vm-1", base.Add(-time.Minute)),
		state("ComputeInstance", "vm-1", base.Add(-time.Minute), "STARTING"),
		state("ComputeInstance", "vm-1", base, "RUNNING"),
		fact("ComputeInstance", "vm-1", "status.instance_type", base, "1e-6"),
	}
	first, err := calculator.Expected(
		"acme", "ComputeInstance", "vm-1", facts, nil, base.Add(1500*time.Millisecond),
	)
	if err != nil || len(first) != 1 {
		t.Fatalf("first pieces=%+v error=%v", first, err)
	}
	key := first[0].Key

	var sent int64
	differences, err := diff(key, first, nil, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, difference := range differences {
		sent += difference.quantityMicro
	}
	ledger, err := expectedRows(first, key)
	if err != nil {
		t.Fatal(err)
	}

	second, err := calculator.Expected(
		"acme", "ComputeInstance", "vm-1", facts, nil, base.Add(2500*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	differences, err = diff(key, second, ledger, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, difference := range differences {
		sent += difference.quantityMicro
	}
	committed, err := expectedRows(second, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 1 {
		t.Fatalf("expected one committed row, got %+v", committed)
	}
	if sent != committed[0].QuantityMicro {
		t.Fatalf(
			"the two successful rounds publish %d micro units but commit %d; round(2.5)=3",
			sent, committed[0].QuantityMicro,
		)
	}
}

// TestRootOfWalksToTheTopmostOwner checks the ownership walk: the gate a resource inherits
// comes from the topmost owner, so a component of a component resolves past its immediate
// owner, and a missing root returns empty facts so billing stays shut.
func TestRootOfWalksToTheTopmostOwner(t *testing.T) {
	const tenant = "rootwalk"
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	store := testenv.Database(t)
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}

	revision := rules.Revision{
		Name: "synthetic", Number: "0001", EffectiveAt: time.Unix(0, 0).UTC(),
		Meters: []rules.Meter{
			{Resource: "WaaS2", Meter: "platform", Unit: "second",
				Billable: rules.BillableWhen{States: []string{"ACTIVE"}, TurnsBillableOnce: []string{"ACTIVE"}}},
			{Resource: "WaaS", Meter: "service", Unit: "second",
				Billable: rules.BillableWhen{States: []string{"ON"}}},
		},
	}
	owners := map[string]usage.OwnerSpec{
		"WaaS": {Field: "status.owner", Resource: "WaaS2"},
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	r, err := New(store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(), &sync.RWMutex{})
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}

	waasFacts := []database.Fact{
		created("WaaS", "waas-1", base),
		state("WaaS", "waas-1", base, "ON"),
		fact("WaaS", "waas-1", "status.owner", base, "top-1"),
	}
	waas2Facts := []database.Fact{
		created("WaaS2", "top-1", base),
		state("WaaS2", "top-1", base, "ACTIVE"),
	}
	if err := database.InsertFacts(ctx, store.Pool(), tenant, waasFacts, 1); err != nil {
		t.Fatalf("writing WaaS facts: %v", err)
	}
	if err := database.InsertFacts(ctx, store.Pool(), tenant, waas2Facts, 2); err != nil {
		t.Fatalf("writing WaaS2 facts: %v", err)
	}

	root, err := r.rootOf(ctx, store.Pool(), tenant, "WaaS", waasFacts)
	if err != nil {
		t.Fatalf("resolving root: %v", err)
	}
	if root == nil || root.Type != "WaaS2" || root.ID != "top-1" || len(root.Facts) == 0 {
		t.Fatalf("root is %+v, want WaaS2 top-1 with facts", root)
	}

	// A missing root holds billing shut: empty facts, never a silent fallback.
	if err := database.InsertFacts(ctx, store.Pool(), tenant, []database.Fact{
		fact("WaaS", "waas-2", "status.owner", base, "gone-1"),
	}, 3); err != nil {
		t.Fatalf("writing orphan facts: %v", err)
	}
	root, err = r.rootOf(ctx, store.Pool(), tenant, "WaaS", []database.Fact{
		created("WaaS", "waas-2", base),
		fact("WaaS", "waas-2", "status.owner", base, "gone-1"),
	})
	if err != nil {
		t.Fatalf("resolving missing root: %v", err)
	}
	if root == nil || root.Type != "WaaS2" || root.ID != "gone-1" || len(root.Facts) != 0 {
		t.Fatalf("missing root is %+v, want WaaS2 gone-1 with no facts", root)
	}
}

// TestScanResourcesDragsOwnedResourcesAlong checks the round's dirtying: a changed cluster
// pulls its workers into the recompute set, so the round after the cluster reaches READY
// bills the workers retroactively, without a fact of their own.
func TestScanResourcesDragsOwnedResourcesAlong(t *testing.T) {
	const tenant = "dirtying"
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	store := testenv.Database(t)
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}

	revision := testenv.DefaultRevision(t)
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	r, err := New(store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(), &sync.RWMutex{})
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}

	// A worker folded in the previous round, and a cluster fact written after it.
	if err := database.InsertFacts(ctx, store.Pool(), tenant, []database.Fact{
		created("BareMetalInstance", "bm-1", base),
		state("BareMetalInstance", "bm-1", base, "STARTING"),
		fact("BareMetalInstance", "bm-1", "spec.instance_type", base, "m7a"),
		fact("BareMetalInstance", "bm-1", "status.cluster", base, "c-1"),
		fact("BareMetalInstance", "bm-1", "status.node_set", base, "workers"),
	}, 1); err != nil {
		t.Fatalf("writing worker facts: %v", err)
	}
	lastRound := base.Add(time.Minute)
	if err := database.InsertFacts(ctx, store.Pool(), tenant, []database.Fact{
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		state("Cluster", "c-1", lastRound.Add(time.Second), "READY"),
	}, 2); err != nil {
		t.Fatalf("writing cluster facts: %v", err)
	}

	progress := database.Progress{
		LastRoundTime: &lastRound, LastRoundEventOffset: 1,
	}
	full, _, err := r.scanResources(ctx, store.Pool(), tenant, progress, lastRound.Add(time.Minute))
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	found := map[resource]bool{}
	for _, entry := range full {
		found[entry] = true
	}
	if !found[resource{"Cluster", "c-1"}] {
		t.Fatal("the changed cluster must be recomputed")
	}
	if !found[resource{"BareMetalInstance", "bm-1"}] {
		t.Fatal("the worker of a changed cluster must be recomputed with it")
	}
}

// created, state, and fact build history rows in round tests.
func created(resource, id string, at time.Time) database.Fact {
	return database.Fact{ResourceType: resource, ResourceID: id, Field: "created", Since: at, Value: "true"}
}

func state(resource, id string, since time.Time, value string) database.Fact {
	return database.Fact{ResourceType: resource, ResourceID: id, Field: "status.state", Since: since, Value: value}
}

func fact(resource, id, field string, since time.Time, value string) database.Fact {
	return database.Fact{ResourceType: resource, ResourceID: id, Field: field, Since: since, Value: value}
}

// TestScanResourcesDragsOwnedThroughRevisionChanges checks the revision drag: a revision
// that takes effect this round and redefines only the cluster's meters must recompute the
// cluster's workers too, because their inherited gate changed. A worker with a shut gate
// has no ledger rows and no new facts, so nothing else would bring it in.
func TestScanResourcesDragsOwnedThroughRevisionChanges(t *testing.T) {
	const tenant = "revisiondrag"
	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	store := testenv.Database(t)
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}

	// A worker folded in the previous round, with no facts since, and its cluster.
	if err := database.InsertFacts(ctx, store.Pool(), tenant, []database.Fact{
		created("BareMetalInstance", "bm-1", base),
		state("BareMetalInstance", "bm-1", base, "STARTING"),
		fact("BareMetalInstance", "bm-1", "spec.instance_type", base, "m7a"),
		fact("BareMetalInstance", "bm-1", "status.cluster", base, "c-1"),
		fact("BareMetalInstance", "bm-1", "status.node_set", base, "workers"),
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
	}, 1); err != nil {
		t.Fatalf("writing worker facts: %v", err)
	}

	lastRound := base.Add(time.Minute)
	revision := rules.Revision{
		Name: "synthetic", Number: "0002", EffectiveAt: lastRound.Add(time.Second),
		Meters: []rules.Meter{
			{
				Resource: "Cluster", Meter: "control_plane", Unit: "second",
				Billable: rules.BillableWhen{
					States: []string{"PROGRESSING", "READY"}, TurnsBillableOnce: []string{"READY"},
				},
			},
		},
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	r, err := New(store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(), &sync.RWMutex{})
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}

	progress := database.Progress{
		LastRoundTime: &lastRound, LastRoundEventOffset: 1,
	}
	full, _, err := r.scanResources(ctx, store.Pool(), tenant, progress, lastRound.Add(time.Minute))
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	found := map[resource]bool{}
	for _, entry := range full {
		found[entry] = true
	}
	if !found[resource{"BareMetalInstance", "bm-1"}] {
		t.Fatal("a revision that redefines the cluster's meters must recompute its workers")
	}
}
