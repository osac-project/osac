/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events_test

import (
	"errors"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
)

var _ = Describe("ResolveCloudEventType", func() {
	It("returns created.v1 for CREATED event type", func() {
		ceType, err := events.ResolveCloudEventType(
			events.TransitionTable{},
			privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			"", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(ceType).To(Equal(events.EventCreated))
	})

	It("returns deleted.v1 for DELETED event type", func() {
		ceType, err := events.ResolveCloudEventType(
			events.TransitionTable{},
			privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			"", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(ceType).To(Equal(events.EventDeleted))
	})

	It("delegates to transition table for UPDATED event type", func() {
		table := events.TransitionTable{
			{From: "A", To: "B"}: {EventType: events.EventStarted},
		}
		ceType, err := events.ResolveCloudEventType(
			table,
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			"A", "B",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(ceType).To(Equal(events.EventStarted))
	})

	It("returns error for UPDATED with missing table entry", func() {
		table := events.TransitionTable{
			{From: "A", To: "B"}: {EventType: events.EventStarted},
		}
		_, err := events.ResolveCloudEventType(
			table,
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			"X", "Y",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected state transition"))
	})

	It("returns error for unknown event type", func() {
		_, err := events.ResolveCloudEventType(
			events.TransitionTable{},
			privatev1.EventType(9999),
			"", "",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported event type"))
	})
})

var _ = Describe("ResolveTransitionTime", func() {
	var (
		creationTS        *timestamppb.Timestamp
		deletionTS        *timestamppb.Timestamp
		stateTransitionTS *timestamppb.Timestamp
	)

	BeforeEach(func() {
		creationTS = timestamppb.New(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
		deletionTS = timestamppb.New(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
		stateTransitionTS = timestamppb.New(time.Date(2026, 7, 1, 11, 30, 0, 0, time.UTC))
	})

	It("returns creation timestamp for CREATED event type", func() {
		t, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			creationTS, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(t).To(Equal(creationTS.AsTime()))
	})

	It("returns deletion timestamp for DELETED event type", func() {
		t, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			creationTS, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(t).To(Equal(deletionTS.AsTime()))
	})

	It("returns state transition timestamp for UPDATED event type", func() {
		t, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			creationTS, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(t).To(Equal(stateTransitionTS.AsTime()))
	})

	It("returns ErrUnsupportedEvent for OBJECT_SIGNALED", func() {
		_, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED,
			creationTS, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrUnsupportedEvent)).To(BeTrue())
	})

	It("returns ErrUnsupportedEvent for unknown event type", func() {
		_, err := events.ResolveTransitionTime(
			privatev1.EventType(9999),
			creationTS, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrUnsupportedEvent)).To(BeTrue())
	})

	It("returns ErrDataQuality when creation timestamp is nil for CREATED", func() {
		_, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			nil, deletionTS, stateTransitionTS, "res-1",
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("returns ErrDataQuality when deletion timestamp is nil for DELETED", func() {
		_, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			creationTS, nil, stateTransitionTS, "res-1",
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("returns ErrDataQuality when state transition timestamp is nil for UPDATED", func() {
		_, err := events.ResolveTransitionTime(
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			creationTS, deletionTS, nil, "res-1",
		)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})
})

var _ = Describe("BuildResourceEvents", func() {
	simpleBuildFn := func(dims map[string]any, eventID string) (cloudevents.Event, error) {
		ce := cloudevents.NewEvent()
		ce.SetID(eventID)
		if err := ce.SetData(cloudevents.ApplicationJSON, dims); err != nil {
			return cloudevents.Event{}, fmt.Errorf("setting data: %w", err)
		}
		return ce, nil
	}

	It("returns a single event for compute_instance", func() {
		dims := map[string]any{"instance_type": "large-gpu"}
		result, err := events.BuildResourceEvents(
			events.ResourceTypeComputeInstance, dims, "evt-ci-1", simpleBuildFn,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(1))
		Expect(result[0].ID()).To(Equal("evt-ci-1"))
	})

	It("decomposes cluster_order into per-component events", func() {
		dims := map[string]any{
			"cluster_template": "ocp-ci-small",
			"components": []any{
				map[string]any{
					"node_set":   "_control_plane",
					"component":  "control_plane",
					"host_type":  "_control_plane",
					"node_count": int32(1),
				},
				map[string]any{
					"node_set":   "gpu-workers",
					"component":  "worker",
					"host_type":  "gpu-h100",
					"node_count": int32(2),
				},
			},
		}

		result, err := events.BuildResourceEvents(
			events.ResourceTypeClusterOrder, dims, "evt-cl-1", simpleBuildFn,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(2))
	})

	It("returns error for unknown resource type", func() {
		_, err := events.BuildResourceEvents(
			"unknown_resource", map[string]any{}, "evt-1", simpleBuildFn,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown resource type for event decomposition"))
	})
})

var _ = Describe("BuildDimensionChangeEvents", func() {
	simpleBuildFn := func(dims map[string]any, eventID string) (cloudevents.Event, error) {
		ce := cloudevents.NewEvent()
		ce.SetID(eventID)
		if err := ce.SetData(cloudevents.ApplicationJSON, dims); err != nil {
			return cloudevents.Event{}, fmt.Errorf("setting data: %w", err)
		}
		return ce, nil
	}

	It("returns a single event for compute_instance (VMaaS)", func() {
		oldDims := map[string]any{"instance_type": "m5.large"}
		newDims := map[string]any{"instance_type": "m5.xlarge"}
		result, err := events.BuildDimensionChangeEvents(
			events.ResourceTypeComputeInstance, oldDims, newDims, "evt-ci-1", simpleBuildFn,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(1))
		Expect(result[0].ID()).To(Equal("evt-ci-1"))
	})

	It("returns per-changed-component events for cluster_order (CaaS)", func() {
		oldDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(4)},
			},
		}
		result, err := events.BuildDimensionChangeEvents(
			events.ResourceTypeClusterOrder, oldDims, newDims, "evt-cl-1", simpleBuildFn,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(1))
		Expect(result[0].ID()).To(Equal("evt-cl-1/gpu-workers"))
	})

	It("returns nil for cluster_order with no changes", func() {
		dims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
			},
		}
		result, err := events.BuildDimensionChangeEvents(
			events.ResourceTypeClusterOrder, dims, dims, "evt-cl-1", simpleBuildFn,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("returns error for unknown resource type", func() {
		_, err := events.BuildDimensionChangeEvents(
			"unknown_resource", map[string]any{}, map[string]any{}, "evt-1", simpleBuildFn,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown resource type for dimension change"))
	})
})

var _ = Describe("resolveTransition (indirect via ResolveCloudEventType)", func() {
	It("returns correct event type for an exact table match", func() {
		table := events.TransitionTable{
			{From: "STOPPED", To: "RUNNING"}: {EventType: events.EventResumed},
		}
		ceType, err := events.ResolveCloudEventType(
			table,
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			"STOPPED", "RUNNING",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(ceType).To(Equal(events.EventResumed))
	})

	It("returns error for a missing table entry", func() {
		table := events.TransitionTable{
			{From: "STOPPED", To: "RUNNING"}: {EventType: events.EventResumed},
		}
		_, err := events.ResolveCloudEventType(
			table,
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			"FAILED", "RUNNING",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected state transition"))
	})

	It("does not fall back to wildcard-like partial matches", func() {
		// Table has only {"A", "B"} entry. If wildcards existed,
		// {"C", "B"} might match a wildcard on From. Verify it does not.
		table := events.TransitionTable{
			{From: "A", To: "B"}: {EventType: events.EventStarted},
		}
		_, err := events.ResolveCloudEventType(
			table,
			privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			"C", "B",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected state transition"))
	})
})
