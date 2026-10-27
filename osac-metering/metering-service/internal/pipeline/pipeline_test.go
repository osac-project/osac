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
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/pipelinetest"
	"github.com/osac-project/osac-metering/internal/testenv"
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

// lines renders the heartbeats as comparable text, sorted so the order inside one round's
// group does not matter.
func lines(beats []pipelinetest.SentBeat) []string {
	result := pipelinetest.BeatLines(beats)
	sort.Strings(result)
	return result
}

// wantLines renders expected heartbeats in the same shape.
func wantLines(want ...string) []string { return want }

// assertBeats compares the sent heartbeats with the wanted lines.
func assertBeats(t *testing.T, beats []pipelinetest.SentBeat, want []string) {
	t.Helper()
	got := lines(beats)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("heartbeats mismatch:\ngot:\n%s\nwant:\n%s", pipelinetest.Join(got), pipelinetest.Join(want))
	}
}

// checkEnvelope asserts one heartbeat's full envelope: the CloudEvent attributes and
// extensions the adapters and the e2e tooling filter on, the payload's identity, interval
// semantics, and precision fields, and the deployment dimension every meter sends.
func checkEnvelope(
	t *testing.T, beat pipelinetest.SentBeat, tenant, resourceID, resourceType, meter, unit string,
) {
	t.Helper()
	event := beat.Event
	data := beat.Data
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
	if data.Usage.Precision != schema.UsagePrecisionSecond {
		t.Fatalf("usage precision is %q", data.Usage.Precision)
	}
	if data.BillingDimensions["deployment"] != deploymentID {
		t.Fatalf("deployment dimension is %q, want %q",
			data.BillingDimensions["deployment"], deploymentID)
	}
	if data.RulesRevision != "0001" {
		t.Fatalf("rules revision is %q, want %q", data.RulesRevision, "0001")
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
func checkQuantity(t *testing.T, beat pipelinetest.SentBeat, multiplier int64) {
	t.Helper()
	from, to, err := heartbeat.Interval(beat.Data)
	if err != nil {
		t.Fatalf("parsing usage interval: %v", err)
	}
	quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
	if err != nil {
		t.Fatalf("parsing quantity: %v", err)
	}
	want := to.Sub(from).Microseconds() * multiplier
	if quantity != want {
		t.Fatalf("quantity is %d micro, want %d for %s..%s", quantity, want,
			beat.Data.Usage.From, beat.Data.Usage.To)
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
	p := pipelinetest.NewPipeline(t, "running")
	p.StartRelay(t)

	running := base.Add(40 * time.Second)
	total := testenv.ProduceEvents(t, p.Tenant, runningVM("e1", running, "small"))
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:40Z..2026-10-05T10:01:00Z 20.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 60.000000",
	))
	for _, beat := range beats {
		checkEnvelope(t, beat, p.Tenant, "vm-7", schema.ResourceTypeComputeInstance,
			"compute", "second")
		checkQuantity(t, beat, 1)
		if beat.Data.BillingDimensions["instance_type"] != "small" {
			t.Fatalf("instance_type dimension is %q", beat.Data.BillingDimensions["instance_type"])
		}
		if beat.Data.BillingDimensions["tenant"] != p.Tenant {
			t.Fatalf("tenant dimension is %q", beat.Data.BillingDimensions["tenant"])
		}
		if beat.Data.LastAppliedOffset != int64(total) {
			t.Fatalf("last applied offset is %d, want the folded offset %d",
				beat.Data.LastAppliedOffset, total)
		}
	}
}

// TestBillingContinuesPerInterval checks that a VM that keeps running bills once per round:
// every round adds exactly one heartbeat of one interval, and the intervals stay contiguous.
func TestBillingContinuesPerInterval(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "continues")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, runningVM("e1", base, "small"))
	p.FoldAll(t, total)

	for i := 1; i <= 5; i++ {
		p.RoundAt(t, base.Add(time.Duration(i)*time.Minute))
		p.WaitOutboxEmpty(t)
		beats := pipelinetest.SentHeartbeats(t, p.Tenant)
		if len(beats) != i {
			t.Fatalf("after round %d the tenant has %d heartbeats, want %d",
				i, len(beats), i)
		}
		checkEnvelope(t, beats[len(beats)-1], p.Tenant, "vm-7",
			schema.ResourceTypeComputeInstance, "compute", "second")
	}
	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
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
	p := pipelinetest.NewPipeline(t, "stop")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, runningVM("e1", base, "small"))
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	// The VM stops at 10:02:30; the event reaches fulfillment before the next round.
	stopped := base.Add(2*time.Minute + 30*time.Second)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2-stop", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			stopped.Add(10*time.Second), base, "web", "STOPPED", stopped, "small", base),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(3*time.Minute))
	p.WaitOutboxEmpty(t)
	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
	))

	// Later rounds send nothing more: the meter's heartbeats stopped, and the partial
	// interval was billed once, not again.
	p.RoundAt(t, base.Add(4*time.Minute))
	p.RoundAt(t, base.Add(5*time.Minute))
	p.WaitOutboxEmpty(t)
	assertBeats(t, pipelinetest.SentHeartbeats(t, p.Tenant), wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
	))
}

// TestResizeSplitsDimensions checks the correction-shaped case: the running instance type
// changes, the usage splits across the two instance_type dimensions, the old meter stops, the
// new one starts, and nothing is billed twice.
func TestResizeSplitsDimensions(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "resize")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, runningVM("e1", base, "small"))
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	// The resize: the running instance type becomes large at 10:02:30.
	resized := base.Add(2*time.Minute + 30*time.Second)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2-resize", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			resized.Add(10*time.Second), base, "web", "RUNNING", base, "large", resized),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:30Z 30.000000",
		"compute 2026-10-05T10:02:30Z..2026-10-05T10:03:00Z 30.000000",
		"compute 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 60.000000",
	))

	// The dimensions split the usage: the small meter stops at the resize and the large
	// one starts there, so the two meters never overlap and together cover the run.
	var small, large []pipelinetest.SentBeat
	for _, beat := range beats {
		checkEnvelope(t, beat, p.Tenant, "vm-7", schema.ResourceTypeComputeInstance,
			"compute", "second")
		switch beat.Data.BillingDimensions["instance_type"] {
		case "small":
			small = append(small, beat)
		case "large":
			large = append(large, beat)
		default:
			t.Fatalf("unexpected instance_type dimension %q",
				beat.Data.BillingDimensions["instance_type"])
		}
	}
	if len(small) != 3 || len(large) != 2 {
		t.Fatalf("expected three small and two large heartbeats, got %d and %d",
			len(small), len(large))
	}
	var billedMicro int64
	for _, beat := range small {
		_, to, err := heartbeat.Interval(beat.Data)
		if err != nil {
			t.Fatalf("parsing usage interval: %v", err)
		}
		if to.After(resized) {
			t.Fatalf("the small meter billed past the resize, until %s", to)
		}
		quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
		if err != nil {
			t.Fatalf("parsing quantity: %v", err)
		}
		billedMicro += quantity
	}
	for _, beat := range large {
		from, _, err := heartbeat.Interval(beat.Data)
		if err != nil {
			t.Fatalf("parsing usage interval: %v", err)
		}
		if from.Before(resized) {
			t.Fatalf("the large meter billed before the resize, from %s", from)
		}
		quantity, err := heartbeat.ParseMicro(beat.Data.Usage.Quantity)
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
	p := pipelinetest.NewPipeline(t, "deleted")
	p.StartRelay(t)

	total := testenv.ProduceEvents(t, p.Tenant, runningVM("e1", base, "small"))
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	deleted := base.Add(2*time.Minute + 10*time.Second)
	total += testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VMEvent("e2-delete", privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			deleted, base, "web", "RUNNING", base, "small", base),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(3*time.Minute))
	p.WaitOutboxEmpty(t)
	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	assertBeats(t, beats, wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:10Z 10.000000",
	))

	// Rounds after the deletion send nothing more.
	p.RoundAt(t, base.Add(4*time.Minute))
	p.RoundAt(t, base.Add(5*time.Minute))
	p.WaitOutboxEmpty(t)
	assertBeats(t, pipelinetest.SentHeartbeats(t, p.Tenant), wantLines(
		"compute 2026-10-05T10:00:00Z..2026-10-05T10:01:00Z 60.000000",
		"compute 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 60.000000",
		"compute 2026-10-05T10:02:00Z..2026-10-05T10:02:10Z 10.000000",
	))
}

// TestVolumeBillsWithItsOwnDimensions checks one non-compute resource: a volume bills on its
// own meter, with its own unit and dimensions, multiplied by its provisioned size.
func TestVolumeBillsWithItsOwnDimensions(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "volume")
	p.StartRelay(t)

	// The volume exists from 10:00 and reaches AVAILABLE, its billable state, at 10:01.
	available := base.Add(time.Minute)
	total := testenv.ProduceEvents(t, p.Tenant, []*privatev1.Event{
		testenv.VolumeEvent("v1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "data", "AVAILABLE", available, "gold", 100),
	})
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(2*time.Minute))
	p.RoundAt(t, base.Add(3*time.Minute))
	p.RoundAt(t, base.Add(4*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	// 100 provisioned GiB over each minute: 6000 gibibyte seconds per interval.
	assertBeats(t, beats, wantLines(
		"capacity 2026-10-05T10:01:00Z..2026-10-05T10:02:00Z 6000.000000",
		"capacity 2026-10-05T10:02:00Z..2026-10-05T10:03:00Z 6000.000000",
		"capacity 2026-10-05T10:03:00Z..2026-10-05T10:04:00Z 6000.000000",
	))
	for _, beat := range beats {
		checkEnvelope(t, beat, p.Tenant, "vol-1", schema.ResourceTypeVolume,
			"capacity", schema.UsageUnitGiByteSecond)
		checkQuantity(t, beat, 100)
		if beat.Data.BillingDimensions["storage_tier"] != "gold" {
			t.Fatalf("storage_tier dimension is %q", beat.Data.BillingDimensions["storage_tier"])
		}
		if beat.Data.BillingDimensions["meter_type"] != schema.UsageUnitGiByteSecond {
			t.Fatalf("meter_type dimension is %q", beat.Data.BillingDimensions["meter_type"])
		}
	}
}

// TestClusterWorkersBillUnderTheRootGate checks the attribution model end to end: while the
// cluster installs, nothing bills, the worker's hardware included. The round after the
// cluster reaches READY bills the control plane retroactively and the worker's allocation
// retroactively from its own start, all under the cluster's gate, and every worker
// heartbeat carries the cluster and node set as dimensions, keyed by the reference's id.
func TestClusterWorkersBillUnderTheRootGate(t *testing.T) {
	p := pipelinetest.NewPipeline(t, "cluster-workers")
	p.StartRelay(t)

	// The cluster installs and the worker is allocated; the gate is shut.
	installing := []*privatev1.Event{
		testenv.ClusterEvent("c1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "PROGRESSING", base, "openshift-4-19", "4.19.0", base),
		testenv.BMIEvent("b1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			base, base, "web", "STARTING", base, "medium", "cluster-1", "workers"),
	}
	total := testenv.ProduceEvents(t, p.Tenant, installing)
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(time.Minute))
	p.WaitOutboxEmpty(t)
	if beats := pipelinetest.SentHeartbeats(t, p.Tenant); len(beats) != 0 {
		t.Fatalf("nothing bills while the cluster installs, got %d heartbeats", len(beats))
	}

	// The cluster reaches READY; the worker has no fact of its own, and the next round
	// must still bill it, retroactively, because the changed cluster drags its workers
	// into the recompute set.
	ready := base.Add(30 * time.Second)
	readyEvents := []*privatev1.Event{
		testenv.ClusterEvent("c2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			ready, base, "web", "READY", ready, "openshift-4-19", "4.19.0", base),
	}
	total += testenv.ProduceEvents(t, p.Tenant, readyEvents)
	p.FoldAll(t, total)

	p.RoundAt(t, base.Add(2*time.Minute))
	p.WaitOutboxEmpty(t)

	beats := pipelinetest.SentHeartbeats(t, p.Tenant)
	assertBeats(t, beats, wantLines(
		"allocation 2026-10-05T10:00:00Z..2026-10-05T10:02:00Z 120.000000",
		"control_plane 2026-10-05T10:00:00Z..2026-10-05T10:02:00Z 120.000000",
	))
	for _, beat := range beats {
		switch beat.Data.Meter {
		case "control_plane":
			checkEnvelope(t, beat, p.Tenant, "cluster-1", schema.ResourceTypeClusterOrder,
				"control_plane", "second")
			checkQuantity(t, beat, 1)
			if beat.Data.BillingDimensions["template"] != "openshift-4-19" {
				t.Fatalf("template dimension is %q", beat.Data.BillingDimensions["template"])
			}
			if beat.Data.BillingDimensions["version"] != "4.19.0" {
				t.Fatalf("version dimension is %q", beat.Data.BillingDimensions["version"])
			}
		case "allocation":
			checkEnvelope(t, beat, p.Tenant, "bmi-1", schema.ResourceTypeBareMetalInstance,
				"allocation", "second")
			checkQuantity(t, beat, 1)
			if beat.Data.BillingDimensions["instance_type"] != "medium" {
				t.Fatalf("instance_type dimension is %q", beat.Data.BillingDimensions["instance_type"])
			}
			// The dimension is the reference's id, never its display name.
			if beat.Data.BillingDimensions["cluster"] != "cluster-1" {
				t.Fatalf("cluster dimension is %q, want the id cluster-1",
					beat.Data.BillingDimensions["cluster"])
			}
			if beat.Data.BillingDimensions["node_set"] != "workers" {
				t.Fatalf("node_set dimension is %q", beat.Data.BillingDimensions["node_set"])
			}
		default:
			t.Fatalf("unexpected meter %q", beat.Data.Meter)
		}
	}
}
