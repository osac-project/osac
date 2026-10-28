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

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/pipelinetest"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

// TestCatchUpRefusesWhenRetentionDeletedTheStoredOffset checks that a stored heartbeat
// offset below the topic's log start refuses the round instead of resuming at the
// oldest retained record. The lost heartbeats may already be billed: resuming would
// rebuild a ledger missing their spans, and the next round would resend them under new
// heartbeat ids. The customer would pay twice, so the round holds billing and says why.
func TestCatchUpRefusesWhenRetentionDeletedTheStoredOffset(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "belowretention")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	// Retention has deleted every segment below the stored offset, the way a topic with
	// finite retention does after a long database restore: the stored offset now points
	// below the topic's log start.
	if _, err := p.Store.Pool().Exec(
		context.Background(),
		`UPDATE progress SET heartbeat_offset = -5 WHERE tenant = $1`, p.Tenant,
	); err != nil {
		t.Fatalf("pushing the stored offset below the log start: %v", err)
	}

	// The round must refuse, naming the retention gap.
	err := p.Round.Once(context.Background(), p.Tenant, time.Now())
	if err == nil || !strings.Contains(err.Error(), "retention start") {
		t.Fatalf("expected the round to refuse on the expired history, got %v", err)
	}

	// Billing holds: nothing new was sent after the two rounds that succeeded.
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, p.Tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// TestCatchUpRefusesWhenHeartbeatTopicWasRecreated checks the recreation case: a stored
// heartbeat offset past the topic's end means the topic was deleted and recreated (its
// offsets restart at zero) or truncated. Treating the position as caught up would replay
// nothing, and the comparison would re-emit the spans the lost heartbeats billed under
// new heartbeat ids. The customer would pay twice, so the round refuses, while a stored
// offset exactly at the topic's end stays caught up.
func TestCatchUpRefusesWhenHeartbeatTopicWasRecreated(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "recreated")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	// The stored offset sits exactly at the topic's end: the round stays caught up
	// and bills the next minute.
	p.RoundAt(t, base.Add(3*time.Minute))
	if outboxCount(t, p.Store, p.Tenant) == 0 {
		t.Fatal("a round whose stored offset equals the topic end must stay caught up and bill")
	}
	p.WaitOutboxEmpty(t)

	// The topic was deleted and recreated after the third round: its offsets restarted
	// at zero, so the stored offset now sits past the new topic's end.
	if _, err := p.Store.Pool().Exec(
		context.Background(),
		`UPDATE progress SET heartbeat_offset = 99 WHERE tenant = $1`, p.Tenant,
	); err != nil {
		t.Fatalf("pushing the stored offset past the topic end: %v", err)
	}

	// The round must refuse, naming the stored offset and the topic's end.
	err := p.Round.Once(context.Background(), p.Tenant, time.Now())
	if err == nil ||
		!strings.Contains(err.Error(), "stored heartbeat offset 99") ||
		!strings.Contains(err.Error(), "is past the end 3 of its heartbeat topic") {
		t.Fatalf("expected the round to refuse on the recreated topic, got %v", err)
	}

	// Billing holds: nothing new was sent after the three rounds that succeeded.
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, p.Tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// TestRoundWithSteadyEventStream checks round liveness on a tenant whose events keep
// arriving: the round compares the fold against the watermark it captured when it started
// waiting, so a steady stream still gets rounds instead of chasing a watermark that always
// moves.
func TestRoundWithSteadyEventStream(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "steady")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	// The fold keeps running and events keep arriving while the rounds tick.
	foldCtx, cancelFold := context.WithCancel(context.Background())
	defer cancelFold()
	go func() { _ = p.Fold.RunTenant(foldCtx, p.Tenant) }()

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
				Topic: topics.Events(p.Tenant),
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
		p.RoundAt(t, base.Add(time.Duration(i)*time.Minute))
		if outboxCount(t, p.Store, p.Tenant) > 0 {
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
	p.WaitOutboxEmpty(t)
	if beats := pipelinetest.SentHeartbeats(t, p.Tenant); len(beats) == 0 {
		t.Fatal("the steady-stream tenant sent no heartbeats")
	}
}

// gatingProbe pauses the round inside its first calculator call, so a test can observe
// what the rules gate allows while a tick is mid-run. Every later call passes through:
// the release channel is closed by then.
type gatingProbe struct {
	round.Calculator
	entered chan struct{}
	release chan struct{}
	fired   sync.Once
}

func (g *gatingProbe) probe() {
	g.fired.Do(func() { close(g.entered) })
	<-g.release
}

func (g *gatingProbe) Revisions() []rules.Revision {
	g.probe()
	return g.Calculator.Revisions()
}

func (g *gatingProbe) OwnerOf(resourceType string, facts []database.Fact) (string, string, bool) {
	g.probe()
	return g.Calculator.OwnerOf(resourceType, facts)
}

func (g *gatingProbe) OwnedFieldOf(rootType string) (string, bool) {
	g.probe()
	return g.Calculator.OwnedFieldOf(rootType)
}

func (g *gatingProbe) MeterAt(resourceType, meter string, at time.Time) (rules.Meter, string, bool) {
	g.probe()
	return g.Calculator.MeterAt(resourceType, meter, at)
}

func (g *gatingProbe) Expected(
	tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root, upTo time.Time,
) ([]usage.Piece, error) {
	g.probe()
	return g.Calculator.Expected(tenant, resourceType, resourceID, facts, root, upTo)
}

func (g *gatingProbe) Since(
	tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root,
	from, upTo time.Time, ledger []database.LedgerPiece,
) ([]usage.Piece, error) {
	g.probe()
	return g.Calculator.Since(tenant, resourceType, resourceID, facts, root, from, upTo, ledger)
}

// TestOnceHoldsRulesGateForWholeTick checks the tick side of the rules race: Once holds
// the read side of the rules gate from before its first calculator call to its commit,
// so a rule-revision change — which holds the write side — can neither swap the
// calculator between two calls of one tick nor be approved against a billing position
// the tick is about to advance.
func TestOnceHoldsRulesGateForWholeTick(t *testing.T) {
	tenant := "rulegate"
	store := testenv.Database(t)

	// One RUNNING VM, folded, so the round's comparison reaches its calculator calls.
	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	foldLoop, err := fold.New(store, testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	foldCtx, cancelFold := context.WithCancel(context.Background())
	defer cancelFold()
	go func() { _ = foldLoop.RunTenant(foldCtx, tenant) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		progress, err := store.GetTenantProgress(context.Background(), tenant)
		if err == nil && progress.EventOffset >= int64(total) {
			break
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("reading progress: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the fold never reached the produced events")
		}
		time.Sleep(10 * time.Millisecond)
	}

	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{testenv.DefaultRevision(t)}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	probe := &gatingProbe{
		Calculator: calculator,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	gate := &sync.RWMutex{}
	r, err := round.New(
		store, probe, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(), gate,
	)
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Once(context.Background(), tenant, base.Add(time.Minute)) }()

	// The tick is mid-run, blocked inside a calculator call.
	<-probe.entered

	// A rules change must not be able to start while the tick runs.
	locked := make(chan struct{})
	go func() { gate.Lock(); close(locked) }()
	select {
	case <-locked:
		t.Fatal("a rules change was applied while a round tick was mid-run")
	case <-time.After(100 * time.Millisecond):
		// Still locked out: the tick holds the read side.
	}

	close(probe.release)
	if err := <-done; err != nil {
		t.Fatalf("round at %s: %v", base.Add(time.Minute), err)
	}
	select {
	case <-locked:
	case <-time.After(10 * time.Second):
		t.Fatal("the rules change never proceeded after the tick finished")
	}
}

// TestCatchUpReadsOversizedGroupsWhole checks that a round group larger than the catch-up
// batch still completes: the pass extends beyond the batch while it stays inside one group,
// the bound limits groups per pass, and nothing is applied twice or skipped.
func TestCatchUpReadsOversizedGroupsWhole(t *testing.T) {
	const tenant = "oversized"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	// Backup after the first round.
	p.RoundAt(t, base.Add(time.Minute))
	p.WaitOutboxEmpty(t)
	backup := snapshot(t, p.Store, tenant)

	// Two resizes within one minute: the 10:03 round sends a group of three.
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "medium",
			base.Add(2*time.Minute+20*time.Second)),
		testenv.VMEvent("e3", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+30*time.Second), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+40*time.Second)),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.RoundAt(t, base.Add(5*time.Minute))
	p.WaitOutboxEmpty(t)

	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.FoldAll(t, total)
	restored.Round.SetCatchUpBatch(2)

	// The topic holds five groups after the backup point: 10:02 (one), 10:03 (three),
	// 10:04 (one), 10:05 (one). With a batch of two groups, the first pass applies the
	// 10:02 group and the whole oversized 10:03 group, and stops at the 10:04 witness.
	restored.RoundAt(t, base.Add(6*time.Minute))
	if outboxCount(t, restored.Store, tenant) != 0 {
		t.Fatal("a round with unfinished catch-up must not write heartbeats")
	}
	pieces, err := database.LedgerPiecesWhere(
		context.Background(), restored.Store.Pool(), tenant, "meter = $2", "compute",
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
	restored.RoundAt(t, base.Add(7*time.Minute))
	if outboxCount(t, restored.Store, tenant) != 1 {
		t.Fatalf("expected exactly one difference heartbeat, got %d",
			outboxCount(t, restored.Store, tenant))
	}
	restored.StartRelay(t)
	restored.WaitOutboxEmpty(t)

	// Nothing twice, nothing skipped: the live heartbeats plus one difference for
	// 10:05 to 10:07. Within the 10:03 group the meters arrive in the round's key order.
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
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
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(wantBeats))
	}
}

// TestCatchUpCommitsPerGroup checks the commit granularity catch-up restores from: a pod
// killed mid-pass leaves the round groups that already committed applied — the offset moved
// past them and the ledger holds their usage — so the next pass resumes at the next group
// instead of repeating the identical pass.
func TestCatchUpCommitsPerGroup(t *testing.T) {
	const tenant = "pergroup"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	// The backup knows the first round. After it the VM is resized mid-minute, so the
	// 10:03 round sends a group of two heartbeats and the 10:02 and 10:04 rounds one each:
	// three groups on the topic.
	p.RoundAt(t, base.Add(time.Minute))
	p.WaitOutboxEmpty(t)
	backup := snapshot(t, p.Store, tenant)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+30*time.Second)),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	// The pod dies mid-catch-up: two of the three groups committed, the third never ran.
	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.FoldAll(t, total)
	restored.Round.SetCrashAfterGroups(2)
	if err := restored.Round.Once(context.Background(), tenant, time.Now()); !errors.Is(err, round.ErrCrashed) {
		t.Fatalf("catch-up must report the simulated crash, got %v", err)
	}

	// The first two groups stayed committed. The topic holds the 10:02 heartbeat at offset
	// 1, the 10:03 pair at offsets 2 and 3, and the 10:04 heartbeat at offset 4; two
	// committed groups move the offset to 4, past everything but the last group.
	progress, err := restored.Store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	if progress.HeartbeatOffset != 4 {
		t.Fatalf("two committed groups must move the offset past their records, got %d",
			progress.HeartbeatOffset)
	}
	pieces, err := database.LedgerPiecesWhere(
		context.Background(), restored.Store.Pool(), tenant, "meter = $2", "compute",
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
		t.Fatalf("the committed groups must stay applied:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(lines), pipelinetest.Join(want))
	}

	// The restart resumes at the next group: a round without the crash finishes catch-up
	// and bills the difference, and nothing is applied twice or skipped.
	restored.Round.SetCrashAfterGroups(0)
	restored.RoundAt(t, base.Add(5*time.Minute))
	if outboxCount(t, restored.Store, tenant) == 0 {
		t.Fatal("a finished catch-up must let the round send its difference")
	}
	restored.StartRelay(t)
	restored.WaitOutboxEmpty(t)

	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	wantBeats := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z 30.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
		"compute 2026-10-05T10:04:00Z..2026-10-05T10:05:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(wantBeats) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(wantBeats))
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
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	// The same backlog as the per-group test: a backup after the first round, then a
	// mid-minute resize, so the topic holds three round groups after the backup point.
	p.RoundAt(t, base.Add(time.Minute))
	p.WaitOutboxEmpty(t)
	backup := snapshot(t, p.Store, tenant)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(2*time.Minute+30*time.Second)),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	// The crash: a restored database dies mid-catch-up, after two of the three groups
	// committed.
	crashed := pipelinetest.NewPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	crashed.Round.SetCrashAfterGroups(2)
	if err := crashed.Round.Once(context.Background(), tenant, time.Now()); !errors.Is(err, round.ErrCrashed) {
		t.Fatalf("catch-up must report the simulated crash, got %v", err)
	}

	// The one-pass result: a database restored from the same snapshot catches up in one
	// uninterrupted pass. The comparison stays out of the way, so the ledger is exactly
	// what catch-up produced.
	reference := pipelinetest.NewPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	reference.RoundAt(t, base.Add(5*time.Minute))

	// The database is restored from the snapshot taken before the crash and replays the
	// same groups. The final ledger must equal the one-pass result.
	replayed := pipelinetest.NewPipelineOnStore(t, tenant, restoreForCatchUpOnly(t, backup, tenant))
	replayed.RoundAt(t, base.Add(5*time.Minute))

	got := ledgerLines(t, replayed.Store, tenant)
	want := ledgerLines(t, reference.Store, tenant)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("a restore to the pre-crash snapshot must replay the same groups to the same ledger:\ngot:\n%s\nwant:\n%s",
			pipelinetest.Join(got), pipelinetest.Join(want))
	}
	replayedProgress, err := replayed.Store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading replayed progress: %v", err)
	}
	referenceProgress, err := reference.Store.GetTenantProgress(context.Background(), tenant)
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
	if fmt.Sprint(ledgerLines(t, crashed.Store, tenant)) == fmt.Sprint(want) {
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
	pieces, err := database.LedgerPiecesWhere(context.Background(), store.Pool(), tenant, "")
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
	history  []historyFact
	ledger   []database.LedgerPiece
	outbox   []database.OutboxRow
	progress database.Progress
}

// historyFact is one history row with the event offset the fold wrote it at. A backup
// must restore both: the offset decides which facts a later round's comparison still
// sees as new.
type historyFact struct {
	fact   database.Fact
	offset int64
}

func snapshot(t *testing.T, store *database.Store, tenant string) tenantSnapshot {
	t.Helper()
	ctx := context.Background()
	progress, err := store.GetTenantProgress(ctx, tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	var snap tenantSnapshot
	snap.progress = progress
	historyRows, err := store.Pool().Query(
		ctx,
		`SELECT resource_type, resource_id, field, since, value, event_offset
		 FROM history WHERE tenant = $1
		 ORDER BY resource_type, resource_id, field, since`, tenant,
	)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	defer historyRows.Close()
	for historyRows.Next() {
		var entry historyFact
		if err := historyRows.Scan(
			&entry.fact.ResourceType, &entry.fact.ResourceID, &entry.fact.Field,
			&entry.fact.Since, &entry.fact.Value, &entry.offset,
		); err != nil {
			t.Fatalf("scanning history: %v", err)
		}
		snap.history = append(snap.history, entry)
	}
	if err := historyRows.Err(); err != nil {
		t.Fatalf("reading history: %v", err)
	}
	ledger, err := database.LedgerPiecesWhere(ctx, store.Pool(), tenant, "")
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	snap.ledger = ledger
	// The outbox is part of the backup: a restore must resend what the relay had not
	// sent yet. The rows copy in seq order, the order the relay sends in.
	outboxRows, err := store.Pool().Query(
		ctx,
		`SELECT seq, id, payload, round_time FROM outbox WHERE tenant = $1 ORDER BY seq`, tenant,
	)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	defer outboxRows.Close()
	for outboxRows.Next() {
		var row database.OutboxRow
		if err := outboxRows.Scan(&row.Seq, &row.ID, &row.Payload, &row.RoundTime); err != nil {
			t.Fatalf("scanning outbox: %v", err)
		}
		snap.outbox = append(snap.outbox, row)
	}
	if err := outboxRows.Err(); err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
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
	// Facts restore at the event offset the backup folded them at. Facts of one event
	// share an offset and InsertFacts writes a whole call at one offset, so the
	// restore groups the facts by theirs.
	groups := map[int64][]database.Fact{}
	for _, entry := range snap.history {
		groups[entry.offset] = append(groups[entry.offset], entry.fact)
	}
	offsets := make([]int64, 0, len(groups))
	for offset := range groups {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	for _, offset := range offsets {
		if err := database.InsertFacts(ctx, tx, tenant, groups[offset], offset); err != nil {
			t.Fatalf("restoring history: %v", err)
		}
	}
	// Every piece of a key restores together: SyncLedger replaces a key's whole
	// history, so restoring piece by piece would keep only the last one.
	byKey := map[database.LedgerKey][]database.LedgerPiece{}
	for _, piece := range snap.ledger {
		byKey[piece.Key] = append(byKey[piece.Key], piece)
	}
	for key, pieces := range byKey {
		if err := database.SyncLedger(ctx, tx, tenant, key, pieces, time.Time{}); err != nil {
			t.Fatalf("restoring ledger: %v", err)
		}
	}
	if err := database.EnqueueHeartbeats(ctx, tx, tenant, snap.outbox); err != nil {
		t.Fatalf("restoring outbox: %v", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE progress SET event_offset = $2, heartbeat_offset = $3, last_round_time = $4,
		 last_round_event_offset = $5 WHERE tenant = $1`,
		tenant, snap.progress.EventOffset, snap.progress.HeartbeatOffset,
		snap.progress.LastRoundTime, snap.progress.LastRoundEventOffset,
	); err != nil {
		t.Fatalf("restoring progress: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return store
}

// TestBackupFixturePreservesAllLedgerPiecesAndPendingOutbox checks the backup
// fixture itself: snapshot and restore must copy every ledger piece of a key and the
// outbox rows the relay had not sent yet, not just the last piece of each key.
func TestBackupFixturePreservesAllLedgerPiecesAndPendingOutbox(t *testing.T) {
	const tenant = "backup-fixture"
	ctx := context.Background()
	store := testenv.Database(t)
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	key := database.LedgerKey{
		ResourceType: "ComputeInstance", ResourceID: "vm-7", Meter: "compute", Component: "compute",
		Dimensions: `{"deployment":"review","tenant":"` + tenant +
			`","meter_type":"second","instance_type":"small"}`,
	}
	pieces := []database.LedgerPiece{
		{Key: key, StartTime: base, EndTime: base.Add(time.Minute), QuantityMicro: 60_000_000},
		{Key: key, StartTime: base.Add(2 * time.Minute), EndTime: base.Add(3 * time.Minute), QuantityMicro: 60_000_000},
	}
	if err := database.SyncLedger(ctx, store.Pool(), tenant, key, pieces, time.Time{}); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	event, err := heartbeat.New(tenant, key, pieces[1].StartTime, pieces[1].EndTime,
		pieces[1].QuantityMicro, "second", pieces[1].EndTime, 3, "0001")
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	if err := database.EnqueueHeartbeats(ctx, store.Pool(), tenant, []database.OutboxRow{
		{ID: event.ID(), Payload: payload, RoundTime: pieces[1].EndTime},
	}); err != nil {
		t.Fatalf("enqueueing the heartbeat: %v", err)
	}
	restored := restore(t, snapshot(t, store, tenant), tenant)
	actualPieces, err := database.LedgerPiecesWhere(ctx, restored.Pool(), tenant, "")
	if err != nil {
		t.Fatalf("reading the restored ledger: %v", err)
	}
	actualOutbox, err := database.NextOutbox(ctx, restored.Pool(), tenant, 10)
	if err != nil {
		t.Fatalf("reading the restored outbox: %v", err)
	}
	if len(actualPieces) != 2 || len(actualOutbox) != 1 {
		t.Fatalf("the backup fixture must keep every piece and the pending outbox: "+
			"ledger pieces=%d, want 2; pending outbox=%d, want 1",
			len(actualPieces), len(actualOutbox))
	}
}

func outboxCount(t *testing.T, store *database.Store, tenant string) int {
	t.Helper()
	rows, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 1000)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	return len(rows)
}

// TestMidnightSplitsHeartbeatsAcrossDays checks that the round's difference merge never
// rejoins what the engine split at UTC midnight: rounds at 23:59:30 and 00:00:30 across a
// month boundary send two heartbeats, each dated inside its own billing day.
func TestMidnightSplitsHeartbeatsAcrossDays(t *testing.T) {
	const tenant = "midnight"
	evening := time.Date(2026, 9, 30, 23, 58, 0, 0, time.UTC)
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			evening, evening, "web", "RUNNING", evening, "small", evening),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, evening.Add(90*time.Second))  // 23:59:30
	p.RoundAt(t, evening.Add(150*time.Second)) // 00:00:30 the next month
	p.WaitOutboxEmpty(t)

	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, p.Tenant))
	want := []string{
		"compute 2026-09-30T23:58:00Z..2026-09-30T23:59:30Z 90.000000",
		"compute 2026-09-30T23:59:30Z..2026-10-01T00:00:00Z 30.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z 30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
	// Each heartbeat is dated inside its own day: the CloudEvent time is the interval's
	// start, and a piece crossing midnight at month end would otherwise fall entirely
	// into one month.
	for _, beat := range pipelinetest.SentHeartbeats(t, p.Tenant) {
		from, err := time.Parse(time.RFC3339Nano, beat.Data.Usage.From)
		if err != nil {
			t.Fatalf("parsing usage from: %v", err)
		}
		to, err := time.Parse(time.RFC3339Nano, beat.Data.Usage.To)
		if err != nil {
			t.Fatalf("parsing usage to: %v", err)
		}
		if from.UTC().YearDay() != to.Add(-time.Nanosecond).UTC().YearDay() {
			t.Fatalf("a heartbeat crossed midnight: %s..%s", beat.Data.Usage.From,
				beat.Data.Usage.To)
		}
	}
}

// TestRevisionBoundarySplitsHeartbeats checks the same rule at a revision boundary: a meter
// billable at the same rate across the boundary sends one heartbeat per revision, each
// carrying the revision that billed it.
func TestRevisionBoundarySplitsHeartbeats(t *testing.T) {
	const tenant = "revsplit"
	first := []rules.Revision{testenv.DefaultRevision(t)}
	second, err := rules.Parse(
		rules.NamePrefix+"0002", base.Add(90*time.Second).Format(time.RFC3339),
		first[0].MetersYAML,
	)
	if err != nil {
		t.Fatalf("parsing second revision: %v", err)
	}
	p := pipelinetest.NewPipelineWithRevisions(t, tenant, testenv.Database(t), []rules.Revision{first[0], second})
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	got := pipelinetest.BeatLines(beats)
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:01:30Z 30.000000",
		"compute 2026-10-05T10:01:30Z..2026-10-05T10:02:00Z 30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
	if beats[1].Data.RulesRevision != "0001" || beats[2].Data.RulesRevision != "0002" {
		t.Fatalf(
			"heartbeats across a revision boundary must carry their billing revision: %s then %s",
			beats[1].Data.RulesRevision, beats[2].Data.RulesRevision,
		)
	}
}

// TestLedgerGrowsOneRowPerDay checks the design's performance rule: a meter billed across
// several days grows its ledger by one row per day, and a round extends the tail row without
// rewriting the rows beneath it.
func TestLedgerGrowsOneRowPerDay(t *testing.T) {
	const tenant = "growth"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	// One round per day, late in each day so every day's midnight falls between rounds.
	// Each round after the first crosses one midnight, so the ledger holds one row per
	// day spanned: five rows for the four midnights the first four rounds crossed.
	for day := 1; day <= 4; day++ {
		p.RoundAt(t, base.Add(time.Duration(day)*24*time.Hour-time.Hour))
	}
	p.WaitOutboxEmpty(t)

	rowsBefore := ledgerRowVersions(t, p.Store, tenant)
	if len(rowsBefore) != 5 {
		t.Fatalf("a meter billed across five days must have one row per day, got %d",
			len(rowsBefore))
	}

	// The next round extends only the tail row: the rows beneath it keep their row
	// versions, so they were not rewritten.
	p.RoundAt(t, base.Add(5*24*time.Hour-time.Hour))
	p.WaitOutboxEmpty(t)

	rowsAfter := ledgerRowVersions(t, p.Store, tenant)
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
	for _, beat := range pipelinetest.SentHeartbeats(t, p.Tenant) {
		quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
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
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		// The operator reports a resize to large, effective 10:30.
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(30*time.Minute), base, "web", "RUNNING", base, "large",
			base.Add(30*time.Minute)),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(31*time.Minute))
	p.RoundAt(t, base.Add(32*time.Minute))
	p.RoundAt(t, base.Add(33*time.Minute))
	p.WaitOutboxEmpty(t)

	// The late event: the VM had stopped at 10:29:30, before the resize took effect.
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e3", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(34*time.Minute), base, "web", "STOPPED",
			base.Add(29*time.Minute+30*time.Second), "large", base.Add(30*time.Minute)),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(34*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	var small, large []schema.HeartbeatData
	for _, beat := range beats {
		switch beat.Data.BillingDimensions["instance_type"] {
		case "small":
			small = append(small, beat.Data)
		case "large":
			large = append(large, beat.Data)
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
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(time.Minute))
	p.WaitOutboxEmpty(t)

	// A fresh database that never folded the events: its event offset claims everything
	// is folded, but history is empty, as a restore whose backup had already consumed
	// events that retention has since deleted would leave it. Catch-up rebuilds the
	// ledger from the heartbeat alone.
	q := pipelinetest.NewPipeline(t, tenant)
	if err := q.Store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	if _, err := q.Store.Pool().Exec(
		context.Background(), `UPDATE progress SET event_offset = $2 WHERE tenant = $1`,
		tenant, int64(total),
	); err != nil {
		t.Fatalf("advancing the event offset: %v", err)
	}
	q.StartRelay(t)
	q.RoundAt(t, base.Add(2*time.Minute))
	q.WaitOutboxEmpty(t)

	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z -60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
	assertLedgerEmpty(t, q.Store, tenant)
}

// TestStateChangeAtRoundTimeIsNotANewFact pins the calculator's half-open boundary: a
// state change dated exactly at the previous round time changes nothing inside the window
// that round billed, because the stop at 10:01 closes the run at that moment and leaves
// nothing billable after it. The restored database has no last round of its own, so its
// first comparison reaches the resource through the recompute path, scanning facts and
// ledger rows alike, and reverses the usage the replayed heartbeat rebuilt.
func TestStateChangeAtRoundTimeIsNotANewFact(t *testing.T) {
	const tenant = "boundary"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)
	previous := base.Add(time.Minute)
	p.RoundAt(t, previous)
	p.WaitOutboxEmpty(t)

	// The restored database knows the creation and a stop dated exactly at the previous
	// round's time, but not the RUNNING stretch the heartbeat billed: the facts predate
	// the round's event offset and none is dated after its time, so none is new.
	q := pipelinetest.NewPipeline(t, tenant)
	if err := q.Store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	tx, err := q.Store.Begin(context.Background())
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
	q.StartRelay(t)
	q.RoundAt(t, base.Add(2*time.Minute))
	q.WaitOutboxEmpty(t)

	// The open row catch-up rebuilt is unjustified — the engine never saw a billable
	// state — and only the tail path's fallback compares it, reversing the usage.
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z -60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
	assertLedgerEmpty(t, q.Store, tenant)
}

// TestRunningAtRoundBoundaryIsNotLost pins the scan's inclusive bound. A VM whose RUNNING
// fact is dated exactly at the last round time opens its span inside the window the next
// round bills, so the scan must still select it: the boundary round consumed the fact but
// wrote no ledger row for it, because the span inside its own window was empty, so the
// tail path cannot find the resource either.
func TestRunningAtRoundBoundaryIsNotLost(t *testing.T) {
	const tenant = "boundary-running"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	// The VM boots from 10:00 and starts RUNNING exactly at 10:01, the moment of the
	// next round.
	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "small", base),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base)

	boundary := base.Add(time.Minute)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			boundary, base, "web", "RUNNING", boundary, "small", base),
	})
	p.FoldAll(t, total)

	// The round at the boundary reaches the RUNNING fact through its event offset, but
	// its window ends where the run starts: the span is empty and nothing bills.
	p.RoundAt(t, boundary)
	p.WaitOutboxEmpty(t)
	if beats := pipelinetest.SentHeartbeats(t, tenant); len(beats) != 0 {
		t.Fatalf("the round at the boundary must bill nothing, got %d heartbeats", len(beats))
	}

	// The next round must find the VM through the scan and bill the minute that
	// follows the boundary.
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// TestRunningAtRoundBoundaryBillsEachMinuteOnce runs the same boundary over more rounds:
// after the boundary the VM's fact sits at the last round's event offset, so every round
// scans it into the recompute list while its open ledger row would also select it for the
// tail. The scan dedups the two lists and the recompute is idempotent against the ledger,
// so each minute after the boundary bills exactly once.
func TestRunningAtRoundBoundaryBillsEachMinuteOnce(t *testing.T) {
	const tenant = "boundary-running-long"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "small", base),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base)

	boundary := base.Add(time.Minute)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			boundary, base, "web", "RUNNING", boundary, "small", base),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, boundary)
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	want := []string{
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// TestRootReadyAtRoundBoundaryIsNotLost pins the same boundary on an ownership root: a
// cluster whose READY fact is dated exactly at the last round time must reach the next
// round through the scan and drag its workers with it, because the gate that opened at
// the boundary bills their runs retroactively from their start. When the cluster is lost,
// the workers' gate never opens: they have no new fact of their own and no ledger row.
func TestRootReadyAtRoundBoundaryIsNotLost(t *testing.T) {
	const tenant = "boundary-root"
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	// The cluster exists from 10:00 in an unspecified state and the worker is
	// allocated while the gate is shut: control_plane bills only from READY, so
	// nothing before it is billable.
	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.ClusterEvent("c1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "UNSPECIFIED", base, "openshift-4-19", "4.19.0", base),
		testenv.BMIEvent("b1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "medium", "cluster-1", "workers"),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base)

	// The cluster reaches READY exactly at 10:01, the moment of the next round.
	boundary := base.Add(time.Minute)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.ClusterEvent("c2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			boundary, base, "web", "READY", boundary, "openshift-4-19", "4.19.0", base),
	})
	p.FoldAll(t, total)

	// The round at the boundary consumes the READY fact, but a gate opens only
	// strictly inside a run, and every run it can see ends at the boundary: nothing
	// bills.
	p.RoundAt(t, boundary)
	p.WaitOutboxEmpty(t)
	if beats := pipelinetest.SentHeartbeats(t, tenant); len(beats) != 0 {
		t.Fatalf("the round at the boundary must bill nothing, got %d heartbeats", len(beats))
	}

	// The next round must bill the cluster from the boundary and the worker from its
	// own start under the opened gate. The two heartbeats come from different
	// resources, whose recompute order the scan does not fix, so the lines compare
	// sorted.
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)
	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, tenant))
	sort.Strings(got)
	want := []string{
		"allocation 2026-10-05T10:00:00Z..2026-10-05T10:02:00Z 120.000000",
		"control_plane 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// TestNegativeCorrectionSplitsAtMidnight covers a negative difference that would merge across
// UTC midnight: a late stop leaves usage overbilled on both sides of midnight, and the
// correction must arrive as one heartbeat per billing day, never crossing midnight.
func TestNegativeCorrectionSplitsAtMidnight(t *testing.T) {
	const tenant = "neg-midnight"
	evening := time.Date(2026, 9, 30, 23, 58, 0, 0, time.UTC)
	p := pipelinetest.NewPipeline(t, tenant)
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			evening, evening, "web", "RUNNING", evening, "small", evening),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, evening.Add(90*time.Second))  // 23:59:30
	p.RoundAt(t, evening.Add(150*time.Second)) // 00:00:30 the next month
	p.WaitOutboxEmpty(t)

	// The late event: the VM stopped at 23:59:00, before both rounds billed it.
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			evening.Add(3*time.Minute), evening, "web", "STOPPED",
			evening.Add(time.Minute), "small", evening),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, evening.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, tenant)
	got := pipelinetest.BeatLines(beats)
	want := []string{
		"compute 2026-09-30T23:58:00Z..2026-09-30T23:59:30Z 90.000000",
		"compute 2026-09-30T23:59:30Z..2026-10-01T00:00:00Z 30.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z 30.000000",
		"compute 2026-09-30T23:59:00Z..2026-10-01T00:00:00Z -60.000000",
		"compute 2026-10-01T00:00:00Z..2026-10-01T00:00:30Z -30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}

	// The corrections net to exactly the true running time: one minute.
	var totalMicro int64
	for _, beat := range beats {
		quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		totalMicro += quantity
	}
	if totalMicro != int64(time.Minute.Seconds())*1_000_000 {
		t.Fatalf("heartbeats sum to %d micro, want one minute", totalMicro)
	}
	for _, beat := range beats {
		from, err := time.Parse(time.RFC3339Nano, beat.Data.Usage.From)
		if err != nil {
			t.Fatalf("parsing usage from: %v", err)
		}
		to, err := time.Parse(time.RFC3339Nano, beat.Data.Usage.To)
		if err != nil {
			t.Fatalf("parsing usage to: %v", err)
		}
		if from.UTC().YearDay() != to.Add(-time.Nanosecond).UTC().YearDay() {
			t.Fatalf("a heartbeat crossed midnight: %s..%s", beat.Data.Usage.From,
				beat.Data.Usage.To)
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
	p := pipelinetest.NewPipeline(t, "late-resize")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.WaitOutboxEmpty(t)

	// The resize happened at 10:02:30 but arrives after the 10:03 round.
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+10*time.Second), base, "web", "RUNNING",
			base, "large", base.Add(2*time.Minute+30*time.Second)),
	})
	p.FoldAll(t, total)
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	got := pipelinetest.BeatLines(pipelinetest.SentHeartbeats(t, p.Tenant))
	want := []string{
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:04:00Z 90.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z -30.000000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// pendingHeartbeats returns the outbox rows waiting for delivery.
func pendingHeartbeats(t *testing.T, store *database.Store, tenant string) []database.OutboxRow {
	t.Helper()
	rows, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 1000)
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	return rows
}

// rowResource returns the resource a pending heartbeat bills.
func rowResource(t *testing.T, row database.OutboxRow) string {
	t.Helper()
	event := cloudevents.Event{}
	if err := event.UnmarshalJSON(row.Payload); err != nil {
		t.Fatalf("unmarshaling the pending heartbeat: %v", err)
	}
	data, err := heartbeat.Data(event)
	if err != nil {
		t.Fatalf("decoding the pending heartbeat: %v", err)
	}
	return data.ResourceID
}

// rowOf returns the pending heartbeat billing the given resource at the given round.
func rowOf(t *testing.T, rows []database.OutboxRow, resource string, round time.Time) database.OutboxRow {
	t.Helper()
	for _, row := range rows {
		if rowResource(t, row) == resource && row.RoundTime.Equal(round) {
			return row
		}
	}
	t.Fatalf("no pending heartbeat for %s at %s", resource, round)
	return database.OutboxRow{}
}

// deliver publishes outbox rows to the heartbeat topic, standing in for the relay.
// Delivering the same row twice reproduces a retry's copy: the same heartbeat id lands
// on the topic at two offsets.
func deliver(t *testing.T, tenant string, rows ...database.OutboxRow) {
	t.Helper()
	producer := testenv.NewProducer(t)
	for _, row := range rows {
		_, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topics.Heartbeat(tenant),
			Key:   sarama.StringEncoder(row.ID),
			Value: sarama.ByteEncoder(row.Payload),
		})
		if err != nil {
			t.Fatalf("delivering heartbeat %s: %v", row.ID, err)
		}
	}
}

// billedMicro sums the heartbeats the topic holds for one resource, counting each
// heartbeat id once: the provider drops copies by id, so a retry's copy does not bill.
func billedMicro(t *testing.T, tenant, resource string) int64 {
	t.Helper()
	seen := map[string]bool{}
	var total int64
	for _, beat := range pipelinetest.SentHeartbeats(t, tenant) {
		if beat.Data.ResourceID != resource || seen[beat.Event.ID()] {
			continue
		}
		seen[beat.Event.ID()] = true
		quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing the quantity of heartbeat %s: %v", beat.Event.ID(), err)
		}
		total += quantity
	}
	return total
}

// TestRebuildRecoversAPartiallyDeliveredRound checks the recovery of a round the relay
// half delivered: one VM's bill reached the topic, the other's lived only in the
// database, and the database was lost. The rebuild must still bill both runs.
func TestRebuildRecoversAPartiallyDeliveredRound(t *testing.T) {
	tenant := "partial-round"
	p := pipelinetest.NewPipeline(t, tenant)

	// Two VMs each run for 20 seconds.
	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEventOf("vm-web", "w1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		testenv.VMEventOf("vm-web", "w2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(20*time.Second), base, "web", "STOPPED", base.Add(20*time.Second), "small", base),
		testenv.VMEventOf("vm-db", "d1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		testenv.VMEventOf("vm-db", "d2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(20*time.Second), base, "web", "STOPPED", base.Add(20*time.Second), "small", base),
	})
	p.FoldAll(t, total)
	backup := snapshot(t, p.Store, tenant)

	// The round bills both runs. Only vm-web's heartbeat is delivered before the
	// database is lost.
	p.RoundAt(t, base.Add(time.Minute))
	rows := pendingHeartbeats(t, p.Store, tenant)
	if len(rows) != 2 {
		t.Fatalf("the round must bill both VMs, got %d heartbeats", len(rows))
	}
	deliver(t, tenant, rowOf(t, rows, "vm-web", base.Add(time.Minute)))

	// The database is restored from before the round; two later rounds must recover
	// the undelivered bill exactly once.
	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.StartRelay(t)
	restored.RoundAt(t, base.Add(2*time.Minute))
	restored.RoundAt(t, base.Add(3*time.Minute))
	restored.WaitOutboxEmpty(t)

	if got := billedMicro(t, tenant, "vm-web"); got != 20_000_000 {
		t.Fatalf("vm-web billed %d micro, want 20000000", got)
	}
	if got := billedMicro(t, tenant, "vm-db"); got != 20_000_000 {
		t.Fatalf("a round the relay half delivered must still bill the lost half "+
			"exactly once: vm-db billed %d micro, want 20000000", got)
	}
}

// TestRebuildBillsARunWithoutAHeartbeat checks that a rebuild bills a VM whose run opened
// at the round's own second: the first round had no elapsed run to bill, so no heartbeat
// of the VM's own reached the topic, and the run's whole bill lives only in the history
// the restored database keeps.
func TestRebuildBillsARunWithoutAHeartbeat(t *testing.T) {
	tenant := "pending-run"
	p := pipelinetest.NewPipeline(t, tenant)

	// vm-mature has run two minutes; vm-young started at the round's own second, so
	// the round has no elapsed run of its own to bill yet.
	young := base.Add(2 * time.Minute)
	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEventOf("vm-mature", "m1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		testenv.VMEventOf("vm-young", "y1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			young, young, "web", "RUNNING", young, "small", young),
	})
	p.FoldAll(t, total)
	backup := snapshot(t, p.Store, tenant)

	// The round bills vm-mature only: vm-young's run opens at the round's own second,
	// so it emits no heartbeat. The round is fully delivered, then the database is lost.
	p.RoundAt(t, base.Add(2*time.Minute))
	rows := pendingHeartbeats(t, p.Store, tenant)
	if len(rows) != 1 {
		t.Fatalf("a run that opens at the round's own second must emit no heartbeat, "+
			"got %d", len(rows))
	}
	deliver(t, tenant, rows...)

	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.StartRelay(t)
	restored.RoundAt(t, base.Add(3*time.Minute))
	restored.RoundAt(t, base.Add(4*time.Minute))
	restored.WaitOutboxEmpty(t)

	if got := billedMicro(t, tenant, "vm-mature"); got != 240_000_000 {
		t.Fatalf("vm-mature billed %d micro, want 240000000", got)
	}
	if got := billedMicro(t, tenant, "vm-young"); got != 120_000_000 {
		t.Fatalf("a run without a heartbeat of its own the rebuild must still bill: "+
			"vm-young billed %d micro, want 120000000", got)
	}
}

// TestRebuildAppliesEveryHeartbeatOfARound checks that a relay retry cannot make the
// rebuild drop a bill: the retry lands copies between the heartbeats of two rounds, and
// the replay must apply every distinct bill while counting each copy once.
func TestRebuildAppliesEveryHeartbeatOfARound(t *testing.T) {
	tenant := "retried-batch"
	p := pipelinetest.NewPipeline(t, tenant)

	// vm-web runs from 10:00; vm-db starts at 10:01:30, so the first round bills
	// only vm-web.
	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEventOf("vm-web", "w1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
		testenv.VMEventOf("vm-db", "d1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base.Add(90*time.Second), base.Add(90*time.Second), "web", "RUNNING",
			base.Add(90*time.Second), "small", base.Add(90*time.Second)),
	})
	p.FoldAll(t, total)
	backup := snapshot(t, p.Store, tenant)

	// Round 10:01 bills vm-web; round 10:02 bills both. A failed batch and its retry
	// leave the topic reading web-1, web-2, web-1-copy, web-2-copy, db-2: vm-db's bill
	// arrives after copies of the earlier ones.
	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	rows := pendingHeartbeats(t, p.Store, tenant)
	if len(rows) != 3 {
		t.Fatalf("the two rounds must bill three heartbeats, got %d", len(rows))
	}
	webOne := rowOf(t, rows, "vm-web", base.Add(time.Minute))
	webTwo := rowOf(t, rows, "vm-web", base.Add(2*time.Minute))
	dbTwo := rowOf(t, rows, "vm-db", base.Add(2*time.Minute))
	deliver(t, tenant, webOne, webTwo, webOne, webTwo, dbTwo)

	// vm-db stops 20 seconds into the third minute, after 110 seconds of running;
	// the last round settles both VMs.
	total += testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEventOf("vm-db", "d2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			base.Add(3*time.Minute+20*time.Second), base.Add(90*time.Second), "web", "STOPPED",
			base.Add(3*time.Minute+20*time.Second), "small", base.Add(90*time.Second)),
	})

	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.FoldAll(t, total)
	restored.StartRelay(t)
	restored.RoundAt(t, base.Add(3*time.Minute))
	restored.RoundAt(t, base.Add(4*time.Minute))
	restored.WaitOutboxEmpty(t)

	if got := billedMicro(t, tenant, "vm-web"); got != 240_000_000 {
		t.Fatalf("vm-web billed %d micro, want 240000000", got)
	}
	if got := billedMicro(t, tenant, "vm-db"); got != 110_000_000 {
		t.Fatalf("vm-db ran 110 seconds and must bill exactly that once, billed "+
			"%d micro", got)
	}
}

// TestRebuildCountsAResentCopyOnceAcrossACrash checks the id memory's survival of a crash
// mid-replay: a heartbeat the relay resent after the crash must not merge into the ledger a
// second time, even though the resumed pass reads it with a fresh in-memory view.
func TestRebuildCountsAResentCopyOnceAcrossACrash(t *testing.T) {
	tenant := "crashed-replay"
	p := pipelinetest.NewPipeline(t, tenant)

	total := testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEventOf("vm-web", "w1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)
	backup := snapshot(t, p.Store, tenant)

	// Two rounds bill vm-web once a minute; the relay's first bill is also resent as
	// a copy that lands after the second bill.
	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	rows := pendingHeartbeats(t, p.Store, tenant)
	if len(rows) != 2 {
		t.Fatalf("the two rounds must bill two heartbeats, got %d", len(rows))
	}
	webOne := rowOf(t, rows, "vm-web", base.Add(time.Minute))
	webTwo := rowOf(t, rows, "vm-web", base.Add(2*time.Minute))
	deliver(t, tenant, webOne, webTwo, webOne)

	// The rebuild crashes after applying the two bills; the resumed pass reads the
	// copy, which the id memory must skip.
	restored := pipelinetest.NewPipelineOnStore(t, tenant, restore(t, backup, tenant))
	restored.StartRelay(t)
	restored.Round.SetCrashAfterGroups(2)
	if err := restored.Round.Once(context.Background(), tenant, base.Add(3*time.Minute)); !errors.Is(err, round.ErrCrashed) {
		t.Fatalf("catch-up must report the simulated crash, got %v", err)
	}
	restored.Round.SetCrashAfterGroups(0)
	restored.RoundAt(t, base.Add(3*time.Minute))
	restored.WaitOutboxEmpty(t)

	if got := billedMicro(t, tenant, "vm-web"); got != 180_000_000 {
		t.Fatalf("a copy resent after a crash mid-replay must count once: vm-web "+
			"billed %d micro, want 180000000", got)
	}
}
