/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package pipeline_test wires the fold, the round, and the relay together in one process,
// against the real PostgreSQL and Kafka containers, and asserts the heartbeats the whole
// pipeline puts on a tenant's heartbeat topic. It is the billing sanity tier: the loops are
// tested separately elsewhere, and the chaos scenarios belong to their own suites. This one
// answers a single question, does the system, end to end, bill what it should.
package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"

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

// TestMain stops the shared containers after the tests, so their output pipes close before
// the test binary exits.
func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

// deploymentID is the dimension every meter sends; any fixed value works.
const deploymentID = "us-east"

// sentBeat is one heartbeat read from a tenant's heartbeat topic.
type sentBeat struct {
	event cloudevents.Event
	data  schema.HeartbeatData
}

// pipeline bundles a fold, a round, and a relay against one database, with a clock the test
// controls. It is the same wiring the service runs, minus tenant discovery.
type pipeline struct {
	tenant string
	store  *database.Store
	fold   *fold.Fold
	round  *round.Round
	relay  *relay.Relay
}

func newPipeline(t *testing.T, tenant string) *pipeline {
	t.Helper()
	store := testenv.Database(t)
	revision, err := rules.DefaultRevision()
	if err != nil {
		t.Fatalf("parsing default revision: %v", err)
	}
	engine, err := usage.NewEngine([]rules.Revision{revision}, deploymentID)
	if err != nil {
		t.Fatalf("creating engine: %v", err)
	}
	foldLoop, err := fold.New(store, testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	roundLoop, err := round.New(
		store, engine, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(),
	)
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}
	relayLoop, err := relay.New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	return &pipeline{
		tenant: tenant, store: store, fold: foldLoop, round: roundLoop, relay: relayLoop,
	}
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
func (p *pipeline) sentHeartbeats(t *testing.T) []sentBeat {
	t.Helper()
	var result []sentBeat
	for _, value := range testenv.ConsumeAll(t, topics.Heartbeat(p.tenant)) {
		event := cloudevents.Event{}
		if err := event.UnmarshalJSON(value); err != nil {
			t.Fatalf("unmarshaling heartbeat: %v", err)
		}
		data, err := heartbeat.Data(event)
		if err != nil {
			t.Fatalf("decoding heartbeat: %v", err)
		}
		result = append(result, sentBeat{event: event, data: data})
	}
	return result
}

// lines renders the heartbeats as comparable text, sorted so the order inside one round's
// group does not matter.
func lines(beats []sentBeat) []string {
	var result []string
	for _, beat := range beats {
		result = append(result, fmt.Sprintf(
			"%s %s..%s %s", beat.data.Meter, beat.data.Usage.From, beat.data.Usage.To,
			beat.data.Usage.Quantity,
		))
	}
	sort.Strings(result)
	return result
}

// wantLines renders expected heartbeats in the same shape.
func wantLines(want ...string) []string { return want }

// assertBeats compares the sent heartbeats with the wanted lines.
func assertBeats(t *testing.T, beats []sentBeat, want []string) {
	t.Helper()
	got := lines(beats)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

func join(parts []string) string {
	out := ""
	for _, part := range parts {
		out += part + "\n"
	}
	return out
}

// checkEnvelope asserts one heartbeat's full envelope: the CloudEvent attributes and
// extensions the adapters and the e2e tooling filter on, the payload's identity, interval
// semantics, and precision fields, and the deployment dimension every meter sends.
func checkEnvelope(
	t *testing.T, beat sentBeat, tenant, resourceID, resourceType, meter, unit string,
) {
	t.Helper()
	event := beat.event
	data := beat.data
	if event.Type() != schema.HeartbeatType {
		t.Fatalf("heartbeat type is %q, want %q", event.Type(), schema.HeartbeatType)
	}
	if event.Source() != "osac-metering" {
		t.Fatalf("heartbeat source is %q", event.Source())
	}
	extensions := event.Extensions()
	if got := fmt.Sprintf("%v", extensions[schema.ExtResourceID]); got != resourceID {
		t.Fatalf("extension %s is %q, want %q", schema.ExtResourceID, got, resourceID)
	}
	if got := fmt.Sprintf("%v", extensions[schema.ExtResourceType]); got != resourceType {
		t.Fatalf("extension %s is %q, want %q", schema.ExtResourceType, got, resourceType)
	}
	if got := fmt.Sprintf("%v", extensions[schema.ExtTenant]); got != tenant {
		t.Fatalf("extension %s is %q, want %q", schema.ExtTenant, got, tenant)
	}
	if data.TenantID != tenant || data.ResourceID != resourceID || data.ResourceType != resourceType {
		t.Fatalf("heartbeat identity is %s/%s/%s", data.TenantID, data.ResourceType, data.ResourceID)
	}
	if data.Meter != meter || data.Component != meter {
		t.Fatalf("heartbeat meter is %s/%s, want %s/%s", data.Meter, data.Component, meter, meter)
	}
	if data.Usage.Semantics != schema.UsageSemanticsInterval {
		t.Fatalf("usage semantics is %q", data.Usage.Semantics)
	}
	if data.Usage.Unit != unit {
		t.Fatalf("usage unit is %q, want %q", data.Usage.Unit, unit)
	}
	if data.Usage.Precision != schema.UsagePrecisionMicrosecond {
		t.Fatalf("usage precision is %q", data.Usage.Precision)
	}
	if data.BillingDimensions["deployment"] != deploymentID {
		t.Fatalf("deployment dimension is %q, want %q",
			data.BillingDimensions["deployment"], deploymentID)
	}
	if data.RulesRevision != rules.DefaultNumber {
		t.Fatalf("rules revision is %q, want %q", data.RulesRevision, rules.DefaultNumber)
	}
	from, to, err := heartbeat.Interval(data)
	if err != nil {
		t.Fatalf("parsing usage interval: %v", err)
	}
	if !from.Before(to) {
		t.Fatalf("usage interval is not forward: %s..%s", data.Usage.From, data.Usage.To)
	}
	// The CloudEvent time is the interval's start, which dates each heartbeat inside its
	// own billing day.
	if !event.Time().Equal(from) {
		t.Fatalf("event time %s is not the interval start %s", event.Time(), from)
	}
	if _, err := heartbeat.HeartbeatTime(data); err != nil {
		t.Fatalf("parsing heartbeat time: %v", err)
	}
}

// checkQuantity asserts the heartbeat's quantity is the interval's length in micro units of
// its unit, scaled by the meter's multiplier.
func checkQuantity(t *testing.T, beat sentBeat, multiplier int64) {
	t.Helper()
	from, to, err := heartbeat.Interval(beat.data)
	if err != nil {
		t.Fatalf("parsing usage interval: %v", err)
	}
	quantity, err := heartbeat.ParseMicro(beat.data.Usage.Quantity)
	if err != nil {
		t.Fatalf("parsing quantity: %v", err)
	}
	want := to.Sub(from).Microseconds() * multiplier
	if quantity != want {
		t.Fatalf("quantity is %d micro, want %d for %s..%s", quantity, want,
			beat.data.Usage.From, beat.data.Usage.To)
	}
}

// runningVM produces a VM created directly in RUNNING at runningSince, so billing starts
// there. The history write keys facts by field and time, so two transitions dated at the
// same instant would collapse into one fact; the source contract now clamps first change
// times on or before creation, so real events cannot collide. Creating the VM already
// RUNNING stays a fixture choice that keeps the timeline single-factored.
func runningVM(id string, runningSince time.Time, instanceType string) []*privatev1.Event {
	return []*privatev1.Event{
		testenv.VMEvent(id, privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			runningSince, runningSince, "web", "RUNNING", runningSince, instanceType,
			runningSince),
	}
}

// TestRunningVMBillsElapsedIntervals checks the first billable stretch end to end: a VM that
// reaches RUNNING sends heartbeats with the right envelope, and their quantities match the
// elapsed intervals, including the partial first one.
func TestRunningVMBillsElapsedIntervals(t *testing.T) {
	p := newPipeline(t, "running")
	p.startRelay(t)

	running := base.Add(40 * time.Second)
	total := testenv.ProduceEvents(t, p.tenant, runningVM("e1", running, "small"))
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)

	beats := p.sentHeartbeats(t)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:40Z..2026-10-05T10:01:00Z 20.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
	))
	for _, beat := range beats {
		checkEnvelope(t, beat, p.tenant, "vm-7", schema.ResourceTypeComputeInstance,
			"compute", "second")
		checkQuantity(t, beat, 1)
		if beat.data.BillingDimensions["instance_type"] != "small" {
			t.Fatalf("instance_type dimension is %q", beat.data.BillingDimensions["instance_type"])
		}
		if beat.data.BillingDimensions["tenant"] != p.tenant {
			t.Fatalf("tenant dimension is %q", beat.data.BillingDimensions["tenant"])
		}
		if beat.data.LastAppliedOffset != int64(total) {
			t.Fatalf("last applied offset is %d, want the folded offset %d",
				beat.data.LastAppliedOffset, total)
		}
	}
}

// TestBillingContinuesPerInterval checks that a VM that keeps running bills once per round:
// every round adds exactly one heartbeat of one interval, and the intervals stay contiguous.
func TestBillingContinuesPerInterval(t *testing.T) {
	p := newPipeline(t, "continues")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, runningVM("e1", base, "small"))
	p.foldAll(t, total)

	for i := 1; i <= 5; i++ {
		p.roundAt(t, base.Add(time.Duration(i)*time.Minute))
		p.waitOutboxEmpty(t)
		beats := p.sentHeartbeats(t)
		if len(beats) != i {
			t.Fatalf("after round %d the tenant has %d heartbeats, want %d",
				i, len(beats), i)
		}
		checkEnvelope(t, beats[len(beats)-1], p.tenant, "vm-7",
			schema.ResourceTypeComputeInstance, "compute", "second")
	}
	beats := p.sentHeartbeats(t)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
		"compute 2026-10-05T10:04:00Z..2026-10-05T10:05:00Z 60.000000",
	))
}

// TestStopEndsBillingWithTheFinalPartialInterval checks the shape of a stop: the meter's
// heartbeats stop, and the final partial interval is billed exactly once.
func TestStopEndsBillingWithTheFinalPartialInterval(t *testing.T) {
	p := newPipeline(t, "stop")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, runningVM("e1", base, "small"))
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	// The VM stops at 10:02:30; the event reaches fulfillment before the next round.
	stopped := base.Add(2*time.Minute + 30*time.Second)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2-stop", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			stopped.Add(10*time.Second), base, "web", "STOPPED", stopped, "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)
	beats := p.sentHeartbeats(t)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
	))

	// Later rounds send nothing more: the meter's heartbeats stopped, and the partial
	// interval was billed once, not again.
	p.roundAt(t, base.Add(4*time.Minute))
	p.roundAt(t, base.Add(5*time.Minute))
	p.waitOutboxEmpty(t)
	assertBeats(t, p.sentHeartbeats(t), wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
	))
}

// TestResizeSplitsDimensions checks the correction-shaped case: the running instance type
// changes, the usage splits across the two instance_type dimensions, the old meter stops, the
// new one starts, and nothing is billed twice.
func TestResizeSplitsDimensions(t *testing.T) {
	p := newPipeline(t, "resize")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, runningVM("e1", base, "small"))
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	// The resize: the running instance type becomes large at 10:02:30.
	resized := base.Add(2*time.Minute + 30*time.Second)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2-resize", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			resized.Add(10*time.Second), base, "web", "RUNNING", base, "large", resized),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(3*time.Minute))
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	beats := p.sentHeartbeats(t)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z 30.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
	))

	// The dimensions split the usage: the small meter stops at the resize and the large
	// one starts there, so the two meters never overlap and together cover the run.
	var small, large []sentBeat
	for _, beat := range beats {
		checkEnvelope(t, beat, p.tenant, "vm-7", schema.ResourceTypeComputeInstance,
			"compute", "second")
		switch beat.data.BillingDimensions["instance_type"] {
		case "small":
			small = append(small, beat)
		case "large":
			large = append(large, beat)
		default:
			t.Fatalf("unexpected instance_type dimension %q",
				beat.data.BillingDimensions["instance_type"])
		}
	}
	if len(small) != 3 || len(large) != 2 {
		t.Fatalf("expected three small and two large heartbeats, got %d and %d",
			len(small), len(large))
	}
	var billedMicro int64
	for _, beat := range small {
		_, to, err := heartbeat.Interval(beat.data)
		if err != nil {
			t.Fatalf("parsing usage interval: %v", err)
		}
		if to.After(resized) {
			t.Fatalf("the small meter billed past the resize, until %s", to)
		}
		quantity, err := heartbeat.ParseMicro(beat.data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		billedMicro += quantity
	}
	for _, beat := range large {
		from, _, err := heartbeat.Interval(beat.data)
		if err != nil {
			t.Fatalf("parsing usage interval: %v", err)
		}
		if from.Before(resized) {
			t.Fatalf("the large meter billed before the resize, from %s", from)
		}
		quantity, err := heartbeat.ParseMicro(beat.data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		billedMicro += quantity
	}
	// Nothing billed twice: the two meters together bill exactly the elapsed run.
	if billedMicro != int64(4*time.Minute.Seconds())*1_000_000 {
		t.Fatalf("the meters together billed %d micro, want the four minute run", billedMicro)
	}
}

// TestDeleteEndsBilling checks that deleting a running VM ends its billing at the deletion.
func TestDeleteEndsBilling(t *testing.T) {
	p := newPipeline(t, "deleted")
	p.startRelay(t)

	total := testenv.ProduceEvents(t, p.tenant, runningVM("e1", base, "small"))
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	deleted := base.Add(2*time.Minute + 10*time.Second)
	total += testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VMEvent("e2-delete", privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			deleted, base, "web", "RUNNING", base, "small", base),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(3*time.Minute))
	p.waitOutboxEmpty(t)
	beats := p.sentHeartbeats(t)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:10Z 10.000000",
	))

	// Rounds after the deletion send nothing more.
	p.roundAt(t, base.Add(4*time.Minute))
	p.roundAt(t, base.Add(5*time.Minute))
	p.waitOutboxEmpty(t)
	assertBeats(t, p.sentHeartbeats(t), wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:10Z 10.000000",
	))
}

// TestVolumeBillsWithItsOwnDimensions checks one non-compute resource: a volume bills on its
// own meter, with its own unit and dimensions, multiplied by its provisioned size.
func TestVolumeBillsWithItsOwnDimensions(t *testing.T) {
	p := newPipeline(t, "volume")
	p.startRelay(t)

	// The volume exists from 10:00 and reaches AVAILABLE, its billable state, at 10:01.
	available := base.Add(time.Minute)
	total := testenv.ProduceEvents(t, p.tenant, []*privatev1.Event{
		testenv.VolumeEvent("v1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "data", "AVAILABLE", available, "gold", 100),
	})
	p.foldAll(t, total)

	p.roundAt(t, base.Add(2*time.Minute))
	p.roundAt(t, base.Add(3*time.Minute))
	p.roundAt(t, base.Add(4*time.Minute))
	p.waitOutboxEmpty(t)

	beats := p.sentHeartbeats(t)
	// 100 provisioned GiB over each minute: 6000 gibibyte seconds per interval.
	assertBeats(t, beats, wantLines(
		"capacity 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 6000.000000",
		"capacity 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 6000.000000",
		"capacity 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 6000.000000",
	))
	for _, beat := range beats {
		checkEnvelope(t, beat, p.tenant, "vol-1", schema.ResourceTypeVolume,
			"capacity", schema.UsageUnitGiByteSecond)
		checkQuantity(t, beat, 100)
		if beat.data.BillingDimensions["storage_tier"] != "gold" {
			t.Fatalf("storage_tier dimension is %q", beat.data.BillingDimensions["storage_tier"])
		}
		if beat.data.BillingDimensions["meter_type"] != schema.UsageUnitGiByteSecond {
			t.Fatalf("meter_type dimension is %q", beat.data.BillingDimensions["meter_type"])
		}
	}
}

// TestClusterWorkersBillThemselves checks the design's cluster attribution end to end: the
// cluster bills only its control plane, a worker BMI bills its own allocation and
// consumption, and the cluster reference the worker gains mid-round is a fact no default
// meter uses, so it splits no span, adds no dimension, and re-bills nothing.
func TestClusterWorkersBillThemselves(t *testing.T) {
	p := newPipeline(t, "cluster-workers")
	p.startRelay(t)

	joined := base.Add(20 * time.Second)
	events := []*privatev1.Event{
		testenv.ClusterEvent("c1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "READY", base, "openshift-4-19", "4.19.0", base),
		// The worker starts unassigned; a second snapshot carries its cluster.
		testenv.BMIEvent("b1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "medium", "", ""),
		testenv.BMIEvent("b2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			joined, base, "web", "RUNNING", joined, "medium", "cluster-1", "workers"),
	}
	total := testenv.ProduceEvents(t, p.tenant, events)
	p.foldAll(t, total)

	p.roundAt(t, base.Add(time.Minute))
	p.roundAt(t, base.Add(2*time.Minute))
	p.waitOutboxEmpty(t)

	beats := p.sentHeartbeats(t)
	// The cluster's control plane and the worker's allocation each bill one contiguous
	// span per round — the state change and the cluster reference split nothing — and
	// consumption starts when the worker reaches RUNNING.
	assertBeats(t, beats, wantLines(
		"allocation 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"allocation 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"consumption 2026-10-05T10:00:20Z..2026-10-05T10:01:00Z 40.000000",
		"consumption 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"control_plane 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"control_plane 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
	))
	for _, beat := range beats {
		if _, ok := beat.data.BillingDimensions["cluster"]; ok {
			t.Fatalf("no default meter uses the cluster fact, but a heartbeat carries it: %v",
				beat.data.BillingDimensions)
		}
		switch beat.data.Meter {
		case "control_plane":
			checkEnvelope(t, beat, p.tenant, "cluster-1", schema.ResourceTypeClusterOrder,
				"control_plane", "second")
			checkQuantity(t, beat, 1)
			if beat.data.BillingDimensions["template"] != "openshift-4-19" {
				t.Fatalf("template dimension is %q", beat.data.BillingDimensions["template"])
			}
			if beat.data.BillingDimensions["version"] != "4.19.0" {
				t.Fatalf("version dimension is %q", beat.data.BillingDimensions["version"])
			}
		case "allocation", "consumption":
			checkEnvelope(t, beat, p.tenant, "bmi-1", schema.ResourceTypeBareMetalInstance,
				beat.data.Meter, "second")
			checkQuantity(t, beat, 1)
			if beat.data.BillingDimensions["instance_type"] != "medium" {
				t.Fatalf("instance_type dimension is %q", beat.data.BillingDimensions["instance_type"])
			}
		default:
			t.Fatalf("unexpected meter %q", beat.data.Meter)
		}
	}
}
