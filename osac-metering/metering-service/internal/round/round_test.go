/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package round_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/relay"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// sentBeat is a heartbeat read from a topic, with its ID.
type sentBeat struct {
	id   string
	data schema.HeartbeatData
}

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

// pipeline bundles a fold, a round, and a relay against one database, with a clock the test
// controls.
type pipeline struct {
	tenant string
	store  *database.Store
	fold   *fold.Fold
	round  *round.Round
	relay  *relay.Relay
}

func newPipeline(t *testing.T, tenant string) *pipeline {
	t.Helper()
	return newPipelineOnStore(t, tenant, testenv.Database(t))
}

func newPipelineOnStore(t *testing.T, tenant string, store *database.Store) *pipeline {
	t.Helper()
	return newPipelineWithRevisions(t, tenant, store, defaultRevisions(t))
}

// defaultRevisions returns the shipped revision 0001.
func defaultRevisions(t *testing.T) []rules.Revision {
	t.Helper()
	revision, err := rules.DefaultRevision()
	if err != nil {
		t.Fatalf("parsing default revision: %v", err)
	}
	return []rules.Revision{revision}
}

func newPipelineWithRevisions(
	t *testing.T, tenant string, store *database.Store, revisions []rules.Revision,
) *pipeline {
	t.Helper()
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator(revisions, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	foldLoop, err := fold.New(store, testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	roundLoop, err := round.New(
		store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(),
	)
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}
	relayLoop, err := relay.New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	return &pipeline{tenant: tenant, store: store, fold: foldLoop, round: roundLoop, relay: relayLoop}
}

// startRelay runs the relay in the background for the rest of the test.
func (p *pipeline) startRelay(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.relay.RunTenant(ctx, p.tenant) }()
}

// foldAll folds until the tenant's stored offset reaches total.
func (p *pipeline) foldAll(t *testing.T, total int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = p.fold.RunTenant(ctx, p.tenant) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		progress, err := p.store.GetTenantProgress(context.Background(), p.tenant)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("reading progress: %v", err)
		}
		if progress.EventOffset >= int64(total) {
			cancel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold did not reach offset %d, stopped at %d", total, progress.EventOffset)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// roundAt runs one round at a fixed time.
func (p *pipeline) roundAt(t *testing.T, moment time.Time) {
	t.Helper()
	p.round.SetClock(func() time.Time { return moment })
	if err := p.round.Once(context.Background(), p.tenant); err != nil {
		t.Fatalf("round at %s: %v", moment, err)
	}
}

// waitOutboxEmpty waits until the relay has sent every heartbeat the rounds wrote.
func (p *pipeline) waitOutboxEmpty(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows, err := database.NextOutbox(context.Background(), p.store.Pool(), p.tenant, 1)
		if err != nil {
			t.Fatalf("reading outbox: %v", err)
		}
		if len(rows) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not drain the outbox")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sentHeartbeats reads every heartbeat the relay put on the tenant's heartbeat topic.
func sentHeartbeats(t *testing.T, tenant string) []sentBeat {
	t.Helper()
	var result []sentBeat
	for _, value := range testenv.ConsumeAll(t, topics.Heartbeat(tenant)) {
		event := cloudevents.Event{}
		if err := event.UnmarshalJSON(value); err != nil {
			t.Fatalf("unmarshaling heartbeat: %v", err)
		}
		data, err := heartbeat.Data(event)
		if err != nil {
			t.Fatalf("decoding heartbeat: %v", err)
		}
		result = append(result, sentBeat{id: event.ID(), data: data})
	}
	return result
}

// quantities renders the heartbeats as comparable text.
func quantities(beats []sentBeat) []string {
	var lines []string
	for _, beat := range beats {
		lines = append(lines, fmt.Sprintf(
			"%s %s..%s %s", beat.data.Meter, beat.data.Usage.From, beat.data.Usage.To,
			beat.data.Usage.Quantity,
		))
	}
	return lines
}

// TestLateEventSendsTheCorrection replays the design's example: a VM that stops, but the
// update reaches fulfillment late.
func TestLateEventSendsTheCorrection(t *testing.T) {
	p := newPipeline(t, "late")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "small", base),
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(40*time.Second), base, "web", "RUNNING",
			base.Add(40*time.Second), "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	// The stop reaches fulfillment late: it happened at 10:02:30 but arrives after the
	// 10:03 round.
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e4", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+10*time.Second), base, "web", "STOPPED",
			base.Add(2*time.Minute+30*time.Second), "small", base),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:40Z..2026-10-05T10:01:00Z 20.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z -30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestRoundLevelSubSecondRun checks the round-level behavior: a run still under one second
// at the first round sends nothing, and the next round bills the whole run from its start,
// including the part the first round watched as pending.
func TestRoundLevelSubSecondRun(t *testing.T) {
	p := newPipeline(t, "subsecond")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The run is 0.4 s long at the first round: nothing is billable yet.
	p.roundAt(t, base.Add(400*time.Millisecond))
	if outboxCount(t, p.store, p.tenant) != 0 {
		t.Fatal("a run under one second must not send a heartbeat")
	}

	// 1.4 s later the run is 1.8 s long: the round bills 1.8 s from the run's start.
	p.roundAt(t, base.Add(1800*time.Millisecond))
	p.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:00:01.8Z 1.800000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestRestoreSendsNothingTwice simulates a database restored from a backup: the restored
// ledger is behind the heartbeat topic, and catch-up brings it up to date before the round
// compares.
func TestRestoreSendsNothingTwice(t *testing.T) {
	const tenant = "restore"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// Backup point: after the 10:01 round and its relay.
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, p.tenant)

	// The live system continues without the backup.
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	// The restored database knows only the backup. Catch-up reads the heartbeats sent
	// after the backup, and the round sends only the difference.
	restored := newPipelineOnStore(t, p.tenant, restore(t, backup, p.tenant))
	restored.startRelay(t)
	restored.roundAt(t, base.Add(4*time.Minute))
	restored.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		// Before the restore.
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		// After the restore: only 10:03 to 10:04.
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestTwoPodsCatchUpApplyEachHeartbeatOnce runs two rounds concurrently on a restored
// database; the round lock and the ID checks keep each heartbeat applied once.
func TestTwoPodsCatchUpApplyEachHeartbeatOnce(t *testing.T) {
	const tenant = "twopods"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, p.tenant)
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	restoredStore := restore(t, backup, p.tenant)
	revision, err := rules.DefaultRevision()
	if err != nil {
		t.Fatalf("parsing default revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}

	// Two pods run a round at the same time on the restored database.
	var failuresMu sync.Mutex
	var failures []error
	done := make(chan struct{})
	for i := range 2 {
		pod, err := round.New(
			restoredStore, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(),
		)
		if err != nil {
			t.Fatalf("creating round: %v", err)
		}
		pod.SetClock(func() time.Time { return base.Add(4 * time.Minute) })
		go func() {
			if err := pod.Once(context.Background(), tenant); err != nil {
				failuresMu.Lock()
				failures = append(failures, err)
				failuresMu.Unlock()
			}
			done <- struct{}{}
		}()
		_ = i
	}
	for range 2 {
		<-done
	}
	failuresMu.Lock()
	defer failuresMu.Unlock()
	if len(failures) > 0 {
		t.Fatalf("rounds failed: %v", failures)
	}

	// Relay the restored database's outbox with a second relay loop, so both pods' work
	// reaches the topic.
	restoredRelay, err := relay.New(restoredStore, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	relayCtx, relayCancel := context.WithCancel(context.Background())
	go func() { _ = restoredRelay.RunTenant(relayCtx, tenant) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows, err := database.NextOutbox(context.Background(), restoredStore.Pool(), tenant, 1)
		if err != nil {
			t.Fatalf("reading outbox: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not drain the restored outbox")
		}
		time.Sleep(20 * time.Millisecond)
	}
	relayCancel()

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestRelayResendKeepsIDs simulates a crash between the Kafka send and the outbox delete: the
// same rows are sent again with the same IDs, and catch-up on a fresh database applies each
// once.
func TestRelayResendKeepsIDs(t *testing.T) {
	p := newPipeline(t, "resend")

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The round writes its heartbeat. The relay is not running yet.
	p.roundAt(t, base.Add(time.Minute))
	rows, err := database.NextOutbox(context.Background(), p.store.Pool(), p.tenant, 10)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one outbox row, got %d", len(rows))
	}

	// The crash: the rows reach the topic but the delete never commits. The relay then
	// starts, sends them again with the same IDs, and deletes them.
	producer := testenv.NewProducer(t)
	for _, row := range rows {
		message := &sarama.ProducerMessage{
			Topic: topics.Heartbeat(p.tenant),
			Key:   sarama.StringEncoder(row.ID),
			Value: sarama.ByteEncoder(row.Payload),
		}
		if _, _, err := producer.SendMessage(message); err != nil {
			t.Fatalf("resending: %v", err)
		}
	}
	p.startRelay(t)
	p.waitOutboxEmpty(t)

	// A fresh database replays everything from offset 0. The duplicate on the topic
	// carries the same ID, so catch-up counts it once.
	fresh := newPipelineOnStore(t, p.tenant, testenv.Database(t))
	fresh.startRelay(t)
	fresh.foldAll(t, total)
	fresh.roundAt(t, base.Add(2*time.Minute))
	fresh.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}

	// The duplicate on the topic carries the same ID.
	beats := sentHeartbeats(t, p.tenant)
	if beats[0].id != beats[1].id {
		t.Fatalf("resent heartbeats must keep their IDs: %s vs %s", beats[0].id, beats[1].id)
	}
}

// TestBoundedCatchUp checks that a long backlog applies in bounded steps, that the round
// skips its comparison until catch-up finishes, and that a round group split across the batch
// boundary is applied whole: no heartbeat twice, none skipped.
func TestBoundedCatchUp(t *testing.T) {
	p := newPipeline(t, "bounded")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The backup knows the first round. After it, the VM is resized mid-minute, so the
	// 10:03 round sends a group of two heartbeats: small until the resize, large after.
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, p.tenant)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+30*time.Second)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	restored := newPipelineOnStore(t, p.tenant, restore(t, backup, p.tenant))
	restored.foldAll(t, total)
	// The batch bounds groups per pass, so with a batch of one the 10:03 group of two
	// is larger than the batch: it must still be read whole on the second pass.
	restored.round.SetCatchUpBatch(1)

	// The first restored round reads the 10:02 heartbeat and the first half of the
	// 10:03 group. The group is split at the batch bound, so only the complete 10:02
	// group applies and nothing is half-applied.
	restored.roundAt(t, base.Add(4*time.Minute))
	if outboxCount(t, restored.store, p.tenant) != 0 {
		t.Fatal("a round with unfinished catch-up must not write heartbeats")
	}
	small, err := database.LedgerPiecesWhere(
		context.Background(), restored.store.Pool(), p.tenant, "meter = $2", "compute",
	)
	if err != nil {
		t.Fatalf("reading restored ledger: %v", err)
	}
	for _, piece := range small {
		if piece.EndTime.After(base.Add(2 * time.Minute)) {
			t.Fatalf(
				"a group split at the batch bound must stay unread, got a piece ending at %s",
				piece.EndTime,
			)
		}
	}

	// The second pass reads the whole 10:03 group, applies it, and the round sends only
	// the new interval.
	restored.roundAt(t, base.Add(5*time.Minute))
	if outboxCount(t, restored.store, p.tenant) == 0 {
		t.Fatal("a finished catch-up must let the round send its difference")
	}
	restored.startRelay(t)
	restored.waitOutboxEmpty(t)

	// Nothing twice, nothing skipped: the small meter sums to 10:00 to 10:02:30 and the
	// large meter to 10:02:30 to 10:05.
	got := quantities(sentHeartbeats(t, p.tenant))
	// Within the 10:03 group the meters arrive in the round's deterministic key order,
	// which sorts the large dimension set before the small one.
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z 30.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:05:00Z 120.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestCatchUpRefusesWhenRetentionDeletedTheStoredOffset checks that a stored heartbeat
// offset below the topic's log start refuses the round instead of resuming at the
// oldest retained record. The lost heartbeats may already be billed: resuming would
// rebuild a ledger missing their spans, and the next round would resend them under new
// heartbeat ids. The customer would pay twice, so the round holds billing and says why.
func TestCatchUpRefusesWhenRetentionDeletedTheStoredOffset(t *testing.T) {
	p := newPipeline(t, "belowretention")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	// Retention has deleted every segment below the stored offset, the way a topic with
	// finite retention does after a long database restore: the stored offset now points
	// below the topic's log start.
	if _, err := p.store.Pool().Exec(
		context.Background(),
		`UPDATE progress SET heartbeat_offset = -5 WHERE tenant = $1`, p.tenant,
	); err != nil {
		t.Fatalf("pushing the stored offset below the log start: %v", err)
	}

	// The round must refuse, naming the retention gap.
	err := p.round.Once(context.Background(), p.tenant)
	if err == nil || !strings.Contains(err.Error(), "retention start") {
		t.Fatalf("expected the round to refuse on the expired history, got %v", err)
	}

	// Billing holds: nothing new was sent after the two rounds that succeeded.
	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestRoundWithSteadyEventStream checks round liveness on a tenant whose events keep
// arriving: the round compares the fold against the watermark it captured when it started
// waiting, so a steady stream still gets rounds instead of chasing a watermark that always
// moves.
func TestRoundWithSteadyEventStream(t *testing.T) {
	p := newPipeline(t, "steady")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The fold keeps running and events keep arriving while the rounds tick.
	foldCtx, cancelFold := context.WithCancel(context.Background())
	defer cancelFold()
	go func() { _ = p.fold.RunTenant(foldCtx, p.tenant) }()

	producer := testenv.NewProducer(t)
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	go func() {
		for i := 0; streamCtx.Err() == nil; i++ {
			event := &privatev1.Event{
				Id:        fmt.Sprintf("stream-%d", i),
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Timestamp: timestamppb.New(base.Add(time.Duration(i) * time.Second)),
				Payload:   &privatev1.Event_Project{Project: &privatev1.Project{Id: "p-1"}},
			}
			value, err := proto.Marshal(event)
			if err != nil {
				return
			}
			if _, _, err := producer.SendMessage(&sarama.ProducerMessage{
				Topic: topics.Events(p.tenant),
				Key:   sarama.StringEncoder(event.Id),
				Value: sarama.ByteEncoder(value),
			}); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	billed := false
	for i := 1; i <= 40 && !billed; i++ {
		p.roundAt(t, base.Add(time.Duration(i)*time.Minute))
		if outboxCount(t, p.store, p.tenant) > 0 {
			billed = true
		}
		// Give the fold time to reach the round's captured watermark; a real
		// deployment ticks once a minute.
		time.Sleep(50 * time.Millisecond)
	}
	stopStream()
	cancelFold()
	if !billed {
		t.Fatal("a tenant with a steady event stream never got a round")
	}
	p.waitOutboxEmpty(t)
	if beats := sentHeartbeats(t, p.tenant); len(beats) == 0 {
		t.Fatal("the steady-stream tenant sent no heartbeats")
	}
}

// TestCatchUpReadsOversizedGroupsWhole checks that a round group larger than the catch-up
// batch still completes: the pass extends beyond the batch while it stays inside one group,
// the bound limits groups per pass, and nothing is applied twice or skipped.
func TestCatchUpReadsOversizedGroupsWhole(t *testing.T) {
	const tenant = "oversized"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// Backup after the first round.
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, tenant)

	// Two resizes within one minute: the 10:03 round sends a group of three.
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "medium",
			base.Add(2*time.Minute+20*time.Second)),
		testenv.VMEvent("e3", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+30*time.Second), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+40*time.Second)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.roundAt(t, base.Add(4*time.Minute))
	p.roundAt(t, base.Add(5*time.Minute))
	p.waitOutboxEmpty(t)

	restored := newPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.foldAll(t, total)
	restored.round.SetCatchUpBatch(2)

	// The topic holds five groups after the backup point: 10:02 (one), 10:03 (three),
	// 10:04 (one), 10:05 (one). With a batch of two groups, the first pass applies the
	// 10:02 group and the whole oversized 10:03 group, and stops at the 10:04 witness.
	restored.roundAt(t, base.Add(6*time.Minute))
	if outboxCount(t, restored.store, tenant) != 0 {
		t.Fatal("a round with unfinished catch-up must not write heartbeats")
	}
	pieces, err := database.LedgerPiecesWhere(
		context.Background(), restored.store.Pool(), tenant, "meter = $2", "compute",
	)
	if err != nil {
		t.Fatalf("reading restored ledger: %v", err)
	}
	var keys []database.LedgerPiece
	keys = append(keys, pieces...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].StartTime.Before(keys[j].StartTime) })
	var stretches []string
	for _, piece := range keys {
		stretches = append(stretches, fmt.Sprintf(
			"%s..%s", piece.StartTime.UTC().Format("15:04:05"), piece.EndTime.UTC().Format("15:04:05"),
		))
	}
	want := []string{"10:00:00..10:02:20", "10:02:20..10:02:40", "10:02:40..10:03:00"}
	if fmt.Sprint(stretches) != fmt.Sprint(want) {
		t.Fatalf("the oversized group must be applied whole:\ngot:\n%s\nwant:\n%s",
			stretches, want)
	}

	// The second pass reads the remaining groups and the round bills the difference.
	restored.roundAt(t, base.Add(7*time.Minute))
	if outboxCount(t, restored.store, tenant) != 1 {
		t.Fatalf("expected exactly one difference heartbeat, got %d",
			outboxCount(t, restored.store, tenant))
	}
	restored.startRelay(t)
	restored.waitOutboxEmpty(t)

	// Nothing twice, nothing skipped: the live heartbeats plus one difference for
	// 10:05 to 10:07. Within the 10:03 group the meters arrive in the round's key order.
	got := quantities(sentHeartbeats(t, tenant))
	wantBeats := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:40Z..2026-10-05T10:03:00Z 20.000000",
		"compute 2026-10-05T10:02:20Z..2026-10-05T10:02:40Z 20.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:20Z 20.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
		"compute 2026-10-05T10:04:00Z..2026-10-05T10:05:00Z 60.000000",
		"compute 2026-10-05T10:05:00Z..2026-10-05T10:07:00Z 120.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(wantBeats) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(wantBeats))
	}
}

// TestCatchUpCommitsPerGroup checks the commit granularity catch-up restores from: a pod
// killed mid-pass leaves the round groups that already committed applied — the offset moved
// past them and the ledger holds their usage — so the next pass resumes at the next group
// instead of repeating the identical pass.
func TestCatchUpCommitsPerGroup(t *testing.T) {
	const tenant = "pergroup"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The backup knows the first round. After it the VM is resized mid-minute, so the
	// 10:03 round sends a group of two heartbeats and the 10:02 and 10:04 rounds one each:
	// three groups on the topic.
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, tenant)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+30*time.Second)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	// The pod dies mid-catch-up: two of the three groups committed, the third never ran.
	restored := newPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.foldAll(t, total)
	restored.round.SetCrashAfterGroups(2)
	if err := restored.round.Once(context.Background(), tenant); !errors.Is(err, round.ErrCrashed) {
		t.Fatalf("catch-up must report the simulated crash, got %v", err)
	}

	// The first two groups stayed committed. The topic holds the 10:02 heartbeat at offset
	// 1, the 10:03 pair at offsets 2 and 3, and the 10:04 heartbeat at offset 4; two
	// committed groups move the offset to 4, past everything but the last group.
	progress, err := restored.store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	if progress.HeartbeatOffset != 4 {
		t.Fatalf("two committed groups must move the offset past their records, got %d",
			progress.HeartbeatOffset)
	}
	pieces, err := database.LedgerPiecesWhere(
		context.Background(), restored.store.Pool(), tenant, "meter = $2", "compute",
	)
	if err != nil {
		t.Fatalf("reading restored ledger: %v", err)
	}
	var lines []string
	for _, piece := range pieces {
		lines = append(lines, fmt.Sprintf(
			"%s..%s %d", piece.StartTime.UTC().Format("15:04:05"),
			piece.EndTime.UTC().Format("15:04:05"), piece.QuantityMicro,
		))
	}
	sort.Strings(lines)
	// The backup's 10:00..10:01 stretch merged with the two committed groups: small billed
	// to the 10:02:30 resize, large from it to 10:03. The 10:04 group never ran.
	want := []string{
		"10:00:00..10:02:30 150000000",
		"10:02:30..10:03:00 30000000",
	}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("the committed groups must stay applied:\ngot:\n%s\nwant:\n%s", join(lines), join(want))
	}

	// The restart resumes at the next group: a round without the crash finishes catch-up
	// and bills the difference, and nothing is applied twice or skipped.
	restored.round.SetCrashAfterGroups(0)
	restored.roundAt(t, base.Add(5*time.Minute))
	if outboxCount(t, restored.store, tenant) == 0 {
		t.Fatal("a finished catch-up must let the round send its difference")
	}
	restored.startRelay(t)
	restored.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, tenant))
	wantBeats := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z 30.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
		"compute 2026-10-05T10:04:00Z..2026-10-05T10:05:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(wantBeats) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(wantBeats))
	}
}

// TestCrashMidCatchUpRestoreReplaysSameLedger proves the restore-safety property: a database
// restored from the snapshot taken before a crash mid-catch-up replays the same round groups
// deterministically and reaches exactly the ledger one uninterrupted pass produced. The
// property holds because each group commits its ledger merge together with the offset past
// it, and the merge is a pure function of the ledger it reads and the heartbeat's interval
// and quantity.
func TestCrashMidCatchUpRestoreReplaysSameLedger(t *testing.T) {
	const tenant = "crash-restore"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// The same backlog as the per-group test: a backup after the first round, then a
	// mid-minute resize, so the topic holds three round groups after the backup point.
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)
	backup := snapshot(t, p.store, tenant)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+30*time.Second)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	// The crash: a restored database dies mid-catch-up, after two of the three groups
	// committed.
	crashed := newPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	crashed.round.SetCrashAfterGroups(2)
	if err := crashed.round.Once(context.Background(), tenant); !errors.Is(err, round.ErrCrashed) {
		t.Fatalf("catch-up must report the simulated crash, got %v", err)
	}

	// The one-pass result: a database restored from the same snapshot catches up in one
	// uninterrupted pass. The comparison stays out of the way, so the ledger is exactly
	// what catch-up produced.
	reference := newPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	reference.roundAt(t, base.Add(5*time.Minute))

	// The database is restored from the snapshot taken before the crash and replays the
	// same groups. The final ledger must equal the one-pass result.
	replayed := newPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	replayed.roundAt(t, base.Add(5*time.Minute))

	got := ledgerLines(t, replayed.store, tenant)
	want := ledgerLines(t, reference.store, tenant)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("a restore to the pre-crash snapshot must replay the same groups to the same ledger:\ngot:\n%s\nwant:\n%s",
			join(got), join(want))
	}
	replayedProgress, err := replayed.store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading replayed progress: %v", err)
	}
	referenceProgress, err := reference.store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading reference progress: %v", err)
	}
	if replayedProgress.HeartbeatOffset != referenceProgress.HeartbeatOffset ||
		replayedProgress.LastRoundTime == nil || referenceProgress.LastRoundTime == nil ||
		!replayedProgress.LastRoundTime.Equal(*referenceProgress.LastRoundTime) {
		t.Fatalf("the replay must reach the reference position: offset %d vs %d, last round %v vs %v",
			replayedProgress.HeartbeatOffset, referenceProgress.HeartbeatOffset,
			replayedProgress.LastRoundTime, referenceProgress.LastRoundTime)
	}

	// The crash left the ledger mid-catch-up, so the replay above was not a no-op: the
	// crashed database holds two groups, the replay and the reference all three.
	if fmt.Sprint(ledgerLines(t, crashed.store, tenant)) == fmt.Sprint(want) {
		t.Fatal("the crashed database must hold less than the one-pass result")
	}
}

// restoreForCatchUpOnly restores a snapshot and holds the event offset behind the fold's
// target, so the round after catch-up skips its comparison: the ledger keeps exactly what
// catch-up applied, which is the part of a round a restore replays.
func restoreForCatchUpOnly(t *testing.T, snap tenantSnapshot, tenant string) *database.Store {
	t.Helper()
	store := restore(t, snap, tenant)
	if _, err := store.Pool().Exec(
		context.Background(), `UPDATE progress SET event_offset = 0 WHERE tenant = $1`, tenant,
	); err != nil {
		t.Fatalf("holding the event offset back: %v", err)
	}
	return store
}

// ledgerLines renders a tenant's ledger in a deterministic comparable form.
func ledgerLines(t *testing.T, store *database.Store, tenant string) []string {
	t.Helper()
	pieces, err := store.LedgerPieces(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	sort.Slice(pieces, func(i, j int) bool {
		left, right := pieces[i], pieces[j]
		if left.Key.Dimensions != right.Key.Dimensions {
			return left.Key.Dimensions < right.Key.Dimensions
		}
		return left.StartTime.Before(right.StartTime)
	})
	var lines []string
	for _, piece := range pieces {
		lines = append(lines, fmt.Sprintf(
			"%s %s..%s %d", piece.Key.Dimensions, piece.StartTime.UTC().Format(time.RFC3339),
			piece.EndTime.UTC().Format(time.RFC3339), piece.QuantityMicro,
		))
	}
	return lines
}

// snapshot copies a tenant's rows, as a backup would.
type tenantSnapshot struct {
	history  []database.Fact
	ledger   []database.LedgerPiece
	progress database.Progress
}

func snapshot(t *testing.T, store *database.Store, tenant string) tenantSnapshot {
	t.Helper()
	ctx := context.Background()
	progress, err := store.GetTenantProgress(ctx, tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	resources, err := database.TenantResources(ctx, store.Pool(), tenant)
	if err != nil {
		t.Fatalf("listing resources: %v", err)
	}
	var snap tenantSnapshot
	snap.progress = progress
	for _, resource := range resources {
		facts, err := database.ResourceFacts(
			ctx, store.Pool(), tenant, resource.ResourceType, resource.ResourceID,
		)
		if err != nil {
			t.Fatalf("reading facts: %v", err)
		}
		snap.history = append(snap.history, facts...)
	}
	ledger, err := store.LedgerPieces(ctx, tenant)
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	snap.ledger = ledger
	return snap
}

// restore writes a snapshot into a fresh database, as a restore would.
func restore(t *testing.T, snap tenantSnapshot, tenant string) *database.Store {
	t.Helper()
	store := testenv.Database(t)
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := database.InsertFacts(ctx, tx, tenant, snap.history, snap.progress.EventOffset); err != nil {
		t.Fatalf("restoring history: %v", err)
	}
	for _, piece := range snap.ledger {
		if err := database.ReplaceLedger(
			ctx, tx, tenant, piece.Key, []database.LedgerPiece{piece},
		); err != nil {
			t.Fatalf("restoring ledger: %v", err)
		}
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE progress SET heartbeat_offset = $2, last_round_time = $3,
		 last_round_event_offset = $4 WHERE tenant = $1`,
		tenant, snap.progress.HeartbeatOffset, snap.progress.LastRoundTime,
		snap.progress.LastRoundEventOffset,
	); err != nil {
		t.Fatalf("restoring progress: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return store
}

func outboxCount(t *testing.T, store *database.Store, tenant string) int {
	t.Helper()
	rows, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 1000)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	return len(rows)
}

func join(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

// TestMidnightSplitsHeartbeatsAcrossDays checks that the round's difference merge never
// rejoins what the engine split at UTC midnight: rounds at 23:59:30 and 00:00:30 across a
// month boundary send two heartbeats, each dated inside its own billing day.
func TestMidnightSplitsHeartbeatsAcrossDays(t *testing.T) {
	const tenant = "midnight"
	evening := time.Date(2026, 9, 30, 23, 58, 0, 0, time.UTC)
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			evening, evening, "web", "RUNNING", evening, "small", evening),
	})
	p.foldAll(t, total)

	p.roundAt(t, evening.Add(90*time.Second))  // 23:59:30
	p.roundAt(t, evening.Add(150*time.Second)) // 00:00:30 the next month
	p.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-09-30T23:58:00Z..2026-09-30T23:59:30Z 90.000000",
		"compute 2026-09-30T23:59:30Z..2026-10-01T00:00:00Z 30.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z 30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
	// Each heartbeat is dated inside its own day: the CloudEvent time is the interval's
	// start, and a piece crossing midnight at month end would otherwise fall entirely
	// into one month.
	for _, beat := range sentHeartbeats(t, p.tenant) {
		from, err := time.Parse(time.RFC3339Nano, beat.data.Usage.From)
		if err != nil {
			t.Fatalf("parsing usage from: %v", err)
		}
		to, err := time.Parse(time.RFC3339Nano, beat.data.Usage.To)
		if err != nil {
			t.Fatalf("parsing usage to: %v", err)
		}
		if from.UTC().YearDay() != to.Add(-time.Nanosecond).UTC().YearDay() {
			t.Fatalf("a heartbeat crossed midnight: %s..%s", beat.data.Usage.From,
				beat.data.Usage.To)
		}
	}
}

// TestRevisionBoundarySplitsHeartbeats checks the same rule at a revision boundary: a meter
// billable at the same rate across the boundary sends one heartbeat per revision, each
// carrying the revision that billed it.
func TestRevisionBoundarySplitsHeartbeats(t *testing.T) {
	const tenant = "revsplit"
	first := defaultRevisions(t)
	second, err := rules.Parse(
		rules.NamePrefix+"0002", base.Add(90*time.Second).Format(time.RFC3339),
		first[0].MetersYAML,
	)
	if err != nil {
		t.Fatalf("parsing second revision: %v", err)
	}
	p := newPipelineWithRevisions(t, tenant, testenv.Database(t), []rules.Revision{first[0], second})
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	beats := sentHeartbeats(t, p.tenant)
	got := quantities(beats)
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:01:30Z 30.000000",
		"compute 2026-10-05T10:01:30Z..2026-10-05T10:02:00Z 30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
	if beats[1].data.RulesRevision != "0001" || beats[2].data.RulesRevision != "0002" {
		t.Fatalf(
			"heartbeats across a revision boundary must carry their billing revision: %s then %s",
			beats[1].data.RulesRevision, beats[2].data.RulesRevision,
		)
	}
}

// TestLedgerGrowsOneRowPerDay checks the design's performance rule: a meter billed across
// several days grows its ledger by one row per day, and a round extends the tail row without
// rewriting the rows beneath it.
func TestLedgerGrowsOneRowPerDay(t *testing.T) {
	const tenant = "growth"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	// One round per day, late in each day so every day's midnight falls between rounds.
	// Each round after the first crosses one midnight, so the ledger holds one row per
	// day spanned: five rows for the four midnights the first four rounds crossed.
	for day := 1; day <= 4; day++ {
		p.roundAt(t, base.Add(time.Duration(day)*24*time.Hour-time.Hour))
	}
	p.waitOutboxEmpty(t)

	rowsBefore := ledgerRowVersions(t, p.store, tenant)
	if len(rowsBefore) != 5 {
		t.Fatalf("a meter billed across five days must have one row per day, got %d",
			len(rowsBefore))
	}

	// The next round extends only the tail row: the rows beneath it keep their row
	// versions, so they were not rewritten.
	p.roundAt(t, base.Add(5*24*time.Hour-time.Hour))
	p.waitOutboxEmpty(t)

	rowsAfter := ledgerRowVersions(t, p.store, tenant)
	if len(rowsAfter) != 6 {
		t.Fatalf("the ledger must grow by one row per day, got %d rows after six days",
			len(rowsAfter))
	}
	// The tail row is the one that ended at the previous round's time; extending it is
	// the round's one rewrite. Every row beneath it must keep its row version.
	tailEnd := base.Add(4*24*time.Hour - time.Hour).UTC().Format(time.RFC3339Nano)
	for start, row := range rowsBefore {
		if row[0] == tailEnd {
			continue
		}
		if rowsAfter[start] != row {
			t.Fatalf("the row starting at %s was rewritten by a tail extension", start)
		}
	}

	// Nothing was lost or billed twice: the heartbeats sum to the whole billed time.
	var totalMicro int64
	for _, beat := range sentHeartbeats(t, p.tenant) {
		quantity, err := heartbeat.ParseMicro(beat.data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		totalMicro += quantity
	}
	// Five daily rounds bill from base to the fifth round's time: five days minus an
	// hour, because every round runs at 09:00.
	wantMicro := int64((5*24*time.Hour - time.Hour).Seconds()) * 1_000_000
	if totalMicro != wantMicro {
		t.Fatalf("heartbeats sum to %d micro, want %d", totalMicro, wantMicro)
	}
}

// ledgerRowVersions returns each ledger row's start time mapped to its end time and the
// transaction version that last wrote it, so a test can see whether a later round rewrote
// the row.
func ledgerRowVersions(
	t *testing.T, store *database.Store, tenant string,
) map[string][2]string {
	t.Helper()
	rows, err := store.Pool().Query(
		context.Background(),
		`SELECT start_time, end_time, xmin::text FROM ledger WHERE tenant = $1 ORDER BY start_time`,
		tenant,
	)
	if err != nil {
		t.Fatalf("reading ledger row versions: %v", err)
	}
	defer rows.Close()
	result := map[string][2]string{}
	for rows.Next() {
		var start, end time.Time
		var version string
		if err := rows.Scan(&start, &end, &version); err != nil {
			t.Fatalf("scanning ledger row version: %v", err)
		}
		result[start.UTC().Format(time.RFC3339Nano)] = [2]string{
			end.UTC().Format(time.RFC3339Nano), version,
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading ledger row versions: %v", err)
	}
	return result
}

// TestRemovedMeterCorrectionCarriesUnitAndRevision replays the design's resize example: a VM
// reported as resized, then a late event shows it stopped before the resize took effect. The
// large meter's negative correction must still carry the meter's unit and the revision that
// billed the pieces, although the meter has no expected usage at all.
func TestRemovedMeterCorrectionCarriesUnitAndRevision(t *testing.T) {
	const tenant = "resize"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		// The operator reports a resize to large, effective 10:30.
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(30*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(30*time.Minute)),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(31*time.Minute))
	p.roundAt(t, base.Add(32*time.Minute))
	p.roundAt(t, base.Add(33*time.Minute))
	p.waitOutboxEmpty(t)

	// The late event: the VM had stopped at 10:29:30, before the resize took effect.
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e3", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(34*time.Minute), base, "web", "STOPPED",
			base.Add(29*time.Minute+30*time.Second), "large", base.Add(30*time.Minute)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(34*time.Minute))
	p.waitOutboxEmpty(t)

	beats := sentHeartbeats(t, p.tenant)
	var small, large []schema.HeartbeatData
	for _, beat := range beats {
		switch beat.data.BillingDimensions["instance_type"] {
		case "small":
			small = append(small, beat.data)
		case "large":
			large = append(large, beat.data)
		}
	}
	if len(small) == 0 || len(large) == 0 {
		t.Fatalf("expected heartbeats on both the small and the large meter, got %d and %d",
			len(small), len(large))
	}

	// The large meter's correction cancels exactly the three rounds that billed it.
	var largeMicro int64
	for _, data := range large {
		quantity, err := heartbeat.ParseMicro(data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		largeMicro += quantity
	}
	if largeMicro != 0 {
		t.Fatalf("the large meter's heartbeats must cancel exactly, sum is %d", largeMicro)
	}
	lastLarge := large[len(large)-1]
	if lastLarge.Usage.Quantity != "-180.000000" {
		t.Fatalf("the large meter's correction is %s, want -180.000000",
			lastLarge.Usage.Quantity)
	}
	if lastLarge.Usage.Unit != "second" {
		t.Fatalf("the correction must carry the meter's unit, got %q", lastLarge.Usage.Unit)
	}
	if lastLarge.RulesRevision != "0001" {
		t.Fatalf("the correction must carry the revision that billed the pieces, got %q",
			lastLarge.RulesRevision)
	}

	// The small meter corrects the thirty seconds it overbilled.
	var smallMicro int64
	for _, data := range small {
		quantity, err := heartbeat.ParseMicro(data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		smallMicro += quantity
	}
	if smallMicro != int64((29*time.Minute+30*time.Second).Seconds())*1_000_000 {
		t.Fatalf("the small meter must sum to the running time, got %d", smallMicro)
	}
}

// TestUnjustifiedLedgerIsReversed covers the restore-after-retention-loss shape: a database
// whose event offset is ahead of what its history holds, so catch-up rebuilds the ledger from
// the heartbeat topic while the engine has no facts to justify any of it. The round must
// compare the ledger whole and reverse exactly the committed usage.
func TestUnjustifiedLedgerIsReversed(t *testing.T) {
	const tenant = "unjustified"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(time.Minute))
	p.waitOutboxEmpty(t)

	// A fresh database that never folded the events: its event offset claims everything
	// is folded, but history is empty, as a restore whose backup had already consumed
	// events that retention has since deleted would leave it. Catch-up rebuilds the
	// ledger from the heartbeat alone.
	q := newPipeline(t, tenant)
	if err := q.store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	if _, err := q.store.Pool().Exec(
		context.Background(), `UPDATE progress SET event_offset = $2 WHERE tenant = $1`,
		tenant, int64(total),
	); err != nil {
		t.Fatalf("advancing the event offset: %v", err)
	}
	q.startRelay(t)
	q.roundAt(t, base.Add(2*time.Minute))
	q.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z -60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
	assertLedgerEmpty(t, q.store, tenant)
}

// TestStateChangeAtRoundTimeIsNotANewFact pins the boundary the tail path relies on: a state
// change dated exactly at the previous round time is not a new fact, because
// ResourcesWithNewFacts compares with a strict "after". The resource reaches the round only
// through its open ledger row, and the row is reconciled by the tail path's fallback, which
// sends the reversal.
func TestStateChangeAtRoundTimeIsNotANewFact(t *testing.T) {
	const tenant = "boundary"
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)
	previous := base.Add(time.Minute)
	p.roundAt(t, previous)
	p.waitOutboxEmpty(t)

	// The restored database knows the creation and a stop dated exactly at the previous
	// round's time, but not the RUNNING stretch the heartbeat billed: the facts predate
	// the round's event offset and none is dated after its time, so none is new.
	q := newPipeline(t, tenant)
	if err := q.store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	tx, err := q.store.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	facts := []database.Fact{
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "created",
			Since: base, Value: "true"},
		{ResourceType: "ComputeInstance", ResourceID: "vm-7", Field: "status.state",
			Since: previous, Value: "STOPPED"},
	}
	if err := database.InsertFacts(context.Background(), tx, tenant, facts, int64(total)); err != nil {
		t.Fatalf("restoring history: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	q.startRelay(t)
	q.roundAt(t, base.Add(2*time.Minute))
	q.waitOutboxEmpty(t)

	// The open row catch-up rebuilt is unjustified — the engine never saw a billable
	// state — and only the tail path's fallback compares it, reversing the usage.
	got := quantities(sentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z -60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
	assertLedgerEmpty(t, q.store, tenant)
}

// TestNegativeCorrectionSplitsAtMidnight covers a negative difference that would merge across
// UTC midnight: a late stop leaves usage overbilled on both sides of midnight, and the
// correction must arrive as one heartbeat per billing day, never crossing midnight.
func TestNegativeCorrectionSplitsAtMidnight(t *testing.T) {
	const tenant = "neg-midnight"
	evening := time.Date(2026, 9, 30, 23, 58, 0, 0, time.UTC)
	p := newPipeline(t, tenant)
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			evening, evening, "web", "RUNNING", evening, "small", evening),
	})
	p.foldAll(t, total)

	p.roundAt(t, evening.Add(90*time.Second))  // 23:59:30
	p.roundAt(t, evening.Add(150*time.Second)) // 00:00:30 the next month
	p.waitOutboxEmpty(t)

	// The late event: the VM stopped at 23:59:00, before both rounds billed it.
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			evening.Add(3*time.Minute), evening, "web", "STOPPED",
			evening.Add(time.Minute), "small", evening),
	})
	p.foldAll(t, total)
	p.roundAt(t, evening.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	beats := sentHeartbeats(t, tenant)
	got := quantities(beats)
	want := []string{
		"compute 2026-09-30T23:58:00Z..2026-09-30T23:59:30Z 90.000000",
		"compute 2026-09-30T23:59:30Z..2026-10-01T00:00:00Z 30.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z 30.000000",
		"compute 2026-09-30T23:59:00Z..2026-10-01T00:00:00Z -60.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z -30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}

	// The corrections net to exactly the true running time: one minute.
	var totalMicro int64
	for _, beat := range beats {
		quantity, err := heartbeat.ParseMicro(beat.data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		totalMicro += quantity
	}
	if totalMicro != int64(time.Minute.Seconds())*1_000_000 {
		t.Fatalf("heartbeats sum to %d micro, want one minute", totalMicro)
	}
	for _, beat := range beats {
		from, err := time.Parse(time.RFC3339Nano, beat.data.Usage.From)
		if err != nil {
			t.Fatalf("parsing usage from: %v", err)
		}
		to, err := time.Parse(time.RFC3339Nano, beat.data.Usage.To)
		if err != nil {
			t.Fatalf("parsing usage to: %v", err)
		}
		if from.UTC().YearDay() != to.Add(-time.Nanosecond).UTC().YearDay() {
			t.Fatalf("a heartbeat crossed midnight: %s..%s", beat.data.Usage.From,
				beat.data.Usage.To)
		}
	}
}

// assertLedgerEmpty fails when the tenant still has ledger rows.
func assertLedgerEmpty(t *testing.T, store *database.Store, tenant string) {
	t.Helper()
	pieces, err := database.LedgerPiecesWhere(context.Background(), store.Pool(), tenant, "")
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	if len(pieces) != 0 {
		t.Fatalf("the ledger must be empty after the reversal, still holds %d rows", len(pieces))
	}
}

// TestLateResizeSendsPositiveAndNegativeCorrections checks the other direction of a late
// event: a resize that happened before a round already billed the old type must split
// the billed span at the resize time, sending a positive correction on the new type and
// a negative one on the old, so the total is right without double billing.
func TestLateResizeSendsPositiveAndNegativeCorrections(t *testing.T) {
	p := newPipeline(t, "late-resize")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	// The resize happened at 10:02:30 but arrives after the 10:03 round.
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+10*time.Second), base, "web", "RUNNING",
			base, "large", base.Add(2*time.Minute+30*time.Second)),
	})
	p.foldAll(t, total)
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	got := quantities(sentHeartbeats(t, p.tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:04:00Z 90.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z -30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}
