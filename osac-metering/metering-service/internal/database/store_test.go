/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osac-project/osac-metering/internal/database"
)

func TestStoreFactsAndProgressAreIdempotent(t *testing.T) {
	ctx := context.Background()
	_, pool := startDatabase(t)

	store := database.NewStore(pool)

	if err := store.EnsureProgress(ctx, "acme"); err != nil {
		t.Fatalf("ensure progress: %v", err)
	}

	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	facts := []database.Fact{
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "created",
			Since: base, Value: "true"},
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "status.state",
			Since: base, Value: "STARTING"},
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "status.state",
			Since: base.Add(40 * time.Second), Value: "RUNNING"},
	}

	write := func(offset int64) {
		t.Helper()
		tx, err := store.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := database.InsertFacts(ctx, tx, "acme", facts, offset); err != nil {
			t.Fatalf("insert facts: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	write(4)
	write(4)
	write(5)

	got, err := database.ResourceFacts(ctx, pool, "acme", "ComputeInstance", "vm-7")
	if err != nil {
		t.Fatalf("read facts: %v", err)
	}
	if len(got) != len(facts) {
		t.Fatalf("expected %d facts after rewrites, got %d", len(facts), len(got))
	}

	progress, err := database.GetProgress(ctx, pool, "acme")
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if progress.EventOffset != 5 {
		t.Errorf("expected event offset 5, got %d", progress.EventOffset)
	}
}

func TestStoreLedgerReplaceAndScan(t *testing.T) {
	ctx := context.Background()
	_, pool := startDatabase(t)

	store := database.NewStore(pool)

	if err := store.EnsureProgress(ctx, "acme"); err != nil {
		t.Fatalf("ensure progress: %v", err)
	}

	round1 := time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC)
	round2 := round1.Add(time.Minute)
	key := database.LedgerKey{
		ResourceType: "ComputeInstance", ResourceID: "vm-7",
		Meter: "compute", Component: "compute", Dimensions: `{"tenant":"acme"}`,
	}
	pieces := []database.LedgerPiece{
		{Key: key, StartTime: round1, EndTime: round2, QuantityMicro: 60_000_000},
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := database.ReplaceLedger(ctx, tx, "acme", key, pieces); err != nil {
		t.Fatalf("replace ledger: %v", err)
	}
	if err := database.ReplaceLedger(ctx, tx, "acme", key, pieces); err != nil {
		t.Fatalf("replace ledger again: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	all, err := database.LedgerPiecesWhere(ctx, pool, "acme", "")
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(all) != 1 || all[0].QuantityMicro != 60_000_000 {
		t.Fatalf("expected one piece of 60s, got %+v", all)
	}

	ending, err := database.LedgerPiecesWhere(
		ctx, pool, "acme", "end_time = $2", round2,
	)
	if err != nil {
		t.Fatalf("scan ledger by end time: %v", err)
	}
	if len(ending) != 1 {
		t.Fatalf("expected one piece ending at the round, got %d", len(ending))
	}
}

func TestStoreOutboxRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, pool := startDatabase(t)

	store := database.NewStore(pool)

	if err := store.EnsureProgress(ctx, "acme"); err != nil {
		t.Fatalf("ensure progress: %v", err)
	}

	round := time.Date(2026, 10, 5, 10, 2, 0, 0, time.UTC)
	rows := []database.OutboxRow{
		{ID: "id-1", Payload: []byte(`{"a":1}`), RoundTime: round},
		{ID: "id-2", Payload: []byte(`{"a":2}`), RoundTime: round},
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := database.EnqueueHeartbeats(ctx, tx, "acme", rows); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := database.EnqueueHeartbeats(ctx, tx, "acme", rows); err != nil {
		t.Fatalf("enqueue duplicates: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := database.NextOutbox(ctx, pool, "acme", 10)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 outbox rows, got %d", len(got))
	}
	if got[0].ID != "id-1" || got[1].ID != "id-2" {
		t.Fatalf("outbox rows out of order: %+v", got)
	}

	if err := database.DeleteOutbox(ctx, pool, "acme", []int64{got[0].Seq, got[1].Seq}); err != nil {
		t.Fatalf("delete outbox: %v", err)
	}
	if got, err := database.NextOutbox(ctx, pool, "acme", 10); err != nil {
		t.Fatalf("read outbox: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("expected empty outbox, got %d rows", len(got))
	}
}

// TestDeleteTenantRowsRollsBackOnFailure pins the all-or-nothing contract: a
// delete that fails part-way must leave the tenant's rows in every table,
// because a tenant with rows in some tables and none in the others has half a
// history and half a ledger.
func TestDeleteTenantRowsRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	_, pool := startDatabase(t)

	store := database.NewStore(pool)

	if err := store.EnsureProgress(ctx, "acme"); err != nil {
		t.Fatalf("ensure progress: %v", err)
	}

	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	facts := []database.Fact{
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "status.state",
			Since: base, Value: "RUNNING"},
	}
	if err := database.InsertFacts(ctx, tx, "acme", facts, 10); err != nil {
		t.Fatalf("insert facts: %v", err)
	}
	key := database.LedgerKey{
		ResourceType: "ComputeInstance", ResourceID: "vm-7",
		Meter: "compute", Component: "compute", Dimensions: `{"tenant":"acme"}`,
	}
	if err := database.ReplaceLedger(ctx, tx, "acme", key, []database.LedgerPiece{
		{Key: key, StartTime: base, EndTime: base.Add(time.Minute), QuantityMicro: 60_000_000},
	}); err != nil {
		t.Fatalf("replace ledger: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Fail the second delete (ledger) with a trigger; the first (history)
	// must roll back with it.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_ledger_delete() RETURNS trigger AS $$
BEGIN
	RAISE EXCEPTION 'ledger delete forced to fail';
END $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("creating failure function: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`CREATE TRIGGER fail_ledger_delete BEFORE DELETE ON ledger FOR EACH STATEMENT EXECUTE FUNCTION fail_ledger_delete()`,
	); err != nil {
		t.Fatalf("creating failure trigger: %v", err)
	}

	if err := store.DeleteTenantRows(ctx, "acme"); err == nil {
		t.Fatal("deleting tenant rows must fail when a delete fails")
	}

	got, err := database.ResourceFacts(ctx, pool, "acme", "ComputeInstance", "vm-7")
	if err != nil {
		t.Fatalf("reading history after the failed delete: %v", err)
	}
	if len(got) != len(facts) {
		t.Fatalf(
			"the first delete must roll back with the failed second one: expected %d history rows, got %d",
			len(facts), len(got),
		)
	}
}

func TestStoreResourcesWithNewFacts(t *testing.T) {
	ctx := context.Background()
	_, pool := startDatabase(t)

	store := database.NewStore(pool)

	if err := store.EnsureProgress(ctx, "acme"); err != nil {
		t.Fatalf("ensure progress: %v", err)
	}

	base := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	err = database.InsertFacts(ctx, tx, "acme", []database.Fact{
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "status.state",
			Since: base, Value: "RUNNING"},
	}, 10)
	if err != nil {
		t.Fatalf("insert facts: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	resources, err := database.ResourcesWithNewFacts(
		ctx, pool, "acme", 9, base.Add(-time.Hour),
	)
	if err != nil {
		t.Fatalf("scan new facts: %v", err)
	}
	if len(resources) != 1 || resources[0].ResourceID != "vm-7" {
		t.Fatalf("expected vm-7 to have new facts, got %+v", resources)
	}

	resources, err = database.ResourcesWithNewFacts(
		ctx, pool, "acme", 10, base.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("scan new facts: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected no resources with new facts, got %+v", resources)
	}
}

func startDatabase(t *testing.T) (*database.Container, *pgxpool.Pool) {
	t.Helper()
	container, err := database.NewContainer(logr.Discard())
	if err != nil {
		t.Fatalf("creating database container: %v", err)
	}
	ctx := context.Background()
	if err := container.Start(ctx); err != nil {
		t.Fatalf("starting database container: %v", err)
	}
	instance := container.NewInstance()
	pool, err := instance.Pool(ctx)
	if err != nil {
		_ = container.Stop(ctx)
		t.Fatalf("creating pool: %v", err)
	}
	t.Cleanup(func() { _ = container.Stop(ctx) })
	return container, pool
}
