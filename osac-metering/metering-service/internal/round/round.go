/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package round runs the per-tenant heartbeat rounds. A round catches up, computes expected
// usage from history and rules, compares it with the ledger, and writes the difference,
// positive or negative, to the outbox in the same transaction as the ledger update.
package round

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/metrics"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
)

// CatchUpBatch bounds how many heartbeats one catch-up pass reads, so a long backlog applies
// in steps.
const CatchUpBatch = 500

// ClockSkewLimit is how far a pod's clock may differ from PostgreSQL's before rounds are
// skipped.
const ClockSkewLimit = 5 * time.Minute

// clockSkewTooLarge reports whether the pod clock and the database clock differ by more
// than ClockSkewLimit. A round run at a stale time computes wrong usage and sends wrong
// corrections, so it skips instead.
func clockSkewTooLarge(pod, database time.Time) bool {
	delta := pod.Sub(database)
	return delta > ClockSkewLimit || -delta > ClockSkewLimit
}

// roundAlreadyRan reports whether the database clock has not passed the last round's time.
// PostgreSQL's clock moved backwards, and the round waits for it rather than compute at a
// time the last round already covered.
func roundAlreadyRan(roundTime time.Time, lastRound *time.Time) bool {
	return lastRound != nil && !roundTime.After(*lastRound)
}

// Calculator is the subset of the usage calculator a round uses. The service hands the
// round a swappable implementation, so approving a new rule revision reloads the rules
// without restarting the tenant loops: each tick holds the read side of the rules gate
// for its whole run, so it reads one set end to end, and a revision change holds the
// write side and can only land between ticks.
type Calculator interface {
	Revisions() []rules.Revision
	OwnerOf(resourceType string, facts []database.Fact) (string, string, bool)
	OwnedFieldOf(rootType string) (string, bool)
	MeterAt(resourceType, meter string, at time.Time) (rules.Meter, string, bool)
	Expected(
		tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root, upTo time.Time,
	) ([]usage.Piece, error)
	Since(
		tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root,
		from, upTo time.Time, ledger []database.LedgerPiece,
	) ([]usage.Piece, error)
}

// Round runs a tenant's heartbeat rounds.
type Round struct {
	store      *database.Store
	calculator Calculator
	client     sarama.Client
	consumer   sarama.Consumer
	logger     logr.Logger

	// rulesGate is held for its read side by every tick, so a rule-revision change
	// (which holds the write side) can neither swap the calculator between one
	// tick's calculator calls nor be decided against a position a tick is about
	// to advance.
	rulesGate *sync.RWMutex

	// eventTarget is the event-topic watermark the round waits for the fold to reach,
	// per tenant. Measured at check time the watermark always moves ahead of the fold
	// on a tenant with a steady event stream, and the tenant would never bill.
	eventTarget map[string]int64

	// catchUpBatch bounds one catch-up pass; tests shrink it.
	catchUpBatch int

	// crashAfterGroups, when nonzero, stops catch-up with ErrCrashed after that many round
	// groups have committed, as a pod kill would. For tests only.
	crashAfterGroups int
}

// New creates a round runner. The client reads topic watermarks; the consumer reads the
// heartbeat topic during catch-up. The rulesGate's read side brackets every tick; the
// service's rules check holds the write side.
func New(
	store *database.Store,
	calculator Calculator,
	client sarama.Client,
	consumer sarama.Consumer,
	logger logr.Logger,
	rulesGate *sync.RWMutex,
) (*Round, error) {
	return &Round{
		store: store, calculator: calculator, client: client, consumer: consumer, logger: logger,
		rulesGate: rulesGate, eventTarget: map[string]int64{}, catchUpBatch: CatchUpBatch,
	}, nil
}

// SetCatchUpBatch shrinks the catch-up bound. For tests only.
func (r *Round) SetCatchUpBatch(size int) {
	r.catchUpBatch = size
}

// SetCrashAfterGroups makes catch-up stop with ErrCrashed after the given number of round
// groups have committed, the way a pod kill stops a pass mid-catch-up: the groups that
// committed stay committed. For tests only.
func (r *Round) SetCrashAfterGroups(groups int) {
	r.crashAfterGroups = groups
}

// RunTenant runs the tenant's rounds until the context ends.
func (r *Round) RunTenant(ctx context.Context, tenant string, interval time.Duration) error {
	if err := r.store.EnsureProgress(ctx, tenant); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// The round time comes from PostgreSQL's clock, so all pods agree on it.
			// A pod whose clock is too far from the database's skips its round rather
			// than compute at a time the database cannot confirm. Truncated to whole
			// seconds: every fact boundary is second-granular, and finer round times
			// would only put fractional digits on interval ends.
			roundTime, err := r.store.Clock(ctx)
			if err != nil {
				r.logger.Error(err, "reading the round clock", "tenant", tenant)
				continue
			}
			roundTime = roundTime.Truncate(time.Second)
			if clockSkewTooLarge(time.Now(), roundTime) {
				r.logger.Error(
					nil, "pod clock is too far from the database clock; skipping round",
					"tenant", tenant, "delta", time.Since(roundTime).String(),
				)
				// Catch-up replays committed heartbeats and needs no trustworthy
				// clock, so a skewed pod still repairs its ledger; only the
				// comparison waits for the clock.
				if _, err := r.catchUp(ctx, tenant); err != nil {
					r.logger.Error(err, "catch-up failed", "tenant", tenant)
				}
				continue
			}
			if err := r.Once(ctx, tenant, roundTime); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Error(err, "round failed", "tenant", tenant)
				metrics.RoundFailed(tenant)
				continue
			}
			// A tick without an error keeps the loop's liveness current, including
			// the ticks that deliberately did nothing: a tenant whose gauge ages is
			// a tenant whose billing stopped while the pod stays ready.
			metrics.RoundSucceeded(tenant, roundTime)
		}
	}
}

// Once runs one round at roundTime. Catch-up runs first, one round group per committed
// transaction, so a crash mid-catch-up leaves the groups that committed applied and the
// next pass resumes at the next group. The comparison runs only when catch-up reached the
// topic's end in one pass; it skips when the fold is behind or when roundTime has not
// passed the last round time. RunTenant reads roundTime from PostgreSQL's clock.
func (r *Round) Once(ctx context.Context, tenant string, roundTime time.Time) error {
	// The tick holds the read side of the rules gate for its whole run, so the
	// calculator cannot change between the tick's calls.
	r.rulesGate.RLock()
	defer r.rulesGate.RUnlock()

	if err := r.store.EnsureProgress(ctx, tenant); err != nil {
		return err
	}
	finished, err := r.catchUp(ctx, tenant)
	if err != nil {
		return err
	}
	if !finished {
		// The offsets and last round moved with the groups catch-up committed; the
		// comparison waits for a pass that reaches the topic's end.
		return nil
	}

	tx, err := r.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := database.TakeTenantLock(ctx, tx, database.LockRound, tenant); err != nil {
		return err
	}

	progress, err := database.GetProgress(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if roundAlreadyRan(roundTime, progress.LastRoundTime) {
		// PostgreSQL's clock moved backwards; wait until it passes the last round.
		return nil
	}

	// A round waits until the tenant's events are all folded, so it never computes from
	// half-folded facts.
	folded, err := r.eventsFolded(ctx, tenant, progress.EventOffset)
	if err != nil {
		return err
	}
	if !folded {
		return nil
	}

	if err := r.compare(ctx, tx, tenant, progress, roundTime); err != nil {
		return err
	}
	if err := database.SetLastRound(ctx, tx, tenant, roundTime, progress.EventOffset); err != nil {
		return err
	}
	// The round's own commit supersedes the replay's id memory: every heartbeat on
	// the topic is now from a round at or before the last round, which the replay
	// skips on its own.
	if err := database.PruneReplayedHeartbeats(ctx, tx, tenant); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// eventsFolded reports whether the fold has consumed the event topic up to the round's
// target. The target is the watermark captured when the round started waiting: measured at
// check time it would always move ahead of the fold on a tenant with a steady event stream,
// and the tenant would never bill. Once the fold reaches the target the round proceeds, even
// though newer events exist; the next round captures a fresh target.
func (r *Round) eventsFolded(ctx context.Context, tenant string, offset int64) (bool, error) {
	high, err := r.client.GetOffset(topics.Events(tenant), 0, sarama.OffsetNewest)
	if err != nil {
		return false, fmt.Errorf("reading the event topic end: %w", err)
	}
	if high <= 0 {
		return true, nil
	}
	target, waiting := r.eventTarget[tenant]
	if !waiting || high < target {
		// No target yet, or the topic moved backwards after it was recreated: capture
		// the watermark this round waits for.
		r.eventTarget[tenant] = high
		if offset >= high {
			// The fold is already at the watermark this round captured: proceed, and
			// drop the target so the next round captures a fresh one instead of
			// comparing against a one-round-stale watermark.
			delete(r.eventTarget, tenant)
			return true, nil
		}
		return false, nil
	}
	if offset >= target {
		// The fold reached the round's target: proceed and capture fresh next round.
		delete(r.eventTarget, tenant)
		return true, nil
	}
	return false, nil
}

// resource identifies one billable resource.
type resource struct {
	resourceType string
	resourceID   string
}

// rootOf resolves and loads the resource's ownership root, following owner references to the
// topmost owner: the gate every resource below it inherits. A standalone resource returns
// nil. A root whose history is missing returns a root with empty facts: the calculator then
// holds the resource's billing shut, and the miss is logged here, because a worker billed as
// standalone would ignore the gate its cluster controls.
func (r *Round) rootOf(
	ctx context.Context, tx database.Querier, tenant, resourceType string,
	facts []database.Fact,
) (*usage.Root, error) {
	originalType, originalID := resourceType, ""
	if len(facts) > 0 {
		originalID = facts[0].ResourceID
	}
	var root *usage.Root
	for range ownerChainLimit {
		ownerType, ownerID, ok := r.calculator.OwnerOf(resourceType, facts)
		if !ok {
			return root, nil
		}
		ownerFacts, err := database.ResourceFacts(ctx, tx, tenant, ownerType, ownerID)
		if err != nil {
			return nil, err
		}
		if len(ownerFacts) == 0 {
			r.logger.Error(
				nil, "ownership root has no history; holding its resources' billing",
				"tenant", tenant, "root_type", ownerType, "root_id", ownerID,
				"resource_type", originalType, "resource_id", originalID,
			)
			return &usage.Root{Type: ownerType, ID: ownerID}, nil
		}
		root = &usage.Root{Type: ownerType, ID: ownerID, Facts: ownerFacts}
		resourceType, facts = ownerType, ownerFacts
	}
	return nil, fmt.Errorf(
		"ownership chain of %s %s exceeds %d hops; billing held",
		originalType, originalID, ownerChainLimit,
	)
}

// ownerChainLimit bounds the ownership walk. Ownership is set at creation by the owner, so a
// cycle cannot occur; the limit turns corrupt data into a loud failure instead of a loop.
const ownerChainLimit = 8

// compare recomputes the meters that can have changed and writes the differences. Resources
// with new facts or a newly effective revision are recomputed from history; meters billable at
// the previous round only extend their ledger tail.
func (r *Round) compare(
	ctx context.Context,
	tx database.Querier,
	tenant string,
	progress database.Progress,
	roundTime time.Time,
) error {
	full, tail, err := r.scanResources(ctx, tx, tenant, progress, roundTime)
	if err != nil {
		return err
	}
	for _, resource := range full {
		facts, root, ledger, err := r.resourceSnapshot(ctx, tx, tenant, resource)
		if err != nil {
			return err
		}
		if err := r.recomputeWhole(
			ctx, tx, tenant, progress, roundTime, resource, facts, root, ledger,
		); err != nil {
			return err
		}
	}
	for _, resource := range tail {
		facts, root, ledger, err := r.resourceSnapshot(ctx, tx, tenant, resource)
		if err != nil {
			return err
		}
		pieces, err := r.calculator.Since(
			tenant, resource.resourceType, resource.resourceID, facts, root,
			*progress.LastRoundTime, roundTime, ledger,
		)
		if err != nil {
			return fmt.Errorf(
				"extending usage of %s %s: %w", resource.resourceType, resource.resourceID, err,
			)
		}
		if len(pieces) == 0 || !openKeysJustified(pieces, ledger, *progress.LastRoundTime) {
			// The calculator cannot justify every meter that was billable at the previous
			// round, for example a ledger rebuilt by catch-up from heartbeats whose
			// events were never folded. The resource's rows are compared whole, so
			// unjustified usage is reversed.
			if err := r.recomputeWhole(
				ctx, tx, tenant, progress, roundTime, resource, facts, root, ledger,
			); err != nil {
				return err
			}
			continue
		}
		// Only the keys the engine produced take part: a key with no new pieces and no new
		// facts holds exactly what the previous round wrote.
		if err := r.diffAndCommit(
			ctx, tx, tenant, progress, roundTime, pieces, ledger, false, *progress.LastRoundTime,
		); err != nil {
			return err
		}
	}
	return nil
}

// resourceSnapshot loads one resource's facts, ownership root, and current ledger
// rows: the inputs both comparison loops share.
func (r *Round) resourceSnapshot(
	ctx context.Context, tx database.Querier, tenant string, res resource,
) (facts []database.Fact, root *usage.Root, ledger []database.LedgerPiece, err error) {
	facts, err = database.ResourceFacts(ctx, tx, tenant, res.resourceType, res.resourceID)
	if err != nil {
		return nil, nil, nil, err
	}
	root, err = r.rootOf(ctx, tx, tenant, res.resourceType, facts)
	if err != nil {
		return nil, nil, nil, err
	}
	ledger, err = database.LedgerPiecesWhere(
		ctx, tx, tenant, "resource_type = $2 AND resource_id = $3",
		res.resourceType, res.resourceID,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return facts, root, ledger, nil
}

// recomputeWhole compares one resource's entire history against its ledger. The full
// loop runs it for every changed resource; the tail loop falls back to it when the
// calculator cannot justify the meters the previous round left open.
func (r *Round) recomputeWhole(
	ctx context.Context, tx database.Querier, tenant string,
	progress database.Progress, roundTime time.Time,
	res resource, facts []database.Fact, root *usage.Root, ledger []database.LedgerPiece,
) error {
	pieces, err := r.calculator.Expected(
		tenant, res.resourceType, res.resourceID, facts, root, roundTime,
	)
	if err != nil {
		return fmt.Errorf("computing usage of %s %s: %w", res.resourceType, res.resourceID, err)
	}
	// Every key with a ledger row takes part: a late event can remove a meter entirely.
	return r.diffAndCommit(ctx, tx, tenant, progress, roundTime, pieces, ledger, true, time.Time{})
}

// scanResources returns the resources whose meters the round must recompute from history, and
// those whose ledger tail it only extends. A recompute is due for resources with new facts and
// for resources of types a newly effective revision affects; a tail extension is due for
// meters billable at the previous round, whose ledger rows end at its time. The first round
// recomputes everything the database holds, facts and ledger rows alike, so a ledger
// rebuilt from heartbeats is compared even for a resource whose events are gone, and a
// round's work stays proportional to the heartbeats it sends.
func (r *Round) scanResources(
	ctx context.Context, tx database.Querier, tenant string,
	progress database.Progress, roundTime time.Time,
) (full []resource, tail []resource, err error) {
	if progress.LastRoundTime == nil {
		keys, err := database.AllTenantResources(ctx, tx, tenant)
		if err != nil {
			return nil, nil, err
		}
		var resources []resource
		for _, key := range keys {
			resources = append(resources, resource{key.ResourceType, key.ResourceID})
		}
		return resources, nil, nil
	}

	seen := map[resource]bool{}
	add := func(entry resource, to *[]resource) {
		if !seen[entry] {
			seen[entry] = true
			*to = append(*to, entry)
		}
	}

	// Resources with facts the previous round has not used: written after its event
	// offset, or dated after its time, because a writer's clock can run ahead.
	newFacts, err := database.ResourcesWithNewFacts(
		ctx, tx, tenant, progress.LastRoundEventOffset, *progress.LastRoundTime,
	)
	if err != nil {
		return nil, nil, err
	}

	// Resources of types a new revision affects, in the round where the revision takes
	// effect.
	affected := map[string]bool{}
	for _, revision := range r.calculator.Revisions() {
		if revision.EffectiveAt.After(*progress.LastRoundTime) &&
			!revision.EffectiveAt.After(roundTime) {
			for _, meter := range revision.Meters {
				affected[meter.Resource] = true
			}
		}
	}
	var affectedResources []resource
	if len(affected) > 0 {
		affectedResources, err = tenantResources(ctx, tx, tenant, affected)
		if err != nil {
			return nil, nil, err
		}
	}

	// The drag queue: every resource above, plus the resources a changed root owns,
	// transitively, because the root's facts gate their billing. A worker whose cluster
	// reached a confirming state, or whose cluster's meters a new revision redefined,
	// recomputes in the same round, retroactively, without a fact of its own.
	queue := make([]resource, 0, len(newFacts)+len(affectedResources))
	for _, key := range newFacts {
		queue = append(queue, resource{key.ResourceType, key.ResourceID})
	}
	queue = append(queue, affectedResources...)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		add(key, &full)
		field, known := r.calculator.OwnedFieldOf(key.resourceType)
		if !known {
			continue
		}
		owned, err := database.ResourcesOwnedBy(ctx, tx, tenant, field, key.resourceID)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range owned {
			if !seen[resource{entry.ResourceType, entry.ResourceID}] {
				queue = append(queue, resource{entry.ResourceType, entry.ResourceID})
			}
		}
	}

	// Meters billable at the previous round: their ledger rows end at its time, and only
	// the latest interval moves.
	open, err := database.LedgerPiecesWhere(
		ctx, tx, tenant, "end_time = $2", *progress.LastRoundTime,
	)
	if err != nil {
		return nil, nil, err
	}
	for _, piece := range open {
		add(resource{piece.Key.ResourceType, piece.Key.ResourceID}, &tail)
	}
	return full, tail, nil
}

func tenantResources(
	ctx context.Context, tx database.Querier, tenant string, types map[string]bool,
) ([]resource, error) {
	keys, err := database.TenantResources(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	var result []resource
	for _, key := range keys {
		if types != nil && !types[key.ResourceType] {
			continue
		}
		result = append(result, resource{key.ResourceType, key.ResourceID})
	}
	return result, nil
}

// openKeysJustified reports whether the pieces cover every meter whose ledger row was open at
// the previous round. A meter billable at the previous round always has a piece that reaches
// past it, so a missing one means the engine cannot justify the row and the resource must be
// compared whole.
func openKeysJustified(
	pieces []usage.Piece, ledger []database.LedgerPiece, lastRound time.Time,
) bool {
	justified := make(map[database.LedgerKey]bool, len(pieces))
	for _, piece := range pieces {
		justified[piece.Key] = true
	}
	for _, row := range ledger {
		if row.EndTime.Equal(lastRound) && !justified[row.Key] {
			return false
		}
	}
	return true
}

// diffAndCommit compares one resource's expected pieces with its ledger, writes the
// differences to the outbox, and syncs the ledger, all in the round's transaction. In the
// full path every key with a ledger row takes part, because a late event can remove a meter
// entirely; in the tail path only the keys the engine produced do, because the rest hold
// exactly what the previous round wrote. tailFrom is zero in the full path, which owns every
// row of the meter; in the tail path it is the previous round's time, before which the
// differences are not compared and the rows are not deleted.
func (r *Round) diffAndCommit(
	ctx context.Context,
	tx database.Querier,
	tenant string,
	progress database.Progress,
	roundTime time.Time,
	expected []usage.Piece,
	ledger []database.LedgerPiece,
	includeLedgerKeys bool,
	tailFrom time.Time,
) error {
	keys := map[database.LedgerKey]bool{}
	for _, piece := range expected {
		keys[piece.Key] = true
	}
	if includeLedgerKeys {
		for _, piece := range ledger {
			keys[piece.Key] = true
		}
	}
	// Sorted, so the heartbeats of one round reach the outbox in a deterministic order.
	ordered := make([]database.LedgerKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.Meter != right.Meter {
			return left.Meter < right.Meter
		}
		if left.Component != right.Component {
			return left.Component < right.Component
		}
		return left.Dimensions < right.Dimensions
	})

	var revisionTimes []time.Time
	for _, revision := range r.calculator.Revisions() {
		revisionTimes = append(revisionTimes, revision.EffectiveAt)
	}

	for _, key := range ordered {
		differences, err := diff(
			key, expectedPiecesOf(expected, key), ledgerPiecesOf(ledger, key), revisionTimes,
			tailFrom,
		)
		if err != nil {
			return fmt.Errorf(
				"comparing meter %s of %s %s: %w", key.Meter, key.ResourceType, key.ResourceID, err,
			)
		}
		rows := make([]database.OutboxRow, 0, len(differences))
		for _, difference := range differences {
			// The unit and revision come from the meter definition and the revision that
			// billed the pieces, not from the expected pieces: a correction for a meter
			// with no expected usage at all still carries them.
			meter, revision, ok := r.calculator.MeterAt(key.ResourceType, key.Meter, difference.start)
			if !ok {
				return fmt.Errorf(
					"no revision defines meter %s of %s, which has ledger rows",
					key.Meter, key.ResourceType,
				)
			}
			event, err := heartbeat.New(
				tenant, key, difference.start, difference.end, difference.quantityMicro,
				meter.Unit, roundTime, progress.EventOffset, revision,
			)
			if err != nil {
				return err
			}
			payload, err := event.MarshalJSON()
			if err != nil {
				return fmt.Errorf("marshaling heartbeat: %w", err)
			}
			rows = append(rows, database.OutboxRow{
				ID: event.ID(), Payload: payload, RoundTime: roundTime,
			})
		}
		if err := database.EnqueueHeartbeats(ctx, tx, tenant, rows); err != nil {
			return err
		}
		rowsForLedger, err := expectedRows(expected, key)
		if err != nil {
			return err
		}
		if err := database.SyncLedger(ctx, tx, tenant, key, rowsForLedger, tailFrom); err != nil {
			return err
		}
	}
	return nil
}

func expectedPiecesOf(pieces []usage.Piece, key database.LedgerKey) []usage.Piece {
	var result []usage.Piece
	for _, piece := range pieces {
		if piece.Key == key {
			result = append(result, piece)
		}
	}
	return result
}

func ledgerPiecesOf(pieces []database.LedgerPiece, key database.LedgerKey) []database.LedgerPiece {
	var result []database.LedgerPiece
	for _, piece := range pieces {
		if piece.Key == key {
			result = append(result, piece)
		}
	}
	return result
}

func expectedRows(pieces []usage.Piece, key database.LedgerKey) ([]database.LedgerPiece, error) {
	var rows []database.LedgerPiece
	for _, piece := range pieces {
		if piece.Key != key {
			continue
		}
		quantity, err := piece.QuantityMicro()
		if err != nil {
			return nil, err
		}
		rows = append(rows, database.LedgerPiece{
			Key:           piece.Key,
			StartTime:     piece.StartTime,
			EndTime:       piece.EndTime,
			QuantityMicro: quantity,
		})
	}
	return rows, nil
}

// difference is one heartbeat to send: a quantity over an interval.
type difference struct {
	key           database.LedgerKey
	start         time.Time
	end           time.Time
	quantityMicro int64
	deltaRate     *big.Rat
}

// diff computes the difference between the expected pieces and the ledger rows of one meter.
// Both sides are rate functions; a heartbeat is emitted wherever the rates differ. Every
// interval is split at UTC midnights and at the given revision boundaries, so a heartbeat
// never crosses a billing day or a rules revision: the billing provider dates each heartbeat
// by a single timestamp, and a correction must carry the revision that billed the pieces it
// corrects. Intervals that end at or before clamp are skipped unless an expected piece
// overlaps them: a tail extension carries only a tail of the meter's pieces, so what lies
// before the clamp is exactly what the previous round wrote, except where a kept piece
// reaches back, for example a pending run that crosses the billable threshold.
func diff(
	key database.LedgerKey, expected []usage.Piece, actual []database.LedgerPiece,
	revisionTimes []time.Time, clamp time.Time,
) ([]difference, error) {
	points := map[time.Time]bool{}
	for _, piece := range expected {
		points[piece.StartTime] = true
		points[piece.EndTime] = true
	}
	for _, piece := range actual {
		points[piece.StartTime] = true
		points[piece.EndTime] = true
	}
	if len(points) == 0 {
		return nil, nil
	}
	sorted := make([]time.Time, 0, len(points))
	for point := range points {
		sorted = append(sorted, point)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	// Split points the pieces themselves may not carry, for example a ledger rebuilt by
	// catch-up from heartbeats an older release merged across a boundary.
	lo, hi := sorted[0], sorted[len(sorted)-1]
	splits := map[time.Time]bool{}
	for midnight := usage.NextMidnight(lo); midnight.Before(hi); midnight = midnight.AddDate(0, 0, 1) {
		points[midnight] = true
		splits[midnight] = true
	}
	for _, at := range revisionTimes {
		if at.After(lo) && at.Before(hi) {
			points[at] = true
			splits[at] = true
		}
	}
	if len(points) != len(sorted) {
		sorted = sorted[:0]
		for point := range points {
			sorted = append(sorted, point)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	}

	var differences []difference
	for i := 1; i < len(sorted); i++ {
		start, end := sorted[i-1], sorted[i]
		if !start.Before(end) {
			continue
		}
		if !end.After(clamp) && !pieceOverlaps(expected, start, end) {
			continue
		}
		// Rates are micro units per microsecond. Equal rates need no correction, and the
		// rate difference decides whether adjacent intervals merge into one heartbeat.
		delta := new(big.Rat).Sub(rateAt(expected, start), actualRateAt(actual, start))
		if delta.Sign() == 0 {
			continue
		}
		// The quantity is what the ledger would commit over the interval minus what it
		// committed. Both sides round once, cumulatively from the covering piece's and
		// row's start, exactly the way expectedRows commits a piece: rounding each
		// interval on its own would not conserve, and the sent total would disagree with
		// the stored one under a fractional rate.
		expectedQty, err := roundedQuantity(expected, start, end)
		if err != nil {
			return nil, err
		}
		committedQty, err := committedQuantity(actual, start, end)
		if err != nil {
			return nil, err
		}
		micro := expectedQty - committedQty
		if micro == 0 {
			continue
		}
		// Adjacent intervals with the same difference are one piece of usage, but never
		// across a UTC midnight or a revision boundary: the provider dates a heartbeat
		// by a single day, and a correction carries the revision that billed it.
		if last := len(differences) - 1; last >= 0 &&
			differences[last].end.Equal(start) && differences[last].rate().Cmp(delta) == 0 &&
			sameBillingDay(differences[last].start, start) && !splits[start] {
			differences[last].end = end
			differences[last].quantityMicro += micro
			continue
		}
		differences = append(differences, difference{
			key: key, start: start, end: end, quantityMicro: micro, deltaRate: delta,
		})
	}
	return differences, nil
}

// sameBillingDay reports whether two moments fall on the same UTC day.
func sameBillingDay(a, b time.Time) bool {
	a, b = a.UTC(), b.UTC()
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// pieceOverlaps reports whether any expected piece covers part of the interval.
func pieceOverlaps(pieces []usage.Piece, start, end time.Time) bool {
	for _, piece := range pieces {
		if piece.StartTime.Before(end) && piece.EndTime.After(start) {
			return true
		}
	}
	return false
}

// rate returns the difference rate the piece was built from.
func (d difference) rate() *big.Rat { return d.deltaRate }

// rateAt returns the expected rate at a moment, in micro units per microsecond. A pending
// piece commits nothing.
func rateAt(pieces []usage.Piece, moment time.Time) *big.Rat {
	for _, piece := range pieces {
		if !moment.Before(piece.StartTime) && moment.Before(piece.EndTime) {
			if piece.Pending {
				return new(big.Rat)
			}
			return big.NewRat(piece.RateMicro, 1_000_000)
		}
	}
	return new(big.Rat)
}

// roundedQuantity returns the quantity the expected pieces commit over [start, end]: the
// covering piece's rate applied from the piece's own start and rounded once, differenced
// between the two moments. expectedRows commits round(rate x piece length) the same way, so
// a heartbeat computed here can never disagree with the row it lands next to.
func roundedQuantity(pieces []usage.Piece, start, end time.Time) (int64, error) {
	for _, piece := range pieces {
		// Diff intervals never cross a piece boundary, so the piece covering start also
		// covers end.
		if piece.StartTime.After(start) || !start.Before(piece.EndTime) {
			continue
		}
		if piece.Pending {
			return 0, nil
		}
		rate := big.NewRat(piece.RateMicro, 1_000_000)
		from := new(big.Rat).Mul(rate, big.NewRat(start.Sub(piece.StartTime).Microseconds(), 1))
		to := new(big.Rat).Mul(rate, big.NewRat(end.Sub(piece.StartTime).Microseconds(), 1))
		fromMicro, err := usage.RationalToMicro(from)
		if err != nil {
			return 0, err
		}
		toMicro, err := usage.RationalToMicro(to)
		if err != nil {
			return 0, err
		}
		return toMicro - fromMicro, nil
	}
	return 0, nil
}

// committedQuantity returns the quantity the ledger rows committed over [start, end]: the
// covering row's rate applied from the row's own start and rounded once, differenced between
// the two moments. Over a row's whole extent this equals the row's committed quantity
// exactly, so corrections computed against it conserve what was sent.
func committedQuantity(rows []database.LedgerPiece, start, end time.Time) (int64, error) {
	for _, row := range rows {
		// Diff intervals never cross a row boundary, so the row covering start also
		// covers end.
		if row.StartTime.After(start) || !start.Before(row.EndTime) {
			continue
		}
		duration := row.EndTime.Sub(row.StartTime).Microseconds()
		if duration <= 0 {
			return 0, nil
		}
		rate := new(big.Rat).SetFrac(big.NewInt(row.QuantityMicro), big.NewInt(duration))
		from := new(big.Rat).Mul(rate, big.NewRat(start.Sub(row.StartTime).Microseconds(), 1))
		to := new(big.Rat).Mul(rate, big.NewRat(end.Sub(row.StartTime).Microseconds(), 1))
		fromMicro, err := usage.RationalToMicro(from)
		if err != nil {
			return 0, err
		}
		toMicro, err := usage.RationalToMicro(to)
		if err != nil {
			return 0, err
		}
		return toMicro - fromMicro, nil
	}
	return 0, nil
}

// actualRateAt returns the ledger's committed rate at a moment, in micro units per
// microsecond.
func actualRateAt(pieces []database.LedgerPiece, moment time.Time) *big.Rat {
	for _, piece := range pieces {
		if !moment.Before(piece.StartTime) && moment.Before(piece.EndTime) {
			duration := piece.EndTime.Sub(piece.StartTime).Microseconds()
			if duration <= 0 {
				continue
			}
			return new(big.Rat).SetFrac(big.NewInt(piece.QuantityMicro), big.NewInt(duration))
		}
	}
	return new(big.Rat)
}
