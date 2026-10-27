/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the part of a connection pool or transaction that queries run against. Loops that
// need several statements to commit together pass a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Store reads and writes the metering database. All methods are safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a store on top of a connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Pool returns the underlying connection pool.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Clock returns the database's current time. Rounds read it so every pod agrees on
// the round time regardless of the pod clocks.
func (s *Store) Clock(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("reading the database clock: %w", err)
	}
	return now.UTC(), nil
}

// Begin starts a transaction.
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	return s.pool.Begin(ctx)
}

// OutboxDepth counts the tenant's heartbeats the rounds wrote and the relay has not sent
// yet. The relay reports it after every drain so a backlog is visible while the pod stays
// ready.
func (s *Store) OutboxDepth(ctx context.Context, tenant string) (int64, error) {
	var depth int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE tenant = $1", tenant).Scan(&depth); err != nil {
		return 0, fmt.Errorf("counting the outbox of %s: %w", tenant, err)
	}
	return depth, nil
}

// Fact is one row of history: a resource's field holds the value since a moment in time.
type Fact struct {
	ResourceType string
	ResourceID   string
	Field        string
	Since        time.Time
	Value        string
}

// Progress is a tenant's position in its topics and its rounds.
type Progress struct {
	Tenant               string
	EventOffset          int64
	HeartbeatOffset      int64
	LastRoundTime        *time.Time
	LastRoundEventOffset int64
}

// LedgerKey identifies one meter: a resource, a meter definition and component, and one set of
// dimension values.
type LedgerKey struct {
	ResourceType string
	ResourceID   string
	Meter        string
	Component    string
	Dimensions   string
}

// LedgerPiece is a stretch of committed usage: a net quantity in micro units of the meter's
// unit, over a half-open time interval. A piece with a zero quantity marks an open run that is
// still too short to bill, so the next round keeps watching it.
type LedgerPiece struct {
	Key           LedgerKey
	StartTime     time.Time
	EndTime       time.Time
	QuantityMicro int64
}

// OutboxRow is a heartbeat a round wrote but the relay has not sent yet.
type OutboxRow struct {
	Seq       int64
	ID        string
	Payload   []byte
	RoundTime time.Time
}

// ReplayedHeartbeatIDs returns the ids catch-up applied during the recovery in
// progress, so a resent copy counts once even after a crash mid-replay.
func ReplayedHeartbeatIDs(ctx context.Context, q Querier, tenant string) (map[string]bool, error) {
	rows, err := q.Query(
		ctx, `SELECT id FROM replayed_heartbeats WHERE tenant = $1`, tenant,
	)
	if err != nil {
		return nil, fmt.Errorf("reading replayed heartbeat ids of tenant %s: %w", tenant, err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning replayed heartbeat id of tenant %s: %w", tenant, err)
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// RecordReplayedHeartbeats stores the ids catch-up just applied, in the group's own
// transaction: a crash mid-replay keeps the memory of what already counted.
func RecordReplayedHeartbeats(ctx context.Context, q Querier, tenant string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(
		ctx, `INSERT INTO replayed_heartbeats (tenant, id)
		      SELECT $1, id FROM unnest($2::text[]) AS id
		      ON CONFLICT DO NOTHING`, tenant, ids,
	)
	if err != nil {
		return fmt.Errorf("recording replayed heartbeats of tenant %s: %w", tenant, err)
	}
	return nil
}

// PruneReplayedHeartbeats forgets the replay's id memory. The round's own commit makes
// it redundant: every heartbeat on the topic is now from a round at or before the last
// round, which the replay skips on its own.
func PruneReplayedHeartbeats(ctx context.Context, q Querier, tenant string) error {
	_, err := q.Exec(ctx, `DELETE FROM replayed_heartbeats WHERE tenant = $1`, tenant)
	if err != nil {
		return fmt.Errorf("pruning replayed heartbeats of tenant %s: %w", tenant, err)
	}
	return nil
}

// RuleRevisionRow is a revision metering has used.
type RuleRevisionRow struct {
	Revision    string
	EffectiveAt time.Time
	MetersYAML  string
}

// LockKind tells TakeTenantLock which loop's lock to take. The fold's lock is separate from the
// round's lock, so the fold keeps going while a round runs.
type LockKind int

const (
	// LockFold serializes folding a tenant's events.
	LockFold LockKind = iota

	// LockRound serializes a tenant's heartbeat rounds.
	LockRound

	// LockRelay serializes sending a tenant's heartbeats.
	LockRelay
)

func (k LockKind) name() string {
	switch k {
	case LockRound:
		return "metering-round"
	case LockRelay:
		return "metering-relay"
	default:
		return "metering-fold"
	}
}

// TakeTenantLock takes the tenant's advisory lock for a loop kind and holds it until the
// transaction ends. Overlapping pods take turns instead of racing.
func TakeTenantLock(ctx context.Context, q Querier, kind LockKind, tenant string) error {
	_, err := q.Exec(
		ctx, "SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))", kind.name(), tenant,
	)
	if err != nil {
		return fmt.Errorf("taking %s lock for tenant %s: %w", kind.name(), tenant, err)
	}
	return nil
}

// EnsureProgress creates the tenant's progress row if it does not exist yet. A new tenant starts
// at offset 0 of both its topics.
func (s *Store) EnsureProgress(ctx context.Context, tenant string) error {
	_, err := s.pool.Exec(
		ctx,
		"INSERT INTO progress (tenant) VALUES ($1) ON CONFLICT (tenant) DO NOTHING",
		tenant,
	)
	if err != nil {
		return fmt.Errorf("ensuring progress for tenant %s: %w", tenant, err)
	}
	return nil
}

// GetProgress returns the tenant's progress.
func GetProgress(ctx context.Context, q Querier, tenant string) (Progress, error) {
	var p Progress
	var lastRound *time.Time
	err := q.QueryRow(
		ctx,
		`SELECT tenant, event_offset, heartbeat_offset, last_round_time, last_round_event_offset
		 FROM progress WHERE tenant = $1`,
		tenant,
	).Scan(&p.Tenant, &p.EventOffset, &p.HeartbeatOffset, &lastRound, &p.LastRoundEventOffset)
	if err != nil {
		return Progress{}, fmt.Errorf("reading progress of tenant %s: %w", tenant, err)
	}
	if lastRound != nil {
		p.LastRoundTime = lastRound
	}
	return p, nil
}

// GetTenantProgress reads the tenant's progress from the pool.
func (s *Store) GetTenantProgress(ctx context.Context, tenant string) (Progress, error) {
	return GetProgress(ctx, s.pool, tenant)
}

// InsertFacts writes facts that the tenant's event carries, and moves the tenant's event offset
// to the given next offset, in the same transaction. The offset only moves forward. Facts are
// consumed in log order, so a fact that exists already is updated to the arriving value: two
// changes of one field within the same second collapse to the value the log carries last, and
// replaying an event writes the value it already wrote.
func InsertFacts(
	ctx context.Context, q Querier, tenant string, facts []Fact, nextOffset int64,
) error {
	batch := &pgx.Batch{}
	for _, fact := range facts {
		batch.Queue(
			`INSERT INTO history
			     (tenant, resource_type, resource_id, field, since, value, event_offset)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (tenant, resource_type, resource_id, field, since)
			 DO UPDATE SET value = EXCLUDED.value, event_offset = EXCLUDED.event_offset`,
			tenant, fact.ResourceType, fact.ResourceID, fact.Field, fact.Since, fact.Value,
			nextOffset,
		)
	}
	batch.Queue(
		`UPDATE progress SET event_offset = GREATEST(event_offset, $2) WHERE tenant = $1`,
		tenant, nextOffset,
	)
	if err := q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("writing %d facts of tenant %s: %w", len(facts), tenant, err)
	}
	return nil
}

// AdvanceEventOffset moves the tenant's event offset forward. Events of types metering does
// not track only advance the offset.
func AdvanceEventOffset(ctx context.Context, q Querier, tenant string, offset int64) error {
	_, err := q.Exec(
		ctx,
		`UPDATE progress SET event_offset = GREATEST(event_offset, $2) WHERE tenant = $1`,
		tenant, offset,
	)
	if err != nil {
		return fmt.Errorf("advancing event offset of tenant %s: %w", tenant, err)
	}
	return nil
}

// AdvanceHeartbeatOffset moves the tenant's heartbeat offset forward, never back.
func AdvanceHeartbeatOffset(ctx context.Context, q Querier, tenant string, offset int64) error {
	_, err := q.Exec(
		ctx,
		`UPDATE progress SET heartbeat_offset = GREATEST(heartbeat_offset, $2) WHERE tenant = $1`,
		tenant, offset,
	)
	if err != nil {
		return fmt.Errorf("advancing heartbeat offset of tenant %s: %w", tenant, err)
	}
	return nil
}

// SetLastRound records the time of the tenant's last round and the event offset it folded.
func SetLastRound(
	ctx context.Context, q Querier, tenant string, roundTime time.Time, eventOffset int64,
) error {
	_, err := q.Exec(
		ctx,
		`UPDATE progress
		 SET last_round_time = $2, last_round_event_offset = GREATEST(last_round_event_offset, $3)
		 WHERE tenant = $1`,
		tenant, roundTime, eventOffset,
	)
	if err != nil {
		return fmt.Errorf("recording last round of tenant %s: %w", tenant, err)
	}
	return nil
}

// LastRoundTime returns the database's billing position: the latest last_round_time
// across tenants, the zero time when no tenant has rounded. Startup passes it to the
// rule-revision check, because a revision may not change spans this database already
// billed, and the last round is what already billed means. A tenant whose round is
// still NULL has billed nothing and does not count.
func (s *Store) LastRoundTime(ctx context.Context) (time.Time, error) {
	var lastRound *time.Time
	if err := s.pool.QueryRow(
		ctx, `SELECT max(last_round_time) FROM progress`,
	).Scan(&lastRound); err != nil {
		return time.Time{}, fmt.Errorf("reading the last round time: %w", err)
	}
	if lastRound == nil {
		return time.Time{}, nil
	}
	return *lastRound, nil
}

// ResourceFacts returns one resource's facts ordered by field and time.
func ResourceFacts(
	ctx context.Context, q Querier, tenant, resourceType, resourceID string,
) ([]Fact, error) {
	rows, err := q.Query(
		ctx,
		`SELECT field, since, value
		 FROM history
		 WHERE tenant = $1 AND resource_type = $2 AND resource_id = $3
		 ORDER BY field, since`,
		tenant, resourceType, resourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("reading facts of %s %s: %w", resourceType, resourceID, err)
	}
	defer rows.Close()
	var facts []Fact
	for rows.Next() {
		var fact Fact
		fact.ResourceType, fact.ResourceID = resourceType, resourceID
		if err := rows.Scan(&fact.Field, &fact.Since, &fact.Value); err != nil {
			return nil, fmt.Errorf("scanning fact of %s %s: %w", resourceType, resourceID, err)
		}
		// pgx scans timestamptz in time.Local, while the round's computed
		// boundaries carry UTC: a time.Time map key compares the location
		// pointer, so the same instant from the two sources must not mix.
		fact.Since = fact.Since.UTC()
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading facts of %s %s: %w", resourceType, resourceID, err)
	}
	return facts, nil
}

// TenantResources returns the tenant's resources that have facts.
func TenantResources(ctx context.Context, q Querier, tenant string) ([]LedgerKey, error) {
	return resourcesWhere(ctx, q, tenant,
		`SELECT DISTINCT resource_type, resource_id FROM history WHERE tenant = $1`)
}

// AllTenantResources returns the tenant's resources that have facts or ledger rows. The
// first round of a recovery scans these: a ledger rebuilt from heartbeats can hold a
// resource whose events are gone, and that resource must still be compared, so its
// unjustified usage is reversed.
func AllTenantResources(ctx context.Context, q Querier, tenant string) ([]LedgerKey, error) {
	return resourcesWhere(ctx, q, tenant,
		`SELECT resource_type, resource_id FROM history WHERE tenant = $1
		 UNION
		 SELECT resource_type, resource_id FROM ledger WHERE tenant = $1`)
}

func resourcesWhere(
	ctx context.Context, q Querier, tenant, query string,
) ([]LedgerKey, error) {
	rows, err := q.Query(
		ctx,
		query,
		tenant,
	)
	if err != nil {
		return nil, fmt.Errorf("listing resources of tenant %s: %w", tenant, err)
	}
	defer rows.Close()
	var resources []LedgerKey
	for rows.Next() {
		var key LedgerKey
		if err := rows.Scan(&key.ResourceType, &key.ResourceID); err != nil {
			return nil, fmt.Errorf("scanning resource of tenant %s: %w", tenant, err)
		}
		resources = append(resources, key)
	}
	return resources, rows.Err()
}

// ResourcesWithNewFacts returns the tenant's resources with a fact written at or after the
// given event offset or dated at or after the given time. Only those resources can have
// changed usage since the previous round. Both bounds are inclusive: a round bills the
// half-open window [last round, now), so a fact dated exactly at the last round time
// belongs to the next window, and its resource must reach the round after the boundary
// even though the boundary round consumed the fact and wrote no ledger row for it.
func ResourcesWithNewFacts(
	ctx context.Context, q Querier, tenant string, afterOffset int64, afterTime time.Time,
) ([]LedgerKey, error) {
	rows, err := q.Query(
		ctx,
		`SELECT DISTINCT resource_type, resource_id
		 FROM history
		 WHERE tenant = $1 AND (event_offset >= $2 OR since >= $3)`,
		tenant, afterOffset, afterTime,
	)
	if err != nil {
		return nil, fmt.Errorf("finding resources with new facts: %w", err)
	}
	defer rows.Close()
	var resources []LedgerKey
	for rows.Next() {
		var key LedgerKey
		if err := rows.Scan(&key.ResourceType, &key.ResourceID); err != nil {
			return nil, fmt.Errorf("scanning resource with new facts: %w", err)
		}
		resources = append(resources, key)
	}
	return resources, rows.Err()
}

// LedgerPiecesWhere returns the tenant's ledger pieces filtered by an extra SQL condition over
// the ledger columns. The condition's placeholders number from $2, after the tenant. An empty
// condition returns every piece.
func LedgerPiecesWhere(
	ctx context.Context, q Querier, tenant, condition string, args ...any,
) ([]LedgerPiece, error) {
	sql := `SELECT resource_type, resource_id, meter, component, dimensions,
	               start_time, end_time, quantity_micro
	        FROM ledger WHERE tenant = $1`
	allArgs := append([]any{tenant}, args...)
	if condition != "" {
		sql += " AND (" + condition + ")"
	}
	rows, err := q.Query(ctx, sql, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("reading ledger of tenant %s: %w", tenant, err)
	}
	defer rows.Close()
	var pieces []LedgerPiece
	for rows.Next() {
		var piece LedgerPiece
		if err := rows.Scan(
			&piece.Key.ResourceType, &piece.Key.ResourceID, &piece.Key.Meter,
			&piece.Key.Component, &piece.Key.Dimensions, &piece.StartTime, &piece.EndTime,
			&piece.QuantityMicro,
		); err != nil {
			return nil, fmt.Errorf("scanning ledger piece of tenant %s: %w", tenant, err)
		}
		// pgx scans timestamptz in time.Local, while the round's computed
		// boundaries carry UTC: a time.Time map key compares the location
		// pointer, so the same instant from the two sources must not mix.
		piece.StartTime = piece.StartTime.UTC()
		piece.EndTime = piece.EndTime.UTC()
		pieces = append(pieces, piece)
	}
	return pieces, rows.Err()
}

// SyncLedger makes the ledger pieces of one meter equal to the given pieces while touching
// only the rows that change: a round that extends a meter's tail updates or appends the tail
// piece instead of rewriting every row the meter already has. Rows that end at or before
// tailFrom are never deleted: a tail extension owns only the rows after that moment, so the
// pieces it carries are a tail of the meter's history, not all of it. With a zero tailFrom
// every row is owned: the pieces replace the meter's whole history.
func SyncLedger(
	ctx context.Context, q Querier, tenant string, key LedgerKey, pieces []LedgerPiece,
	tailFrom time.Time,
) error {
	starts := make([]time.Time, 0, len(pieces))
	batch := &pgx.Batch{}
	for _, piece := range pieces {
		starts = append(starts, piece.StartTime)
		batch.Queue(
			`INSERT INTO ledger
			     (tenant, resource_type, resource_id, meter, component, dimensions,
			      start_time, end_time, quantity_micro)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 ON CONFLICT (tenant, resource_type, resource_id, meter, component, dimensions,
			              start_time)
			 DO UPDATE SET end_time = EXCLUDED.end_time, quantity_micro = EXCLUDED.quantity_micro
			 WHERE ledger.end_time <> EXCLUDED.end_time
			    OR ledger.quantity_micro <> EXCLUDED.quantity_micro`,
			tenant, key.ResourceType, key.ResourceID, key.Meter, key.Component, key.Dimensions,
			piece.StartTime, piece.EndTime, piece.QuantityMicro,
		)
	}
	// Rows whose start no longer appears among the pieces are gone; an empty piece set
	// removes every owned row.
	batch.Queue(
		`DELETE FROM ledger
		 WHERE tenant = $1 AND resource_type = $2 AND resource_id = $3
		   AND meter = $4 AND component = $5 AND dimensions = $6
		   AND end_time > $8 AND start_time <> ALL($7)`,
		tenant, key.ResourceType, key.ResourceID, key.Meter, key.Component, key.Dimensions,
		starts, tailFrom,
	)
	if err := q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("syncing ledger of meter %s: %w", key.Meter, err)
	}
	return nil
}

// EnqueueHeartbeats adds heartbeats to the outbox. The ID is unique: a heartbeat resent after a
// crash keeps its ID.
func EnqueueHeartbeats(
	ctx context.Context, q Querier, tenant string, rows []OutboxRow,
) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, row := range rows {
		batch.Queue(
			`INSERT INTO outbox (tenant, id, payload, round_time)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (id) DO NOTHING`,
			tenant, row.ID, row.Payload, row.RoundTime,
		)
	}
	if err := q.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("enqueueing heartbeats of tenant %s: %w", tenant, err)
	}
	return nil
}

// NextOutbox returns the tenant's unsent heartbeats in order.
func NextOutbox(ctx context.Context, q Querier, tenant string, limit int) ([]OutboxRow, error) {
	rows, err := q.Query(
		ctx,
		`SELECT seq, id, payload, round_time FROM outbox
		 WHERE tenant = $1 ORDER BY seq LIMIT $2`,
		tenant, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("reading outbox of tenant %s: %w", tenant, err)
	}
	defer rows.Close()
	var result []OutboxRow
	for rows.Next() {
		var row OutboxRow
		if err := rows.Scan(&row.Seq, &row.ID, &row.Payload, &row.RoundTime); err != nil {
			return nil, fmt.Errorf("scanning outbox row of tenant %s: %w", tenant, err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// DeleteOutbox removes sent heartbeats from the outbox.
func DeleteOutbox(ctx context.Context, q Querier, tenant string, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	_, err := q.Exec(
		ctx, `DELETE FROM outbox WHERE tenant = $1 AND seq = ANY($2)`, tenant, seqs,
	)
	if err != nil {
		return fmt.Errorf("deleting sent heartbeats of tenant %s: %w", tenant, err)
	}
	return nil
}

// RecordRuleRevision remembers a revision metering has used.
func (s *Store) RecordRuleRevision(ctx context.Context, row RuleRevisionRow) error {
	_, err := s.pool.Exec(
		ctx,
		`INSERT INTO rule_revisions (revision, effective_at, meters_yaml)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (revision) DO NOTHING`,
		row.Revision, row.EffectiveAt, row.MetersYAML,
	)
	if err != nil {
		return fmt.Errorf("recording rule revision %s: %w", row.Revision, err)
	}
	return nil
}

// ListRuleRevisions returns the revisions metering has used.
func (s *Store) ListRuleRevisions(ctx context.Context) ([]RuleRevisionRow, error) {
	rows, err := s.pool.Query(
		ctx, `SELECT revision, effective_at, meters_yaml FROM rule_revisions ORDER BY revision`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing rule revisions: %w", err)
	}
	defer rows.Close()
	var result []RuleRevisionRow
	for rows.Next() {
		var row RuleRevisionRow
		if err := rows.Scan(&row.Revision, &row.EffectiveAt, &row.MetersYAML); err != nil {
			return nil, fmt.Errorf("scanning rule revision: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// DeleteTenantRows removes every row of the tenant. Metering does this when the tenant's topics
// are deleted after its final invoice. The deletes run in one transaction: a failure part-way
// must not leave the tenant with rows in some tables and none in the others.
func (s *Store) DeleteTenantRows(ctx context.Context, tenant string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("deleting tenant %s: %w", tenant, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tables := []string{"history", "ledger", "outbox", "progress", "replayed_heartbeats"}
	for _, table := range tables {
		if _, err := tx.Exec(
			ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant = $1", table), tenant,
		); err != nil {
			return fmt.Errorf("deleting tenant %s from %s: %w", tenant, table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("deleting tenant %s: %w", tenant, err)
	}
	return nil
}

// ResourcesOwnedBy returns the tenant's resources whose owner fact names the given root. The
// round uses it to recompute a changed root's workers in the same round.
func ResourcesOwnedBy(
	ctx context.Context, q Querier, tenant, field, rootID string,
) ([]LedgerKey, error) {
	rows, err := q.Query(
		ctx,
		`SELECT DISTINCT resource_type, resource_id
		 FROM history
		 WHERE tenant = $1 AND field = $2 AND value = $3`,
		tenant, field, rootID,
	)
	if err != nil {
		return nil, fmt.Errorf("finding resources owned by %s: %w", rootID, err)
	}
	defer rows.Close()
	var resources []LedgerKey
	for rows.Next() {
		var key LedgerKey
		if err := rows.Scan(&key.ResourceType, &key.ResourceID); err != nil {
			return nil, fmt.Errorf("scanning resource owned by %s: %w", rootID, err)
		}
		resources = append(resources, key)
	}
	return resources, rows.Err()
}
