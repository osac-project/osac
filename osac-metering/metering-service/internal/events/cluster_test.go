package events_test

import (
	"encoding/json"
	"errors"
	"fmt"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
)

var _ = Describe("CaaS Cluster Mapper", func() {
	var cl *privatev1.Cluster

	BeforeEach(func() {
		cl = &privatev1.Cluster{
			Id: "cluster-abc-123",
			Metadata: &privatev1.Metadata{
				Tenant:            "tenant-1",
				Project:           "project-alpha",
				Version:           5,
				CreationTimestamp: timestamppb.Now(),
			},
			Spec: &privatev1.ClusterSpec{
				Template:    &privatev1.ClusterTemplateReference{Id: "ocp-ci-small", Name: "ocp-ci-small"},
				CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cluster-catalog-1", Name: "cluster-catalog-1"},
				Version:     &privatev1.ClusterVersionReference{Id: "4.17.0", Name: "4.17.0"},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 2},
					"cpu-workers": {HostType: &privatev1.HostTypeReference{Name: "cpu-only"}, Size: 3},
				},
			},
			Status: &privatev1.ClusterStatus{
				State:               privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				StateTransitionTime: timestamppb.Now(),
			},
		}
	})

	Context("state machine — full transition matrix", func() {
		DescribeTable("resolves correct CloudEvent type for state transitions",
			func(currentState privatev1.ClusterState, previousState string, expectedType string, expectSkip bool) {
				cl.Status.State = currentState

				event := &privatev1.Event{
					Id:      "evt-1",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_Cluster{Cluster: cl},
				}

				stateCtx := &events.StateContext{PreviousState: previousState}
				ce, err := mapEvent(event, stateCtx)

				if expectSkip {
					Expect(err).To(HaveOccurred())
					Expect(errors.Is(err, events.ErrSkipTransition)).To(BeTrue())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(ce.Type()).To(Equal(expectedType))
				}
			},
			// --- Started: first billable state (no previous) ---
			Entry("initial PROGRESSING (prev=empty) -> started.v1",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.StateEmpty, events.EventStarted, false),
			Entry("initial READY (prev=empty) -> started.v1",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.StateEmpty, events.EventStarted, false),

			// --- Skip: first observed in non-billable state ---
			Entry("initial FAILED (prev=empty) -> skip",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.StateEmpty, "", true),
			Entry("initial DELETING (prev=empty) -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.StateEmpty, "", true),
			Entry("initial DELETE_FAILED (prev=empty) -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.StateEmpty, "", true),
			Entry("initial UNSPECIFIED (prev=empty) -> skip",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.StateEmpty, "", true),

			// --- Resumed: non-billable to billable ---
			Entry("FAILED -> PROGRESSING -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateFailed, events.EventResumed, false),
			Entry("FAILED -> READY -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateFailed, events.EventResumed, false),
			Entry("DELETING -> PROGRESSING -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateDeleting, events.EventResumed, false),
			Entry("DELETING -> READY -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateDeleting, events.EventResumed, false),
			Entry("DELETE_FAILED -> PROGRESSING -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateDeleteFailed, events.EventResumed, false),
			Entry("DELETE_FAILED -> READY -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateDeleteFailed, events.EventResumed, false),
			Entry("UNSPECIFIED -> PROGRESSING -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateUnspecified, events.EventResumed, false),
			Entry("UNSPECIFIED -> READY -> resumed.v1",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateUnspecified, events.EventResumed, false),

			// --- Suspended: billable to non-billable ---
			Entry("PROGRESSING -> FAILED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateProgressing, events.EventSuspended, false),
			Entry("PROGRESSING -> DELETING -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateProgressing, events.EventSuspended, false),
			Entry("READY -> FAILED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateReady, events.EventSuspended, false),
			Entry("READY -> DELETING -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateReady, events.EventSuspended, false),
			Entry("PROGRESSING -> UNSPECIFIED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateProgressing, events.EventSuspended, false),
			Entry("READY -> UNSPECIFIED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateReady, events.EventSuspended, false),
			Entry("READY -> DELETE_FAILED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateReady, events.EventSuspended, false),
			Entry("PROGRESSING -> DELETE_FAILED -> suspended.v1",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateProgressing, events.EventSuspended, false),

			// --- Skip: billable to billable (no billing boundary) ---
			Entry("PROGRESSING -> READY -> skip",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateProgressing, "", true),
			Entry("READY -> PROGRESSING -> skip",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateReady, "", true),
			Entry("PROGRESSING -> PROGRESSING -> skip (same-state)",
				privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, events.ClusterStateProgressing, "", true),
			Entry("READY -> READY -> skip (same-state, scaling)",
				privatev1.ClusterState_CLUSTER_STATE_READY, events.ClusterStateReady, "", true),

			// --- Skip: non-billable same-state ---
			Entry("FAILED -> FAILED -> skip (same-state)",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateFailed, "", true),
			Entry("DELETING -> DELETING -> skip (same-state)",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateDeleting, "", true),
			Entry("DELETE_FAILED -> DELETE_FAILED -> skip (same-state)",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateDeleteFailed, "", true),
			Entry("UNSPECIFIED -> UNSPECIFIED -> skip (same-state)",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateUnspecified, "", true),

			// --- Skip: non-billable to non-billable (cross-state) ---
			Entry("FAILED -> DELETING -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateFailed, "", true),
			Entry("FAILED -> DELETE_FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateFailed, "", true),
			Entry("DELETING -> DELETE_FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateDeleting, "", true),
			Entry("DELETE_FAILED -> DELETING -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateDeleteFailed, "", true),
			Entry("DELETING -> FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateDeleting, "", true),
			Entry("DELETE_FAILED -> FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateDeleteFailed, "", true),
			Entry("UNSPECIFIED -> FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_FAILED, events.ClusterStateUnspecified, "", true),
			Entry("UNSPECIFIED -> DELETING -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETING, events.ClusterStateUnspecified, "", true),
			Entry("UNSPECIFIED -> DELETE_FAILED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED, events.ClusterStateUnspecified, "", true),
			Entry("FAILED -> UNSPECIFIED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateFailed, "", true),
			Entry("DELETING -> UNSPECIFIED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateDeleting, "", true),
			Entry("DELETE_FAILED -> UNSPECIFIED -> skip",
				privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, events.ClusterStateDeleteFailed, "", true),
		)

		It("returns error for unknown state (missing table entry)", func() {
			cl.Status.State = privatev1.ClusterState(9999)

			event := &privatev1.Event{
				Id:      "evt-unknown",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			_, err := mapEvent(event, &events.StateContext{PreviousState: events.ClusterStateReady})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unexpected state transition"))
			Expect(errors.Is(err, events.ErrSkipTransition)).To(BeFalse())
		})

		It("maps OBJECT_CREATED to created.v1", func() {
			event := &privatev1.Event{
				Id:      "evt-create",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventCreated))
		})

		It("maps OBJECT_DELETED to deleted.v1", func() {
			cl.Metadata.DeletionTimestamp = timestamppb.Now()

			event := &privatev1.Event{
				Id:      "evt-delete",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventDeleted))
		})
	})

	Context("resource mapper fields", func() {
		It("returns resource_type=cluster_order", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["resource_type"]).To(Equal(events.ResourceTypeClusterOrder))
		})

		It("extracts resource_id from cluster ID", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osacresourceid"]).To(Equal("cluster-abc-123"))
		})

		It("extracts tenant_id, project_id, template_id, catalog_item_id from metadata and spec", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["tenant_id"]).To(Equal("tenant-1"))
			Expect(data["project_id"]).To(Equal("project-alpha"))
			Expect(data["template_id"]).To(Equal("ocp-ci-small"))
			Expect(data["catalog_item_id"]).To(Equal("cluster-catalog-1"))
		})

		It("trims CLUSTER_STATE_ prefix from current_state", func() {
			cl.Status.State = privatev1.ClusterState_CLUSTER_STATE_READY
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["current_state"]).To(Equal(events.ClusterStateReady))
		})
	})

	Context("billability", func() {
		It("PROGRESSING is billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateProgressing)).To(BeTrue())
		})

		It("READY is billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateReady)).To(BeTrue())
		})

		It("FAILED is not billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateFailed)).To(BeFalse())
		})

		It("DELETING is not billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateDeleting)).To(BeFalse())
		})

		It("DELETE_FAILED is not billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateDeleteFailed)).To(BeFalse())
		})

		It("UNSPECIFIED is not billable", func() {
			Expect(events.IsClusterBillableState(events.ClusterStateUnspecified)).To(BeFalse())
		})
	})

	Context("billing dimensions", func() {
		It("includes cluster_template, release_image, and full components breakdown", func() {
			dims := events.ClusterBillingDimensions(cl)
			Expect(dims["cluster_template"]).To(Equal("ocp-ci-small"))
			Expect(dims["release_image"]).To(Equal("4.17.0"))

			components, ok := dims["components"].([]any)
			Expect(ok).To(BeTrue(), "components must be []any for DecomposeClusterComponents compatibility")
			Expect(components).To(HaveLen(3))

			cp := components[0].(map[string]any)
			Expect(cp["node_set"]).To(Equal("_control_plane"))
			Expect(cp["component"]).To(Equal("control_plane"))
			Expect(cp["host_type"]).To(Equal("_control_plane"))
			Expect(cp["node_count"]).To(Equal(int32(1)))
		})

		It("sorts worker node sets by key for deterministic ordering", func() {
			dims := events.ClusterBillingDimensions(cl)
			components := dims["components"].([]any)

			// control_plane first, then sorted by node set key: "cpu-workers" < "gpu-workers"
			w1 := components[1].(map[string]any)
			Expect(w1["node_set"]).To(Equal("cpu-workers"))
			Expect(w1["host_type"]).To(Equal("cpu-only"))
			Expect(w1["node_count"]).To(Equal(int32(3)))
			w2 := components[2].(map[string]any)
			Expect(w2["node_set"]).To(Equal("gpu-workers"))
			Expect(w2["host_type"]).To(Equal("gpu-h100"))
			Expect(w2["node_count"]).To(Equal(int32(2)))
		})

		It("omits release_image when nil", func() {
			cl.Spec.Version = nil
			dims := events.ClusterBillingDimensions(cl)
			Expect(dims).NotTo(HaveKey("release_image"))
		})

		It("handles nil spec gracefully", func() {
			cl.Spec = nil
			dims := events.ClusterBillingDimensions(cl)
			Expect(dims).To(BeEmpty())
		})

		It("handles nil node_sets with control plane only", func() {
			cl.Spec.NodeSets = nil
			dims := events.ClusterBillingDimensions(cl)
			components := dims["components"].([]any)
			Expect(components).To(HaveLen(1))
			cp := components[0].(map[string]any)
			Expect(cp["node_set"]).To(Equal("_control_plane"))
			Expect(cp["component"]).To(Equal("control_plane"))
		})

		It("DecomposeClusterComponents works on fresh (non-JSONB) output", func() {
			dims := events.ClusterBillingDimensions(cl)
			records, err := events.DecomposeClusterComponents(dims)
			Expect(err).NotTo(HaveOccurred())
			Expect(records).To(HaveLen(3))
			Expect(records[0].NodeSet).To(Equal("_control_plane"))
			Expect(records[0].Component).To(Equal("control_plane"))
			Expect(records[0].NodeCount).To(Equal(int32(1)))
			Expect(records[1].NodeSet).To(Equal("cpu-workers"))
			Expect(records[1].Component).To(Equal("worker"))
			Expect(records[2].NodeSet).To(Equal("gpu-workers"))
			Expect(records[2].Component).To(Equal("worker"))
		})
	})

	Context("transition time", func() {
		It("uses creation_timestamp for CREATED events", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time()).To(Equal(cl.Metadata.CreationTimestamp.AsTime()))
		})

		It("uses state_transition_time for UPDATED events", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time()).To(Equal(cl.Status.StateTransitionTime.AsTime()))
		})

		It("rejects CREATED without creation_timestamp", func() {
			cl.Metadata.CreationTimestamp = nil
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("rejects UPDATED without state_transition_time", func() {
			cl.Status.StateTransitionTime = nil
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})
	})

	Context("error handling", func() {
		It("rejects events with empty resource_id", func() {
			cl.Id = ""
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("rejects events with empty tenant_id", func() {
			cl.Metadata.Tenant = ""
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})
	})
})

var _ = Describe("DecomposeClusterEvents", func() {
	It("produces N+1 events with per-component dims and deterministic IDs", func() {
		dims := map[string]any{
			"cluster_template": "ocp-ci-small",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		built := []string{}
		result, err := events.DecomposeClusterEvents(dims, "base-id", func(d map[string]any, eventID string) (cloudevents.Event, error) {
			built = append(built, eventID)
			ce := cloudevents.NewEvent()
			ce.SetID(eventID)
			Expect(ce.SetData(cloudevents.ApplicationJSON, d)).To(Succeed())
			return ce, nil
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(2))
		Expect(built).To(ConsistOf("base-id/_control_plane", "base-id/gpu-workers"))
	})

	It("returns ErrDataQuality when cluster has no components", func() {
		dims := map[string]any{"cluster_template": "ocp-ci-small"}

		_, err := events.DecomposeClusterEvents(dims, "base-id", func(d map[string]any, eventID string) (cloudevents.Event, error) {
			Fail("buildFn should not be called")
			return cloudevents.Event{}, nil
		})

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("returns ErrDataQuality when components array is empty", func() {
		dims := map[string]any{
			"cluster_template": "ocp-ci-small",
			"components":       []any{},
		}

		_, err := events.DecomposeClusterEvents(dims, "base-id", func(d map[string]any, eventID string) (cloudevents.Event, error) {
			Fail("buildFn should not be called")
			return cloudevents.Event{}, nil
		})

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("propagates buildFn errors", func() {
		dims := map[string]any{
			"components": []any{
				map[string]any{"node_set": "_control_plane", "node_count": int32(1)},
			},
		}

		_, err := events.DecomposeClusterEvents(dims, "base-id", func(d map[string]any, eventID string) (cloudevents.Event, error) {
			return cloudevents.Event{}, fmt.Errorf("kafka down")
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kafka down"))
	})
})

var _ = Describe("DecomposeClusterComponents", func() {
	It("decomposes 1 control plane + 2 worker sets into 3 records", func() {
		dims := map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "quay.io/ocp:4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "cpu-workers", "component": "worker", "host_type": "cpu-only", "node_count": int32(3)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		records, err := events.DecomposeClusterComponents(dims)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(3))

		Expect(records[0].NodeSet).To(Equal("_control_plane"))
		Expect(records[0].Component).To(Equal("control_plane"))
		Expect(records[0].HostType).To(Equal("_control_plane"))
		Expect(records[0].NodeCount).To(Equal(int32(1)))
		Expect(records[0].ClusterTemplate).To(Equal("ocp-ci-small"))
		Expect(records[0].ReleaseImage).To(Equal("quay.io/ocp:4.17.0"))

		Expect(records[1].NodeSet).To(Equal("cpu-workers"))
		Expect(records[1].Component).To(Equal("worker"))
		Expect(records[1].HostType).To(Equal("cpu-only"))
		Expect(records[1].NodeCount).To(Equal(int32(3)))

		Expect(records[2].NodeSet).To(Equal("gpu-workers"))
		Expect(records[2].Component).To(Equal("worker"))
		Expect(records[2].HostType).To(Equal("gpu-h100"))
		Expect(records[2].NodeCount).To(Equal(int32(2)))
	})

	It("handles node_count as float64 (JSONB round-trip)", func() {
		dims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": float64(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": float64(2)},
			},
		}

		records, err := events.DecomposeClusterComponents(dims)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		Expect(records[0].NodeCount).To(Equal(int32(1)))
		Expect(records[1].NodeCount).To(Equal(int32(2)))
	})

	It("returns ErrDataQuality when no components key", func() {
		dims := map[string]any{"cluster_template": "tmpl"}
		_, err := events.DecomposeClusterComponents(dims)
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("returns ErrDataQuality for empty dims", func() {
		_, err := events.DecomposeClusterComponents(map[string]any{})
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

})

var _ = Describe("ComponentRecord", func() {
	It("produces flat billing dimensions", func() {
		cr := events.ComponentRecord{
			NodeSet:         "gpu-workers",
			Component:       "worker",
			HostType:        "gpu-h100",
			NodeCount:       2,
			ClusterTemplate: "ocp-ci-small",
			ReleaseImage:    "quay.io/ocp:4.17.0",
		}

		flat := cr.FlatBillingDimensions()
		Expect(flat["cluster_template"]).To(Equal("ocp-ci-small"))
		Expect(flat["release_image"]).To(Equal("quay.io/ocp:4.17.0"))
		Expect(flat["node_set"]).To(Equal("gpu-workers"))
		Expect(flat["component"]).To(Equal("worker"))
		Expect(flat["host_type"]).To(Equal("gpu-h100"))
		Expect(flat["node_count"]).To(Equal(int32(2)))
	})

	It("omits release_image when empty", func() {
		cr := events.ComponentRecord{
			NodeSet:         "_control_plane",
			Component:       "control_plane",
			HostType:        "_control_plane",
			NodeCount:       1,
			ClusterTemplate: "tmpl",
		}

		flat := cr.FlatBillingDimensions()
		Expect(flat).NotTo(HaveKey("release_image"))
	})
})

var _ = Describe("ComponentEventID", func() {
	It("produces deterministic IDs", func() {
		comp := events.ComponentRecord{NodeSet: "gpu-workers", Component: "worker", HostType: "gpu-h100"}
		id1 := events.ComponentEventID("evt-123", comp)
		id2 := events.ComponentEventID("evt-123", comp)
		Expect(id1).To(Equal(id2))
		Expect(id1).To(Equal("evt-123/gpu-workers"))
	})

	It("produces different IDs for different components", func() {
		cp := events.ComponentRecord{NodeSet: "_control_plane", Component: "control_plane", HostType: "_control_plane"}
		worker := events.ComponentRecord{NodeSet: "gpu-workers", Component: "worker", HostType: "gpu-h100"}
		Expect(events.ComponentEventID("evt-1", cp)).NotTo(Equal(events.ComponentEventID("evt-1", worker)))
	})

	It("produces different IDs for different base events", func() {
		comp := events.ComponentRecord{NodeSet: "gpu-workers", Component: "worker", HostType: "gpu-h100"}
		Expect(events.ComponentEventID("evt-1", comp)).NotTo(Equal(events.ComponentEventID("evt-2", comp)))
	})
})

var _ = Describe("ChangedComponents", func() {
	It("detects node_count change in a single worker set", func() {
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

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1))
		Expect(changed[0].HostType).To(Equal("gpu-h100"))
		Expect(changed[0].NodeCount).To(Equal(int32(4)))
	})

	It("returns empty when nothing changed", func() {
		dims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
			},
		}

		changed, err := events.ChangedComponents(dims, dims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeEmpty())
	})

	It("detects newly added worker node set", func() {
		oldDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1))
		Expect(changed[0].HostType).To(Equal("gpu-h100"))
	})

	It("detects removed worker node set with NodeCount=0", func() {
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
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1))
		Expect(changed[0].HostType).To(Equal("gpu-h100"))
		Expect(changed[0].NodeCount).To(Equal(int32(0)))
		Expect(changed[0].NodeSet).To(Equal("gpu-workers"))
	})

	It("preserves distinct NodeSet on multi-removal for unique event IDs", func() {
		oldDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "pool-a", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
				map[string]any{"node_set": "pool-b", "component": "worker", "host_type": "cpu-only", "node_count": int32(3)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(2))

		nodeSetToHostType := map[string]string{}
		for _, c := range changed {
			Expect(c.NodeCount).To(Equal(int32(0)))
			Expect(c.NodeSet).NotTo(BeEmpty())
			id := events.ComponentEventID("evt-1", c)
			Expect(id).NotTo(Equal("evt-1/"))
			nodeSetToHostType[c.NodeSet] = c.HostType
		}
		Expect(nodeSetToHostType).To(HaveLen(2))
		Expect(nodeSetToHostType["pool-a"]).To(Equal("gpu-h100"))
		Expect(nodeSetToHostType["pool-b"]).To(Equal("cpu-only"))
	})

	It("sets IsNew=true for newly-added components and IsNew=false for modified", func() {
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
				map[string]any{"node_set": "tpu-workers", "component": "worker", "host_type": "tpu-v5", "node_count": int32(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(2))

		byNodeSet := map[string]events.ComponentRecord{}
		for _, c := range changed {
			byNodeSet[c.NodeSet] = c
		}

		Expect(byNodeSet["gpu-workers"].IsNew).To(BeFalse(),
			"modified component should have IsNew=false")
		Expect(byNodeSet["tpu-workers"].IsNew).To(BeTrue(),
			"newly-added component should have IsNew=true")
	})

	It("detects host_type change within a node set", func() {
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
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-a100", "node_count": int32(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1))
		Expect(changed[0].HostType).To(Equal("gpu-a100"))
		Expect(changed[0].NodeCount).To(Equal(int32(2)))
	})

	It("detects cluster_template change with unchanged node_count and host_type", func() {
		oldDims := map[string]any{
			"cluster_template": "ocp-ci-small",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "ocp-ci-large",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1), "a cluster_template-only change must still be reported so it can be published")
		Expect(changed[0].ClusterTemplate).To(Equal("ocp-ci-large"))
	})

	It("detects release_image change with unchanged node_count and host_type", func() {
		oldDims := map[string]any{
			"cluster_template": "tmpl",
			"release_image":    "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "tmpl",
			"release_image":    "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(HaveLen(1), "a cluster upgrade (release_image-only change) must still be reported so it can be published")
		Expect(changed[0].ReleaseImage).To(Equal("quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64"))
	})

	It("handles int32 vs float64 from JSONB round-trip", func() {
		oldDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		newDims := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": float64(2)},
			},
		}

		changed, err := events.ChangedComponents(oldDims, newDims)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeEmpty())
	})
})

var _ = Describe("DimensionsEqual with nested CaaS components", func() {
	It("matches identical billing dimensions with components array", func() {
		a := map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "quay.io/ocp:4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		b := map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "quay.io/ocp:4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		Expect(events.DimensionsEqual(a, b)).To(BeTrue())
	})

	It("detects node_count change in components array", func() {
		a := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		b := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(4)},
			},
		}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse())
	})

	It("detects different number of components", func() {
		a := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
			},
		}
		b := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse())
	})

	It("handles JSONB round-trip: int32 stored, float64 on read", func() {
		stored := map[string]any{
			"cluster_template": "tmpl",
			"release_image":    "quay.io/ocp:4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		// Simulate JSONB round-trip: marshal then unmarshal
		data, err := json.Marshal(stored)
		Expect(err).NotTo(HaveOccurred())

		var roundTripped map[string]any
		Expect(json.Unmarshal(data, &roundTripped)).To(Succeed())

		// After JSON round-trip: int32 becomes float64, []map becomes []any
		Expect(events.DimensionsEqual(stored, roundTripped)).To(BeTrue())
	})

	It("detects node_count change after JSONB round-trip", func() {
		stored := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		data, err := json.Marshal(stored)
		Expect(err).NotTo(HaveOccurred())

		var roundTripped map[string]any
		Expect(json.Unmarshal(data, &roundTripped)).To(Succeed())

		// Change node_count in the incoming (non-round-tripped) version
		incoming := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(4)},
			},
		}

		Expect(events.DimensionsEqual(roundTripped, incoming)).To(BeFalse())
	})

	It("round-trip preserves equality for multi-component clusters", func() {
		original := map[string]any{
			"cluster_template": "ocp-ci-small",
			"release_image":    "quay.io/ocp:4.17.0",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "cpu-workers", "component": "worker", "host_type": "cpu-only", "node_count": int32(3)},
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}

		data, err := json.Marshal(original)
		Expect(err).NotTo(HaveOccurred())
		var rt1 map[string]any
		Expect(json.Unmarshal(data, &rt1)).To(Succeed())

		// Round-trip again to simulate double-read
		data2, err := json.Marshal(rt1)
		Expect(err).NotTo(HaveOccurred())
		var rt2 map[string]any
		Expect(json.Unmarshal(data2, &rt2)).To(Succeed())

		Expect(events.DimensionsEqual(rt1, rt2)).To(BeTrue())
	})

	It("detects host_type change within components", func() {
		a := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
			},
		}
		b := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-a100", "node_count": int32(2)},
			},
		}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse())
	})

	It("treats different component array order as unequal", func() {
		a := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "_control_plane", "node_count": int32(1)},
				map[string]any{"node_set": "gpu-workers", "node_count": int32(2)},
			},
		}
		b := map[string]any{
			"cluster_template": "tmpl",
			"components": []any{
				map[string]any{"node_set": "gpu-workers", "node_count": int32(2)},
				map[string]any{"node_set": "_control_plane", "node_count": int32(1)},
			},
		}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse(),
			"component array order matters — ClusterBillingDimensions sorts keys for deterministic ordering")
	})
})

var _ = Describe("CaaS transition table completeness", func() {
	stateProtoMap := map[string]privatev1.ClusterState{
		events.ClusterStateProgressing:  privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
		events.ClusterStateReady:        privatev1.ClusterState_CLUSTER_STATE_READY,
		events.ClusterStateFailed:       privatev1.ClusterState_CLUSTER_STATE_FAILED,
		events.ClusterStateDeleting:     privatev1.ClusterState_CLUSTER_STATE_DELETING,
		events.ClusterStateDeleteFailed: privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED,
		events.ClusterStateUnspecified:  privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED,
	}

	It("covers every (from, to) state pair from all proto states plus empty initial", func() {
		fromStates := []string{events.StateEmpty, events.ClusterStateProgressing, events.ClusterStateReady, events.ClusterStateFailed, events.ClusterStateDeleting, events.ClusterStateDeleteFailed, events.ClusterStateUnspecified}
		toStates := []string{events.ClusterStateProgressing, events.ClusterStateReady, events.ClusterStateFailed, events.ClusterStateDeleting, events.ClusterStateDeleteFailed, events.ClusterStateUnspecified}

		for _, from := range fromStates {
			for _, to := range toStates {
				cl := &privatev1.Cluster{
					Id:       "cl-completeness",
					Metadata: &privatev1.Metadata{Tenant: "t", CreationTimestamp: timestamppb.Now()},
					Spec:     &privatev1.ClusterSpec{},
					Status: &privatev1.ClusterStatus{
						State:               stateProtoMap[to],
						StateTransitionTime: timestamppb.Now(),
					},
				}

				event := &privatev1.Event{
					Id:      "evt-completeness",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_Cluster{Cluster: cl},
				}

				stateCtx := &events.StateContext{PreviousState: from}
				_, err := mapEvent(event, stateCtx)

				Expect(err == nil ||
					errors.Is(err, events.ErrSkipTransition) ||
					errors.Is(err, events.ErrTransientState)).To(BeTrue(),
					"transition %s -> %s returned unexpected error: %v", from, to, err)
			}
		}
	})
})
