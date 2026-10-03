/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package watch

import (
	"context"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type componentStartTestStore struct {
	state projection.ResourceState
}

func (s *componentStartTestStore) Get(context.Context, string) (*projection.ResourceState, error) {
	state := s.state
	return &state, nil
}

func (s *componentStartTestStore) Upsert(_ context.Context, state projection.ResourceState) error {
	s.state = state
	return nil
}

func (*componentStartTestStore) DeleteIfVersion(context.Context, string, int32) (bool, error) {
	return false, nil
}

func (*componentStartTestStore) ListBillable(context.Context) ([]projection.ResourceState, error) {
	return nil, nil
}

func (*componentStartTestStore) ListAll(context.Context) ([]projection.ResourceState, error) {
	return nil, nil
}

func (*componentStartTestStore) UpdateLastHeartbeat(context.Context, []string, time.Time) error {
	return nil
}

type componentStartTestPublisher struct {
	published int
}

func (p *componentStartTestPublisher) Publish(context.Context, cloudevents.Event) error {
	p.published++
	return nil
}

func TestBuildComponentEventHandlesNilBaseData(t *testing.T) {
	c := &Consumer{}
	baseCE := cloudevents.NewEvent()
	baseCE.SetID("evt-1")
	baseCE.SetSource("osac-metering")
	baseCE.SetType("osac.resource.started.v1")
	// No SetData call: the base event carries zero-length data, so DataAs
	// leaves the target map nil without returning an error.

	ce, err := c.buildComponentEvent(&baseCE, "evt-1/node-a", map[string]any{"component": "worker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data map[string]any
	if err := ce.DataAs(&data); err != nil {
		t.Fatalf("unexpected error reading component event data: %v", err)
	}
	if data["billing_dimensions"] == nil {
		t.Errorf("expected billing_dimensions to be set on the component event even when the base event carried no data")
	}
}

func TestBuildStateContextUsesHeartbeatDeltaOrActiveStart(t *testing.T) {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	lastHeartbeat := start.Add(45 * time.Minute)
	end := start.Add(time.Hour)
	consumer := &Consumer{}

	for _, test := range []struct {
		name            string
		lastHeartbeatAt *time.Time
		want            float64
	}{
		{name: "first close falls back to active start", want: 3600},
		{name: "later close reports only the tail", lastHeartbeatAt: &lastHeartbeat, want: 900},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &projection.ResourceState{
				IsBillable:      true,
				BillableSince:   &start,
				LastHeartbeatAt: test.lastHeartbeatAt,
			}
			got, err := consumer.buildStateContext(state, false, end, nil)
			if err != nil {
				t.Fatalf("buildStateContext() error = %v", err)
			}
			if got.DurationSeconds == nil || *got.DurationSeconds != test.want {
				t.Fatalf("duration_seconds = %v, want %v", got.DurationSeconds, test.want)
			}
			if got.BillableSince == nil || !got.BillableSince.Equal(start) {
				t.Fatalf("billable_since = %v, want %v", got.BillableSince, start)
			}
		})
	}
}

func TestBuildStateContextRejectsBillableCloseWithoutStart(t *testing.T) {
	consumer := &Consumer{}
	state := &projection.ResourceState{
		ResourceID: "missing-billable-start",
		IsBillable: true,
	}

	got, err := consumer.buildStateContext(state, false, time.Now(), nil)
	if err == nil {
		t.Fatalf("buildStateContext() = %#v, want missing billable-since error", got)
	}
	const want = "resource missing-billable-start is billable but has no billable-since timestamp for close"
	if err.Error() != want {
		t.Fatalf("buildStateContext() error = %q, want %q", err, want)
	}
}

func TestComponentDurationSecondsRequiresComponentStart(t *testing.T) {
	consumer := &Consumer{}
	state := &projection.ResourceState{ResourceID: "cluster-scaling"}
	_, err := consumer.componentDurationSeconds(state, "gpu-workers", time.Now())
	const want = `cluster cluster-scaling node_set "gpu-workers" has no component billable-since timestamp`
	if err == nil || err.Error() != want {
		t.Fatalf("componentDurationSeconds() error = %v, want %q", err, want)
	}
}

func TestScalingClusterRequiresComponentStartBeforePublishing(t *testing.T) {
	transitionTime := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	billableSince := transitionTime.Add(-time.Hour)
	event := &privatev1.Event{
		Id:   "scale-event",
		Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Cluster{Cluster: &privatev1.Cluster{
			Id:       "cluster-scaling",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", Version: 2},
		}},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatalf("MapperForEvent() error = %v", err)
	}
	oldDimensions := map[string]any{
		"cluster_template": "ocp-ci-small",
		"release_image":    "4.17.0",
		"components": []any{
			map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": 1},
			map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu", "node_count": 1},
		},
	}
	newDimensions := map[string]any{
		"cluster_template": "ocp-ci-small",
		"release_image":    "4.17.0",
		"components": []any{
			map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": 1},
			map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu", "node_count": 2},
		},
	}
	state := projection.ResourceState{
		ResourceID:         "cluster-scaling",
		ResourceType:       events.ResourceTypeClusterOrder,
		TenantID:           "tenant-1",
		CurrentState:       "READY",
		IsBillable:         true,
		BillableSince:      &billableSince,
		FulfillmentVersion: 1,
		BillingDimensions:  oldDimensions,
		ComponentBillableSince: map[string]time.Time{
			"_control_plane": transitionTime.Add(-time.Hour),
		},
	}
	store := &componentStartTestStore{state: state}
	publisher := &componentStartTestPublisher{}
	consumer := &Consumer{store: store, publisher: publisher}

	err = consumer.handleScalingEvent(context.Background(), event, mapper, &state, transitionTime, 2, "READY", true, newDimensions)
	const want = `cluster cluster-scaling node_set "gpu-workers" has no component billable-since timestamp`
	if err == nil || err.Error() != want {
		t.Fatalf("handleScalingEvent() error = %v, want %q", err, want)
	}
	if publisher.published != 0 {
		t.Fatalf("published events = %d, want 0", publisher.published)
	}
}

func TestPublishSuspendedClusterRequiresComponentStart(t *testing.T) {
	cluster := &privatev1.Cluster{
		Id:       "cluster-suspended",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
	}
	event := &privatev1.Event{
		Id:      "suspend-event",
		Payload: &privatev1.Event_Cluster{Cluster: cluster},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatalf("MapperForEvent() error = %v", err)
	}
	baseCE := cloudevents.NewEvent()
	baseCE.SetType(events.EventSuspended)
	baseCE.SetSource("osac-metering")
	baseCE.SetID(event.GetId())
	if err := baseCE.SetData(cloudevents.ApplicationJSON, map[string]any{}); err != nil {
		t.Fatalf("SetData() error = %v", err)
	}

	consumer := &Consumer{}
	dimensions := map[string]any{
		"cluster_template": "ocp-ci-small",
		"release_image":    "4.17.0",
		"components": []any{
			map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": 1},
			map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu", "node_count": 1},
		},
	}
	for _, test := range []struct {
		name     string
		existing *projection.ResourceState
		want     string
	}{
		{
			name: "component entry missing",
			existing: &projection.ResourceState{
				ResourceID: cluster.GetId(),
				ComponentBillableSince: map[string]time.Time{
					"_control_plane": time.Now(),
				},
			},
			want: `cluster cluster-suspended node_set "gpu-workers" has no component billable-since timestamp`,
		},
		{
			name: "projection state missing",
			want: `cluster cluster-suspended node_set "_control_plane" has no component billable-since timestamp`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err = consumer.publishLifecycleEvents(context.Background(), &baseCE, mapper, event.GetId(), dimensions, test.existing, time.Now())
			if err == nil || err.Error() != test.want {
				t.Fatalf("publishLifecycleEvents() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProjectionIsAheadTreatsTombstoneAsTerminal(t *testing.T) {
	existing := &projection.ResourceState{
		ResourceID:         "bmi-tombstoned",
		Deleted:            true,
		FulfillmentVersion: 4,
		CurrentState:       "RUNNING",
	}

	for _, version := range []int32{1, 4, 5, 100} {
		if !projectionIsAhead(existing, version, "RUNNING", nil, false) {
			t.Errorf("projectionIsAhead() = false for tombstone at incoming version %d", version)
		}
	}
}
