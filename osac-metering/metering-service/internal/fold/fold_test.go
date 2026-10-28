/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package fold_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	"github.com/osac-project/osac-metering/internal/metrics/metertest"
	"github.com/osac-project/osac-metering/internal/testenv"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// TestMain stops the shared containers after the tests, so their output pipes close before
// the test binary exits.
func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

// base is the timeline of the design's fold example: a VM created, resized, stopped, and
// deleted, plus a volume, an external IP, and an event of a type metering does not track.
var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

func recordedLog() []*privatev1.Event {
	return []*privatev1.Event{
		vmEvent("e1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, base,
			"STARTING", base, "small", base),
		vmEvent("e2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(40*time.Second),
			"RUNNING", base.Add(40*time.Second), "small", base),
		vmEvent("e3", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(30*time.Minute),
			"RUNNING", base.Add(40*time.Second), "large", base.Add(30*time.Minute)),
		vmEvent("e4", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(time.Hour),
			"STOPPED", base.Add(time.Hour), "large", base.Add(30*time.Minute)),
		vmEvent("e5", privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			base.Add(time.Hour).Add(5*time.Minute+12*time.Second),
			"STOPPED", base.Add(time.Hour), "large", base.Add(30*time.Minute)),
		volumeEvent("e6", "vol-1", base.Add(time.Minute),
			"AVAILABLE", base.Add(2*time.Minute)),
		externalIPEvent("e7", "ip-1", base.Add(2*time.Minute),
			"ALLOCATED", base.Add(2*time.Minute), true),
		untrackedEvent("e8", base.Add(3*time.Minute)),
	}
}

func vmEvent(
	id string,
	typ privatev1.EventType,
	timestamp time.Time,
	state string,
	stateSince time.Time,
	runningType string,
	typeSince time.Time,
) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      typ,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_ComputeInstance{
			ComputeInstance: &privatev1.ComputeInstance{
				Id: "vm-7",
				Metadata: &privatev1.Metadata{
					Tenant:            "acme",
					Project:           "web",
					CreationTimestamp: timestamppb.New(base),
				},
				Spec: &privatev1.ComputeInstanceSpec{
					Template: &privatev1.ComputeInstanceTemplateReference{Id: "1", Name: "tpl-1"},
					DiskImage: &privatev1.DiskImageReference{
						Id: "img-1", Name: "rhel-9",
					},
					BootDisk: &privatev1.ComputeInstanceDisk{SizeGib: int32Ptr(30)},
				},
				Status: &privatev1.ComputeInstanceStatus{
					State:                      computeState(state),
					StateTransitionTime:        timestamppb.New(stateSince),
					InstanceType:               &privatev1.InstanceTypeReference{Id: "it-1", Name: runningType},
					InstanceTypeTransitionTime: timestamppb.New(typeSince),
				},
			},
		},
	}
}

func volumeEvent(id, resourceID string, timestamp time.Time, state string, stateSince time.Time) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_Volume{
			Volume: &privatev1.Volume{
				Id: resourceID,
				Metadata: &privatev1.Metadata{
					Tenant: "acme", Project: "data",
					CreationTimestamp: timestamppb.New(timestamp),
				},
				Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
				Status: &privatev1.VolumeStatus{
					State:               volumeState(state),
					StateTransitionTime: timestamppb.New(stateSince),
					VendorVolumeId:      "vend-9",
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					ProvisionedSizeGib:  100,
				},
			},
		},
	}
}

func externalIPEvent(
	id, resourceID string, timestamp time.Time, state string, stateSince time.Time, attached bool,
) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_ExternalIp{
			ExternalIp: &privatev1.ExternalIP{
				Id: resourceID,
				Metadata: &privatev1.Metadata{
					Tenant: "acme", Project: "web",
					CreationTimestamp: timestamppb.New(timestamp),
				},
				Spec: &privatev1.ExternalIPSpec{
					Pool: &privatev1.ExternalIPPoolReference{Id: "p-1", Name: "pool-a"},
				},
				Status: &privatev1.ExternalIPStatus{
					State:                    externalIPState(state),
					StateTransitionTime:      timestamppb.New(stateSince),
					Address:                  "192.0.2.10",
					Attached:                 attached,
					AttachmentTransitionTime: timestamppb.New(stateSince),
					Attribution: &privatev1.ExternalIPAttribution{
						Target: &privatev1.ExternalIPAttribution_ComputeInstance{
							ComputeInstance: &privatev1.ComputeInstanceLocalReference{Id: "vm-7"},
						},
						Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					},
				},
			},
		},
	}
}

func untrackedEvent(id string, timestamp time.Time) *privatev1.Event {
	return &privatev1.Event{
		Id:        id,
		Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Timestamp: timestamppb.New(timestamp),
		Payload: &privatev1.Event_Project{
			Project: &privatev1.Project{
				Id: "p-9",
				Metadata: &privatev1.Metadata{
					Tenant: "acme", CreationTimestamp: timestamppb.New(timestamp),
				},
			},
		},
	}
}

func computeState(state string) privatev1.ComputeInstanceState {
	return privatev1.ComputeInstanceState(
		privatev1.ComputeInstanceState_value["COMPUTE_INSTANCE_STATE_"+state],
	)
}

func volumeState(state string) privatev1.VolumeState {
	return privatev1.VolumeState(privatev1.VolumeState_value["VOLUME_STATE_"+state])
}

func externalIPState(state string) privatev1.ExternalIPState {
	return privatev1.ExternalIPState(privatev1.ExternalIPState_value["EXTERNAL_IP_STATE_"+state])
}

func int32Ptr(v int32) *int32 { return &v }

func produce(t *testing.T, tenant string, events []*privatev1.Event) int {
	t.Helper()
	return testenv.ProduceEvents(t, tenant, events)
}

// foldAll folds the tenant's topic until the stored offset reaches total, restarting the fold
// at random points when restarts is true. A fold error fails the test instead of stalling
// until a timeout.
func foldAll(
	t *testing.T, store *database.Store, tenant string, total int, restarts bool,
) {
	t.Helper()
	ctx := context.Background()
	f, err := fold.New(store, testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	random := rand.New(rand.NewSource(1))
	deadline := time.Now().Add(60 * time.Second)

	waitForTotal := func() bool {
		for {
			progress, err := store.GetTenantProgress(context.Background(), tenant)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("reading progress: %v", err)
			}
			if progress.EventOffset >= int64(total) {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// check folds a stopped run's error: a context cancellation is a clean stop, anything
	// else is a real failure.
	check := func(err error) {
		t.Helper()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("fold run failed: %v", err)
		}
	}

	if !restarts {
		// A transient database blip ends one fold run; the service restarts the
		// loop, so the test restarts the fold a few attempts before the error
		// is real.
		const attempts = 3
		for attempt := 1; ; attempt++ {
			runCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- f.RunTenant(runCtx, tenant) }()

			reached := waitForTotal()
			cancel()
			var err error
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("fold did not stop after cancellation")
			}

			if reached && (err == nil || errors.Is(err, context.Canceled)) {
				return
			}
			if attempt >= attempts {
				if !reached {
					t.Fatalf("fold did not reach offset %d", total)
				}
				t.Fatalf("fold run failed: %v", err)
			}
		}
	}
	// Each restart is a new process: its own consumer and its own fold.
	reached := func() bool {
		progress, err := store.GetTenantProgress(context.Background(), tenant)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("reading progress: %v", err)
		}
		return progress.EventOffset >= int64(total)
	}
	for {
		consumer, err := sarama.NewConsumer(
			[]string{testenv.Broker(t)}, testenv.SaramaConfig(t),
		)
		if err != nil {
			t.Fatalf("creating consumer: %v", err)
		}
		restarted, err := fold.New(store, consumer, logr.Discard())
		if err != nil {
			t.Fatalf("creating fold: %v", err)
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- restarted.RunTenant(runCtx, tenant) }()
		time.Sleep(time.Duration(random.Intn(400)+100) * time.Millisecond)
		cancel()
		check(<-done)
		_ = consumer.Close()
		if reached() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold with restarts did not reach offset %d", total)
		}
	}
}

// dumpHistory renders the tenant's whole history as comparable text.
func dumpHistory(t *testing.T, store *database.Store, tenant string) []string {
	t.Helper()
	pool := store.Pool()
	rows, err := pool.Query(
		context.Background(),
		`SELECT resource_type, resource_id, field, since, value
		 FROM history WHERE tenant = $1
		 ORDER BY resource_type, resource_id, field, since, value`,
		tenant,
	)
	if err != nil {
		t.Fatalf("dumping history: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var resourceType, resourceID, field, value string
		var since time.Time
		if err := rows.Scan(&resourceType, &resourceID, &field, &since, &value); err != nil {
			t.Fatalf("scanning history row: %v", err)
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s %s %s %s", resourceType, resourceID, field, since.UTC().Format(time.RFC3339Nano), value,
		))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("dumping history: %v", err)
	}
	return lines
}

// TestFoldSkipsEventsOfAnotherTenant checks that an event whose payload names a different
// tenant is skipped, not folded: billing it here would bill this tenant for another
// tenant's resource. The offset still advances past the skipped record, and a payload
// without a tenant keeps trusting the topic.
func TestFoldSkipsEventsOfAnotherTenant(t *testing.T) {
	tenant := "t-foreign"
	mine := testenv.VMEventOf("vm-mine", "f1",
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base, base, "web",
		"RUNNING", base, "small", base)
	mine.GetComputeInstance().Metadata.Tenant = tenant
	foreign := testenv.VMEventOf("vm-foreign", "f2",
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(time.Minute), base, "web",
		"RUNNING", base, "small", base)
	foreign.GetComputeInstance().Metadata.Tenant = "beta"
	unlabeled := testenv.VMEventOf("vm-unlabeled", "f3",
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(2*time.Minute), base, "web",
		"RUNNING", base, "small", base)
	unlabeled.GetComputeInstance().Metadata.Tenant = ""

	total := testenv.ProduceEventsAsWritten(t, tenant, []*privatev1.Event{mine, foreign, unlabeled})
	store := testenv.Database(t)
	skipsBefore := skippedEvents(t, tenant, "foreign_tenant")
	foldAll(t, store, tenant, total, false)

	history := stringsJoin(dumpHistory(t, store, tenant))
	if !strings.Contains(history, "vm-mine") {
		t.Fatalf("own-tenant event was not folded:\n%s", history)
	}
	if !strings.Contains(history, "vm-unlabeled") {
		t.Fatalf("event without a payload tenant was not folded:\n%s", history)
	}
	if strings.Contains(history, "vm-foreign") {
		t.Fatalf("event of another tenant was folded:\n%s", history)
	}
	if skipsAfter := skippedEvents(t, tenant, "foreign_tenant"); skipsAfter != skipsBefore+1 {
		t.Fatalf(
			"foreign-tenant skip counter is %v after the fold, want %v: the skip must be visible to alerting",
			skipsAfter, skipsBefore+1,
		)
	}
}

// skippedEvents reads the fold's skip counter for a tenant and reason.
func skippedEvents(t *testing.T, tenant, reason string) float64 {
	t.Helper()
	return metertest.LabeledValue(
		t, "osac_metering_events_skipped_total",
		map[string]string{"tenant": tenant, "reason": reason},
	)
}

// TestFoldSkipsEventsWithoutAPayload checks that an event whose payload this build cannot
// read is skipped, not silently advanced past: a payload variant a newer publisher knows,
// or a record with no payload at all, would otherwise be unbilled forever with the offset
// moving on. A payload variant this build does not know survives decoding only as unknown
// fields, so after unmarshaling it reads exactly like a missing payload.
func TestFoldSkipsEventsWithoutAPayload(t *testing.T) {
	tenant := "t-no-payload"
	bare := &privatev1.Event{
		Id:        "b1",
		Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Timestamp: timestamppb.New(base),
	}
	healthy := testenv.VMEventOf("vm-mine", "h1",
		privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, base, base, "web",
		"RUNNING", base, "small", base)

	total := produce(t, tenant, []*privatev1.Event{bare, healthy})
	store := testenv.Database(t)
	skipsBefore := skippedEvents(t, tenant, "unknown_payload")
	foldAll(t, store, tenant, total, false)

	history := stringsJoin(dumpHistory(t, store, tenant))
	if !strings.Contains(history, "vm-mine") {
		t.Fatalf("healthy event was not folded:\n%s", history)
	}
	for _, line := range dumpHistory(t, store, tenant) {
		if !strings.Contains(line, "vm-mine") {
			t.Fatalf("the payload-less event must contribute no facts:\n%s", history)
		}
	}
	if skipsAfter := skippedEvents(t, tenant, "unknown_payload"); skipsAfter != skipsBefore+1 {
		t.Fatalf(
			"unknown-payload skip counter is %v after the fold, want %v: the skip must be visible to alerting",
			skipsAfter, skipsBefore+1,
		)
	}
}

// TestFoldSkipsTrackedPayloadsWithoutAResourceID checks that a tracked payload without a
// resource id is skipped loudly: its facts could never be keyed into history.
func TestFoldSkipsTrackedPayloadsWithoutAResourceID(t *testing.T) {
	tenant := "t-no-id"
	anonymous := testenv.VMEventOf("", "a1",
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base, base, "web",
		"RUNNING", base, "small", base)
	healthy := testenv.VMEventOf("vm-mine", "h1",
		privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, base, base, "web",
		"RUNNING", base, "small", base)

	total := produce(t, tenant, []*privatev1.Event{anonymous, healthy})
	store := testenv.Database(t)
	skipsBefore := skippedEvents(t, tenant, "missing_resource_id")
	foldAll(t, store, tenant, total, false)

	history := stringsJoin(dumpHistory(t, store, tenant))
	if !strings.Contains(history, "vm-mine") {
		t.Fatalf("healthy event was not folded:\n%s", history)
	}
	if skipsAfter := skippedEvents(t, tenant, "missing_resource_id"); skipsAfter != skipsBefore+1 {
		t.Fatalf(
			"missing-resource-id skip counter is %v after the fold, want %v",
			skipsAfter, skipsBefore+1,
		)
	}
}

// TestFoldSkipsTrackedPayloadsWithoutACreationTime checks that a tracked payload whose
// creation timestamp is missing, or present but all zero, is skipped loudly. Folding it
// would silently drop every creation-keyed fact: created, project, template, and the
// creation fallback of varying fields. Inventing a time instead would bill the wrong
// minutes. A payload with a real creation timestamp keeps folding normally.
func TestFoldSkipsTrackedPayloadsWithoutACreationTime(t *testing.T) {
	tenant := "t-no-creation"
	undated := testenv.VMEventOf("vm-undated", "u1",
		privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, base, base, "web",
		"RUNNING", base, "small", base)
	undated.GetComputeInstance().Metadata.CreationTimestamp = nil
	zeroDated := testenv.VMEventOf("vm-zero", "z1",
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, base.Add(time.Minute), base, "web",
		"RUNNING", base, "small", base)
	zeroDated.GetComputeInstance().Metadata.CreationTimestamp = timestamppb.New(time.Unix(0, 0))
	healthy := testenv.VMEventOf("vm-mine", "h1",
		privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, base, base, "web",
		"RUNNING", base, "small", base)

	total := produce(t, tenant, []*privatev1.Event{undated, zeroDated, healthy})
	store := testenv.Database(t)
	skipsBefore := skippedEvents(t, tenant, "missing_creation_time")
	foldAll(t, store, tenant, total, false)

	history := stringsJoin(dumpHistory(t, store, tenant))
	if !strings.Contains(history, "vm-mine") {
		t.Fatalf("an event with a real creation timestamp was not folded:\n%s", history)
	}
	if strings.Contains(history, "vm-undated") || strings.Contains(history, "vm-zero") {
		t.Fatalf("an event without a creation time was folded:\n%s", history)
	}
	if skipsAfter := skippedEvents(t, tenant, "missing_creation_time"); skipsAfter != skipsBefore+2 {
		t.Fatalf(
			"missing-creation-time skip counter is %v after the fold, want %v",
			skipsAfter, skipsBefore+2,
		)
	}
}

// TestFoldSkipsUndatedDeletionEvents checks that a deletion event without an envelope
// timestamp, or with an all-zero one, is skipped loudly. Folding it silently would leave
// the resource billed forever; dating the deletion at 1970 would bill decades too much.
// A dated deletion still records the deleted fact.
func TestFoldSkipsUndatedDeletionEvents(t *testing.T) {
	tenant := "t-no-deletion"
	undated := testenv.VMEventOf("vm-undated", "u1",
		privatev1.EventType_EVENT_TYPE_OBJECT_DELETED, base.Add(time.Hour), base, "web",
		"STOPPED", base.Add(time.Hour), "large", base)
	undated.Timestamp = nil
	zeroDated := testenv.VMEventOf("vm-zero", "z1",
		privatev1.EventType_EVENT_TYPE_OBJECT_DELETED, base.Add(time.Hour), base, "web",
		"STOPPED", base.Add(time.Hour), "large", base)
	zeroDated.Timestamp = timestamppb.New(time.Unix(0, 0))
	dated := testenv.VMEventOf("vm-dated", "d1",
		privatev1.EventType_EVENT_TYPE_OBJECT_DELETED, base.Add(time.Hour), base, "web",
		"STOPPED", base.Add(time.Hour), "large", base)

	total := produce(t, tenant, []*privatev1.Event{undated, zeroDated, dated})
	store := testenv.Database(t)
	skipsBefore := skippedEvents(t, tenant, "missing_deletion_time")
	foldAll(t, store, tenant, total, false)

	history := stringsJoin(dumpHistory(t, store, tenant))
	if !strings.Contains(history, "vm-dated deleted") {
		t.Fatalf("a dated deletion event did not record the deleted fact:\n%s", history)
	}
	if strings.Contains(history, "vm-undated deleted") || strings.Contains(history, "vm-zero deleted") {
		t.Fatalf("an undated deletion event recorded a deleted fact:\n%s", history)
	}
	if skipsAfter := skippedEvents(t, tenant, "missing_deletion_time"); skipsAfter != skipsBefore+2 {
		t.Fatalf(
			"missing-deletion-time skip counter is %v after the fold, want %v",
			skipsAfter, skipsBefore+2,
		)
	}
}

func TestFoldIsDeterministic(t *testing.T) {
	scenarios := []struct {
		name      string
		transform func([]*privatev1.Event) []*privatev1.Event
		restarts  bool
	}{
		{name: "single-pass", transform: func(e []*privatev1.Event) []*privatev1.Event { return e }},
		{
			name:      "restarts-at-random-points",
			transform: func(e []*privatev1.Event) []*privatev1.Event { return e },
			restarts:  true,
		},
		{
			name: "every-event-delivered-twice",
			transform: func(events []*privatev1.Event) []*privatev1.Event {
				var doubled []*privatev1.Event
				for _, event := range events {
					doubled = append(doubled, event, event)
				}
				return doubled
			},
		},
		{
			name: "shuffled-order",
			transform: func(events []*privatev1.Event) []*privatev1.Event {
				shuffled := append([]*privatev1.Event(nil), events...)
				random := rand.New(rand.NewSource(7))
				random.Shuffle(len(shuffled), func(i, j int) {
					shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
				})
				return shuffled
			},
		},
	}

	var reference []string
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			tenant := "t-" + scenario.name
			events := scenario.transform(recordedLog())
			total := produce(t, tenant, events)
			store := testenv.Database(t)
			foldAll(t, store, tenant, total, scenario.restarts)
			got := dumpHistory(t, store, tenant)
			if reference == nil {
				reference = got
				return
			}
			if stringsJoin(got) != stringsJoin(reference) {
				t.Fatalf(
					"history differs from the single-pass fold:\ngot:\n%s\nwant:\n%s",
					stringsJoin(got), stringsJoin(reference),
				)
			}
		})
	}
}

// TestFoldRecordsCreationValueWithoutTransitionTime checks that a changing field that carries
// a value but no change time yet is recorded as the creation value: a cluster created with a
// version and never upgraded keeps its version dimension.
func TestFoldRecordsCreationValueWithoutTransitionTime(t *testing.T) {
	tenant := "t-creation-value"
	creation := base.Add(3 * time.Hour)
	event := &privatev1.Event{
		Id:        "c1",
		Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
		Timestamp: timestamppb.New(creation),
		Payload: &privatev1.Event_Cluster{
			Cluster: &privatev1.Cluster{
				Id: "c-9",
				Metadata: &privatev1.Metadata{
					Tenant: "acme", Project: "web",
					CreationTimestamp: timestamppb.New(creation),
				},
				Spec: &privatev1.ClusterSpec{
					// A version with no version transition time: the creation value.
					Version: &privatev1.ClusterVersionReference{Id: "9", Name: "4.18"},
				},
				Status: &privatev1.ClusterStatus{
					State:               privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
					StateTransitionTime: timestamppb.New(creation),
				},
			},
		},
	}
	total := produce(t, tenant, []*privatev1.Event{event})
	store := testenv.Database(t)
	foldAll(t, store, tenant, total, false)
	got := dumpHistory(t, store, tenant)
	want := []string{
		"Cluster c-9 created " + creation.Format(time.RFC3339Nano) + " true",
		"Cluster c-9 metadata.project " + creation.Format(time.RFC3339Nano) + " web",
		"Cluster c-9 spec.version " + creation.Format(time.RFC3339Nano) + " 4.18",
		"Cluster c-9 status.state " + creation.Format(time.RFC3339Nano) + " PROGRESSING",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("history mismatch:\ngot:\n%s\nwant:\n%s", stringsJoin(got), stringsJoin(want))
	}
}

// TestFoldTreatsAllZeroTimestampsAsAbsent checks that a present-but-zero timestamp field
// (seconds 0, nanos 0) follows exactly the path of an absent one: the creation value. A
// zero timestamp is an unset field rendered as Unix epoch, not a real 1970 date, so dating
// a fact at it would place the value decades before the resource existed.
func TestFoldTreatsAllZeroTimestampsAsAbsent(t *testing.T) {
	extractor, err := fold.NewFactExtractor()
	if err != nil {
		t.Fatalf("creating extractor: %v", err)
	}
	creation := base.Add(3 * time.Hour)
	newCluster := func(transition *timestamppb.Timestamp) *privatev1.Event {
		return &privatev1.Event{
			Id:        "c1",
			Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			Timestamp: timestamppb.New(creation),
			Payload: &privatev1.Event_Cluster{
				Cluster: &privatev1.Cluster{
					Id: "c-9",
					Metadata: &privatev1.Metadata{
						Tenant: "acme", Project: "web",
						CreationTimestamp: timestamppb.New(creation),
					},
					Spec: &privatev1.ClusterSpec{
						Version: &privatev1.ClusterVersionReference{Id: "9", Name: "4.18"},
					},
					Status: &privatev1.ClusterStatus{
						State:               privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
						StateTransitionTime: timestamppb.New(creation),
						// The version's transition time: absent in one event, all zero in
						// the other.
						VersionTransitionTime: transition,
					},
				},
			},
		}
	}
	absent, err := extractor.Facts("acme", newCluster(nil))
	if err != nil {
		t.Fatalf("folding the absent-transition event: %v", err)
	}
	zero, err := extractor.Facts("acme", newCluster(timestamppb.New(time.Unix(0, 0))))
	if err != nil {
		t.Fatalf("folding the zero-transition event: %v", err)
	}
	if fmt.Sprint(absent) != fmt.Sprint(zero) {
		t.Fatalf(
			"an all-zero transition time must behave like an absent one:\nabsent:\n%s\nzero:\n%s",
			fmt.Sprint(absent), fmt.Sprint(zero),
		)
	}
	// The absent path records the creation value, so the zero case must too: the version
	// is a fact of the creation, not of 1970.
	found := false
	for _, fact := range zero {
		if fact.Field != "spec.version" {
			continue
		}
		found = true
		if !fact.Since.Equal(creation) {
			t.Errorf("spec.version is dated %s, want the creation %s", fact.Since, creation)
		}
	}
	if !found {
		t.Error("the version fact is missing")
	}
}

func stringsJoin(lines []string) string {
	sort.Strings(lines)
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

func TestFoldWritesTheDesignExampleHistory(t *testing.T) {
	tenant := "t-example"
	total := produce(t, tenant, recordedLog())
	store := testenv.Database(t)
	foldAll(t, store, tenant, total, false)
	got := dumpHistory(t, store, tenant)

	want := []string{
		// The VM of the design's fold example.
		"ComputeInstance vm-7 created " + base.Format(time.RFC3339Nano) + " true",
		"ComputeInstance vm-7 deleted " +
			base.Add(time.Hour+5*time.Minute+12*time.Second).Format(time.RFC3339Nano) + " true",
		"ComputeInstance vm-7 metadata.project " + base.Format(time.RFC3339Nano) + " web",
		"ComputeInstance vm-7 spec.boot_disk.size_gib " + base.Format(time.RFC3339Nano) + " 30",
		"ComputeInstance vm-7 spec.disk_image " + base.Format(time.RFC3339Nano) + " rhel-9",
		"ComputeInstance vm-7 spec.template " + base.Format(time.RFC3339Nano) + " tpl-1",
		"ComputeInstance vm-7 status.instance_type " + base.Format(time.RFC3339Nano) + " small",
		"ComputeInstance vm-7 status.instance_type " +
			base.Add(30*time.Minute).Format(time.RFC3339Nano) + " large",
		"ComputeInstance vm-7 status.state " + base.Format(time.RFC3339Nano) + " STARTING",
		"ComputeInstance vm-7 status.state " +
			base.Add(40*time.Second).Format(time.RFC3339Nano) + " RUNNING",
		"ComputeInstance vm-7 status.state " +
			base.Add(time.Hour).Format(time.RFC3339Nano) + " STOPPED",
		// The volume: set-once-later fields are known from creation.
		"Volume vol-1 created " + base.Add(time.Minute).Format(time.RFC3339Nano) + " true",
		"Volume vol-1 metadata.project " + base.Add(time.Minute).Format(time.RFC3339Nano) + " data",
		"Volume vol-1 spec.storage_tier " + base.Add(time.Minute).Format(time.RFC3339Nano) + " gold",
		"Volume vol-1 status.protocol " + base.Add(time.Minute).Format(time.RFC3339Nano) + " BLOCK",
		"Volume vol-1 status.provisioned_size_gib " +
			base.Add(time.Minute).Format(time.RFC3339Nano) + " 100",
		"Volume vol-1 status.state " +
			base.Add(2*time.Minute).Format(time.RFC3339Nano) + " AVAILABLE",
		"Volume vol-1 status.vendor_volume_id " +
			base.Add(time.Minute).Format(time.RFC3339Nano) + " vend-9",
		// The external IP: the address is a tracked set-once field, and the IP
		// family derives from it at evaluation time. The event carries an
		// attribution, but attachments are ignored for billing, so no
		// attribution fact is derived.
		"ExternalIP ip-1 created " + base.Add(2*time.Minute).Format(time.RFC3339Nano) + " true",
		"ExternalIP ip-1 metadata.project " + base.Add(2*time.Minute).Format(time.RFC3339Nano) + " web",
		"ExternalIP ip-1 spec.pool " + base.Add(2*time.Minute).Format(time.RFC3339Nano) + " pool-a",
		"ExternalIP ip-1 status.address " +
			base.Add(2*time.Minute).Format(time.RFC3339Nano) + " 192.0.2.10",
		"ExternalIP ip-1 status.attached " + base.Add(2*time.Minute).Format(time.RFC3339Nano) + " true",
		"ExternalIP ip-1 status.state " +
			base.Add(2*time.Minute).Format(time.RFC3339Nano) + " ALLOCATED",
	}
	if stringsJoin(got) != stringsJoin(want) {
		t.Fatalf("history mismatch:\ngot:\n%s\nwant:\n%s", stringsJoin(got), stringsJoin(want))
	}
}
