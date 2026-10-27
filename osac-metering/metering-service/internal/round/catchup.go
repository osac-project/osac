/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
	"github.com/osac-project/osac-metering/schema"
)

// caughtHeartbeat is one heartbeat read from the heartbeat topic during catch-up.
type caughtHeartbeat struct {
	id            string
	offset        int64
	roundTime     time.Time
	key           database.LedgerKey
	start, end    time.Time
	quantityMicro int64
}

// ErrCrashed is the simulated pod kill SetCrashAfterGroups triggers mid-catch-up: the round
// groups that already committed stay committed and the rest of the pass never runs.
var ErrCrashed = errors.New("round crashed mid-catch-up")

// catchUp reads the heartbeat topic from the tenant's stored heartbeat offset and adds each
// heartbeat's quantity to the ledger, so a database restored from a backup re-sends nothing
// and an empty database replays everything from offset 0. Replay rebuilds the ledger only:
// the topic holds what was sent, not what a round computed, so the comparison's position
// stays what the database itself says, and the first comparison after recovery recomputes
// the gap, emitting whatever replay could not rebuild. Every round group applies and moves
// the heartbeat offset past itself in one committed transaction, so a crash mid-catch-up
// resumes at the next group instead of repeating the identical pass. It returns false when it
// hit its batch bound, meaning there is more to read and the round must skip its comparison.
func (r *Round) catchUp(ctx context.Context, tenant string) (bool, error) {
	progress, err := r.store.GetTenantProgress(ctx, tenant)
	if err != nil {
		return false, err
	}
	known, err := database.ReplayedHeartbeatIDs(ctx, r.store.Pool(), tenant)
	if err != nil {
		return false, err
	}
	caught, finished, err := r.readHeartbeats(ctx, tenant, progress.HeartbeatOffset)
	if err != nil {
		return false, err
	}
	committed := 0
	for first := 0; first < len(caught); {
		last := groupEnd(caught, first)
		if err := r.applyGroup(ctx, tenant, known, caught[first:last]); err != nil {
			return false, err
		}
		committed++
		first = last
		if r.crashAfterGroups > 0 && committed >= r.crashAfterGroups && first < len(caught) {
			// The simulated pod kill: what committed stays committed.
			return false, ErrCrashed
		}
	}
	if !finished && committed == 0 {
		// The read collected records but every group was truncated by the idle
		// timeout, so nothing was applied and the offset did not move. The next
		// pass would read the identical records and truncate them again, silently
		// stopping the tenant's billing. Refuse instead.
		return false, fmt.Errorf(
			"catch-up of %s read records above the stored offset %d but applied "+
				"no complete round group; the tenant cannot progress",
			tenant, progress.HeartbeatOffset,
		)
	}
	return finished, nil
}

// applyGroup applies one round group of heartbeats in a transaction that also moves the
// heartbeat offset past the group and records the applied ids: the ledger, the offset,
// and the id memory advance together or not at all, so a crash mid-catch-up resumes at
// the next group, and a database restored to any earlier snapshot replays the same
// groups to the same ledger, because the merge is a pure function of the ledger it
// reads and the heartbeat's interval and quantity.
//
// The group's starting round comes from the stored last round, read fresh under the
// lock, and catch-up never moves it. A heartbeat whose round time is not after it was
// computed by this database's own comparison and is already in the ledger, and so is
// every copy of it. A heartbeat after it came from a database this one is recovering
// from: it is applied, and its id joins the memory, so a copy the relay resent, in a
// later group or a later pass after a crash, counts once. Read records move the offset
// either way: without that, a resend after a crash would wedge the stored offset behind
// them forever.
func (r *Round) applyGroup(
	ctx context.Context, tenant string, known map[string]bool, group []caughtHeartbeat,
) error {
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
	start := time.Time{}
	if progress.LastRoundTime != nil {
		start = *progress.LastRoundTime
	}

	var applied []string
	last := progress.HeartbeatOffset
	for index := range group {
		entry := &group[index]
		if entry.offset < progress.HeartbeatOffset {
			continue
		}
		last = entry.offset + 1
		if !entry.roundTime.After(start) || known[entry.id] {
			continue
		}
		known[entry.id] = true
		if err := r.applyHeartbeat(ctx, tx, tenant, entry); err != nil {
			return err
		}
		applied = append(applied, entry.id)
	}
	if last > progress.HeartbeatOffset {
		if err := database.AdvanceHeartbeatOffset(ctx, tx, tenant, last); err != nil {
			return err
		}
	}
	if err := database.RecordReplayedHeartbeats(ctx, tx, tenant, applied); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// readHeartbeats reads heartbeats from the tenant's heartbeat topic and returns only whole
// round groups: a group split at the end of the read stays unread, so no round is
// half-applied. The batch bounds how many groups one pass reads, so a long backlog applies
// in steps; a group larger than the batch is still read whole, so a big round group can
// never wedge catch-up. It returns false as its second value when more records remain.
func (r *Round) readHeartbeats(
	ctx context.Context, tenant string, offset int64,
) ([]caughtHeartbeat, bool, error) {
	topic := topics.Heartbeat(tenant)
	high, err := r.client.GetOffset(topic, 0, sarama.OffsetNewest)
	if err != nil {
		return nil, false, fmt.Errorf("reading the heartbeat topic end: %w", err)
	}
	if offset == high {
		return nil, true, nil
	}
	if offset > high {
		// The topic was deleted and recreated, so its offsets restarted at zero, or it
		// was truncated: a stored offset can never legitimately exceed the end of the
		// topic it was written to. Reporting the tenant caught up would replay nothing,
		// and the comparison would then re-emit the spans the lost heartbeats billed
		// under new heartbeat ids: the customer pays twice. Refuse instead, loudly, and
		// leave recovery to the operator.
		return nil, false, fmt.Errorf(
			"stored heartbeat offset %d of %s is past the end %d of its heartbeat topic: "+
				"the topic was recreated or truncated and the billed history is gone; "+
				"replaying what the topic now holds would bill the same spans twice; "+
				"restore the topic's history or rebuild the tenant's progress from a "+
				"backup that matches the topic",
			offset, tenant, high,
		)
	}
	partition, err := r.consumer.ConsumePartition(topic, 0, offset)
	if err != nil {
		if !errors.Is(err, sarama.ErrOffsetOutOfRange) {
			return nil, false, fmt.Errorf("consuming %s from %d: %w", topic, offset, err)
		}
		// Retention deleted the segments below the stored offset: the round cannot
		// know which of the lost heartbeats the provider already billed. Resuming at
		// the oldest retained record would rebuild a ledger missing those spans, and
		// the next round would resend them under new heartbeat ids: the customer pays
		// twice. Refuse instead, loudly, and leave recovery to the operator.
		oldest, oldestErr := r.client.GetOffset(topic, 0, sarama.OffsetOldest)
		if oldestErr != nil {
			return nil, false, fmt.Errorf(
				"reading the oldest retained heartbeat of %s: %w", topic, oldestErr,
			)
		}
		return nil, false, fmt.Errorf(
			"stored heartbeat offset %d of %s is below the topic's retention start %d: "+
				"the heartbeats in between cannot be replayed and the provider may have "+
				"billed them; restore the topic's retention or rebuild from a backup "+
				"whose offset the topic still covers",
			offset, topic, oldest,
		)
	}
	defer func() { _ = partition.Close() }()

	caught := make([]caughtHeartbeat, 0, r.catchUpBatch+1)
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-readCtx.Done()
		_ = partition.Close()
	}()
read:
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case message, ok := <-partition.Messages():
			if !ok {
				break read
			}
			heartbeat, err := decodeHeartbeat(message.Value)
			if err != nil {
				return nil, false, fmt.Errorf(
					"decoding heartbeat at %s/%d: %w", topic, message.Offset, err,
				)
			}
			heartbeat.offset = message.Offset
			caught = append(caught, heartbeat)
			if r.groupBoundReached(caught) {
				break read
			}
		case <-time.After(500 * time.Millisecond):
			break read
		}
	}

	// Cut back to the end of the last complete group: the trailing group is complete
	// only when the read reached the topic's end, measured against the high watermark
	// fetched before reading.
	cut := len(caught)
	if cut > 0 && caught[cut-1].offset+1 < high {
		// Leave the trailing partial group unread: it is not applied, and the
		// offsets do not move past the previous complete group.
		cut = groupStart(caught, cut-1)
	}
	// Finished only when every applicable record was kept and the read position reached
	// the high watermark fetched before reading.
	finished := cut > 0 && caught[cut-1].offset+1 >= high
	return caught[:cut], finished, nil
}

// groupBoundReached reports whether the pass has read its fill: catchUpBatch complete round
// groups plus the first record of the next group, which witnesses the last kept group's
// boundary. Records of the group being read never count towards the bound, so a group larger
// than the batch is read whole.
func (r *Round) groupBoundReached(caught []caughtHeartbeat) bool {
	groups := 0
	for i := 1; i < len(caught); i++ {
		if !caught[i].roundTime.Equal(caught[i-1].roundTime) {
			groups++
			if groups >= r.catchUpBatch {
				return true
			}
		}
	}
	return false
}

// groupStart returns the index of the first record of the group that the given index belongs
// to.
func groupStart(records []caughtHeartbeat, index int) int {
	for index > 0 && records[index-1].roundTime.Equal(records[index].roundTime) {
		index--
	}
	return index
}

// groupEnd returns the index one past the last record of the group that the given index
// belongs to.
func groupEnd(records []caughtHeartbeat, index int) int {
	for index+1 < len(records) && records[index+1].roundTime.Equal(records[index].roundTime) {
		index++
	}
	return index + 1
}

// applyHeartbeat adds the heartbeat's quantity over its interval to the ledger, merging it
// into the stretches it overlaps: a correction often shares its start with an existing
// stretch, and a separate row would collide or double-count.
func (r *Round) applyHeartbeat(
	ctx context.Context, tx database.Querier, tenant string, caught *caughtHeartbeat,
) error {
	existing, err := database.LedgerPiecesWhere(
		ctx, tx, tenant,
		"resource_type = $2 AND resource_id = $3 AND meter = $4 AND component = $5 AND dimensions = $6",
		caught.key.ResourceType, caught.key.ResourceID, caught.key.Meter,
		caught.key.Component, caught.key.Dimensions,
	)
	if err != nil {
		return err
	}
	// The merge read the meter's every piece, so its result owns the whole history:
	// a zero tailFrom makes SyncLedger replace it outright.
	merged, err := mergeIntoLedger(existing, caught)
	if err != nil {
		return err
	}
	return database.SyncLedger(ctx, tx, tenant, caught.key, merged, time.Time{})
}

// mergeIntoLedger adds one caught heartbeat's quantity, spread evenly over its interval, to
// the ledger's stretches. The caught interval's boundaries split the stretches they cross,
// the quantities add, and adjacent stretches of the same summed rate rejoin into one piece.
func mergeIntoLedger(
	existing []database.LedgerPiece, caught *caughtHeartbeat,
) ([]database.LedgerPiece, error) {
	caughtDuration := caught.end.Sub(caught.start).Microseconds()
	if caughtDuration <= 0 {
		return existing, nil
	}
	stretches := append([]database.LedgerPiece(nil), existing...)
	sort.Slice(stretches, func(i, j int) bool {
		return stretches[i].StartTime.Before(stretches[j].StartTime)
	})
	caughtRate := new(big.Rat).SetFrac(big.NewInt(caught.quantityMicro), big.NewInt(caughtDuration))

	points := map[time.Time]bool{caught.start: true, caught.end: true}
	for _, piece := range stretches {
		points[piece.StartTime] = true
		points[piece.EndTime] = true
	}
	boundaries := make([]time.Time, 0, len(points))
	for point := range points {
		boundaries = append(boundaries, point)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })

	inCaught := func(moment time.Time) bool {
		return !moment.Before(caught.start) && moment.Before(caught.end)
	}

	var merged []database.LedgerPiece
	for i := 1; i < len(boundaries); i++ {
		start, end := boundaries[i-1], boundaries[i]
		if !start.Before(end) {
			continue
		}
		rate := actualRateAt(stretches, start)
		if inCaught(start) {
			rate = new(big.Rat).Add(rate, caughtRate)
		}
		quantity, err := usage.RationalToMicro(
			new(big.Rat).Mul(rate, big.NewRat(end.Sub(start).Microseconds(), 1)),
		)
		if err != nil {
			return nil, fmt.Errorf("applying heartbeat %s: %w", caught.id, err)
		}
		// Adjacent stretches of the same summed rate are one piece of usage.
		if last := len(merged) - 1; last >= 0 && merged[last].EndTime.Equal(start) &&
			pieceRate(merged[last]).Cmp(rate) == 0 {
			merged[last].EndTime = end
			merged[last].QuantityMicro += quantity
			continue
		}
		merged = append(merged, database.LedgerPiece{
			Key: caught.key, StartTime: start, EndTime: end, QuantityMicro: quantity,
		})
	}
	return merged, nil
}

// pieceRate returns a piece's committed rate in micro units per microsecond.
func pieceRate(piece database.LedgerPiece) *big.Rat {
	duration := piece.EndTime.Sub(piece.StartTime).Microseconds()
	if duration <= 0 {
		return new(big.Rat)
	}
	return new(big.Rat).SetFrac(big.NewInt(piece.QuantityMicro), big.NewInt(duration))
}

// decodeHeartbeat reads a heartbeat record from the topic.
func decodeHeartbeat(value []byte) (caughtHeartbeat, error) {
	event := cloudevents.Event{}
	if err := event.UnmarshalJSON(value); err != nil {
		return caughtHeartbeat{}, fmt.Errorf("unmarshaling CloudEvent: %w", err)
	}
	data, err := heartbeat.Data(event)
	if err != nil {
		return caughtHeartbeat{}, err
	}
	from, to, err := heartbeat.Interval(data)
	if err != nil {
		return caughtHeartbeat{}, err
	}
	roundTime, err := heartbeat.HeartbeatTime(data)
	if err != nil {
		return caughtHeartbeat{}, err
	}
	quantity, err := heartbeat.ParseMicro(data.Usage.Quantity)
	if err != nil {
		return caughtHeartbeat{}, err
	}
	dimensions, err := json.Marshal(data.BillingDimensions)
	if err != nil {
		return caughtHeartbeat{}, fmt.Errorf("encoding dimensions: %w", err)
	}
	return caughtHeartbeat{
		id:        event.ID(),
		roundTime: roundTime,
		key: database.LedgerKey{
			ResourceType: schema.SchemaResourceTypeToProto(data.ResourceType),
			ResourceID:   data.ResourceID,
			Meter:        data.Meter,
			Component:    data.Component,
			Dimensions:   string(dimensions),
		},
		start:         from,
		end:           to,
		quantityMicro: quantity,
	}, nil
}
