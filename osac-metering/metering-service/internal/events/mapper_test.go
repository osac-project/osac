package events_test

import (
	"encoding/json"
	"errors"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
)

func mapEvent(event *privatev1.Event, stateCtx *events.StateContext) (*cloudevents.Event, error) {
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		return nil, err
	}
	return events.MapWatchEvent(event, mapper, stateCtx)
}

var _ = Describe("MapWatchEvent", func() {

	var (
		instanceType = "large-gpu"
		ci           *privatev1.ComputeInstance
	)

	BeforeEach(func() {
		ci = &privatev1.ComputeInstance{
			Id: "ci-abc-123",
			Metadata: &privatev1.Metadata{
				Tenant:            "tenant-1",
				Project:           "project-alpha",
				CreationTimestamp: timestamppb.Now(),
			},
			Spec: &privatev1.ComputeInstanceSpec{
				Template:     &privatev1.ComputeInstanceTemplateReference{Name: "tmpl-gpu"},
				CatalogItem:  &privatev1.ComputeInstanceCatalogItemReference{Name: "catalog-item-1"},
				InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
				Image: &privatev1.ComputeInstanceImage{
					SourceRef: "rhel-10.2-x86_64",
				},
				BootDisk: &privatev1.ComputeInstanceDisk{
					SizeGib: 100,
				},
			},
			Status: &privatev1.ComputeInstanceStatus{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
				StateTransitionTime: timestamppb.Now(),
			},
		}
	})

	Context("VMaaS state machine -- full transition matrix", func() {
		DescribeTable("resolves correct CloudEvent type for state transitions",
			func(currentState privatev1.ComputeInstanceState, previousState string, expectedType string, expectSkip, expectTransient bool) {
				ci.Status.State = currentState
				event := &privatev1.Event{
					Id:      "evt-1",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
				}
				stateCtx := &events.StateContext{PreviousState: previousState}
				ce, err := mapEvent(event, stateCtx)
				if expectSkip {
					Expect(err).To(HaveOccurred())
					Expect(errors.Is(err, events.ErrSkipTransition)).To(BeTrue())
				} else if expectTransient {
					Expect(err).To(HaveOccurred())
					Expect(errors.Is(err, events.ErrTransientState)).To(BeTrue())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(ce.Type()).To(Equal(expectedType))
				}
			},

			// --- From "" (initial observation) ---
			Entry("initial -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "", events.EventStarted, false, false),
			Entry("initial -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "", "", true, false),
			Entry("initial -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "", "", true, false),
			Entry("initial -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "", "", true, false),
			Entry("initial -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "", "", false, true),
			Entry("initial -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "", "", false, true),
			Entry("initial -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "", "", true, false),
			Entry("initial -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "", "", true, false),

			// --- From RUNNING ---
			Entry("RUNNING -> RUNNING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "RUNNING", "", true, false),
			Entry("RUNNING -> STOPPED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "RUNNING", events.EventSuspended, false, false),
			Entry("RUNNING -> PAUSED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "RUNNING", events.EventSuspended, false, false),
			Entry("RUNNING -> FAILED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "RUNNING", events.EventSuspended, false, false),
			Entry("RUNNING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "RUNNING", "", false, true),
			Entry("RUNNING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "RUNNING", "", false, true),
			Entry("RUNNING -> DELETING -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "RUNNING", events.EventSuspended, false, false),
			Entry("RUNNING -> UNSPECIFIED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "RUNNING", events.EventSuspended, false, false),

			// --- From STOPPED ---
			Entry("STOPPED -> RUNNING -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPED", events.EventResumed, false, false),
			Entry("STOPPED -> STOPPED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STOPPED", "", true, false),
			Entry("STOPPED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STOPPED", "", true, false),
			Entry("STOPPED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STOPPED", "", true, false),
			Entry("STOPPED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STOPPED", "", false, true),
			Entry("STOPPED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STOPPED", "", false, true),
			Entry("STOPPED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STOPPED", "", true, false),
			Entry("STOPPED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STOPPED", "", true, false),

			// --- From PAUSED ---
			Entry("PAUSED -> RUNNING -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "PAUSED", events.EventResumed, false, false),
			Entry("PAUSED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "PAUSED", "", true, false),
			Entry("PAUSED -> PAUSED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "PAUSED", "", true, false),
			Entry("PAUSED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "PAUSED", "", true, false),
			Entry("PAUSED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "PAUSED", "", false, true),
			Entry("PAUSED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "PAUSED", "", false, true),
			Entry("PAUSED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "PAUSED", "", true, false),
			Entry("PAUSED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "PAUSED", "", true, false),

			// --- From FAILED ---
			Entry("FAILED -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "FAILED", events.EventStarted, false, false),
			Entry("FAILED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "FAILED", "", true, false),
			Entry("FAILED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "FAILED", "", true, false),
			Entry("FAILED -> FAILED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "FAILED", "", true, false),
			Entry("FAILED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "FAILED", "", false, true),
			Entry("FAILED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "FAILED", "", false, true),
			Entry("FAILED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "FAILED", "", true, false),
			Entry("FAILED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "FAILED", "", true, false),

			// --- From STOPPING ---
			Entry("STOPPING -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPING", events.EventStarted, false, false),
			Entry("STOPPING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STOPPING", "", true, false),
			Entry("STOPPING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STOPPING", "", true, false),
			Entry("STOPPING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STOPPING", "", true, false),
			Entry("STOPPING -> STOPPING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STOPPING", "", true, false),
			Entry("STOPPING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STOPPING", "", false, true),
			Entry("STOPPING -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STOPPING", "", true, false),
			Entry("STOPPING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STOPPING", "", true, false),

			// --- From STARTING ---
			Entry("STARTING -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STARTING", events.EventStarted, false, false),
			Entry("STARTING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STARTING", "", true, false),
			Entry("STARTING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STARTING", "", true, false),
			Entry("STARTING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STARTING", "", true, false),
			Entry("STARTING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STARTING", "", false, true),
			Entry("STARTING -> STARTING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STARTING", "", true, false),
			Entry("STARTING -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STARTING", "", true, false),
			Entry("STARTING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STARTING", "", true, false),

			// --- From DELETING ---
			Entry("DELETING -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "DELETING", events.EventStarted, false, false),
			Entry("DELETING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "DELETING", "", true, false),
			Entry("DELETING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "DELETING", "", true, false),
			Entry("DELETING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "DELETING", "", true, false),
			Entry("DELETING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "DELETING", "", false, true),
			Entry("DELETING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "DELETING", "", false, true),
			Entry("DELETING -> DELETING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "DELETING", "", true, false),
			Entry("DELETING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "DELETING", "", true, false),

			// --- From UNSPECIFIED ---
			Entry("UNSPECIFIED -> RUNNING -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "UNSPECIFIED", events.EventStarted, false, false),
			Entry("UNSPECIFIED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "UNSPECIFIED", "", true, false),
			Entry("UNSPECIFIED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "UNSPECIFIED", "", true, false),
			Entry("UNSPECIFIED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "UNSPECIFIED", "", true, false),
			Entry("UNSPECIFIED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "UNSPECIFIED", "", false, true),
			Entry("UNSPECIFIED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "UNSPECIFIED", "", false, true),
			Entry("UNSPECIFIED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "UNSPECIFIED", "", true, false),
			Entry("UNSPECIFIED -> UNSPECIFIED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "UNSPECIFIED", "", true, false),
		)

		It("returns ErrUnsupportedEvent for OBJECT_SIGNALED", func() {
			event := &privatev1.Event{
				Id:      "evt-signaled",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrUnsupportedEvent)).To(BeTrue())
		})

		It("returns error for unknown state (missing table entry)", func() {
			ci.Status.State = privatev1.ComputeInstanceState(9999)

			event := &privatev1.Event{
				Id:      "evt-unknown-state",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unexpected state transition"))
			Expect(errors.Is(err, events.ErrTransientState)).To(BeFalse())
		})

		It("maps RUNNING->STOPPED with duration to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED

			event := &privatev1.Event{
				Id:      "evt-stopped-with-duration",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			duration := 7200.0
			stateCtx := &events.StateContext{
				PreviousState:   "RUNNING",
				DurationSeconds: &duration,
			}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventSuspended))

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["duration_seconds"]).To(BeNumerically("==", 7200.0))
		})

		It("maps RUNNING->FAILED (prev=RUNNING) to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED

			event := &privatev1.Event{
				Id:      "evt-running-to-failed",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "RUNNING"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventSuspended))
		})

		It("includes previous_state and duration_seconds when state context provided", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED

			event := &privatev1.Event{
				Id:      "evt-with-ctx",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			duration := 3600.5
			stateCtx := &events.StateContext{
				PreviousState:   "RUNNING",
				DurationSeconds: &duration,
			}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["previous_state"]).To(Equal("RUNNING"))
			Expect(data["duration_seconds"]).To(BeNumerically("==", 3600.5))
		})

		It("maps OBJECT_CREATED to osac.resource.created.v1", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventCreated))
		})

		It("maps OBJECT_DELETED to osac.resource.deleted.v1", func() {
			ci.Metadata.DeletionTimestamp = timestamppb.Now()

			event := &privatev1.Event{
				Id:      "evt-3",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal(events.EventDeleted))
		})
	})

	Context("CloudEvent standard attributes", func() {
		It("sets specversion to 1.0", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.SpecVersion()).To(Equal("1.0"))
		})

		It("sets source to osac-metering", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Source()).To(Equal("osac-metering"))
		})

		It("preserves fulfillment event ID as CloudEvent ID for dedup", func() {
			event := &privatev1.Event{
				Id:      "fulfillment-evt-abc-123",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.ID()).To(Equal("fulfillment-evt-abc-123"))
		})

		It("sets time to a non-zero RFC3339 timestamp", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time().IsZero()).To(BeFalse())
		})
	})

	Context("extension attributes", func() {
		It("sets osacresourceid to the compute instance ID", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osacresourceid"]).To(Equal("ci-abc-123"))
		})

		It("sets osacresourcetype to compute_instance", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osacresourcetype"]).To(Equal("compute_instance"))
		})

		It("sets osactenant to the tenant from metadata", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osactenant"]).To(Equal("tenant-1"))
		})
	})

	Context("data payload field extraction", func() {
		It("extracts resource_id, tenant_id, project_id, and current_state", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data["resource_id"]).To(Equal("ci-abc-123"))
			Expect(data["resource_type"]).To(Equal("compute_instance"))
			Expect(data["tenant_id"]).To(Equal("tenant-1"))
			Expect(data["project_id"]).To(Equal("project-alpha"))
			Expect(data["current_state"]).To(Equal("RUNNING"))
			Expect(data["schema_version"]).To(Equal("v1"))
		})

		It("populates billing_dimensions from spec fields", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd, ok := data["billing_dimensions"].(map[string]any)
			Expect(ok).To(BeTrue(), "billing_dimensions should be a map")
			Expect(bd["instance_type"]).To(Equal("large-gpu"))
			Expect(bd["image_ref"]).To(Equal("rhel-10.2-x86_64"))
			// JSON numbers unmarshal as float64
			Expect(bd["boot_disk_size_gib"]).To(BeNumerically("==", 100))
		})

		It("sets previous_state and duration_seconds to null (Phase 1)", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("previous_state"))
			Expect(data["previous_state"]).To(BeNil())

			Expect(data).To(HaveKey("duration_seconds"))
			Expect(data["duration_seconds"]).To(BeNil())
		})

		It("populates template_id and catalog_item_id from spec", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data["template_id"]).To(Equal("tmpl-gpu"))
			Expect(data["catalog_item_id"]).To(Equal("catalog-item-1"))
		})

		It("includes transition_time as a non-empty RFC3339 string", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data["transition_time"]).NotTo(BeEmpty())
		})
	})

	Context("error handling", func() {
		It("returns an error for nil ComputeInstance payload", func() {
			event := &privatev1.Event{
				Id:   "evt-1",
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				// No payload set
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported"))
		})

		It("returns an error for unsupported payload type", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Hub{Hub: &privatev1.Hub{}},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported"))
		})
	})

	Context("optional field handling", func() {
		It("handles nil InstanceType gracefully", func() {
			ci.Spec.InstanceType = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).ToNot(HaveKey("instance_type"))
		})

		It("handles nil Image gracefully", func() {
			ci.Spec.Image = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).ToNot(HaveKey("image_ref"))
		})

		It("handles nil BootDisk gracefully", func() {
			ci.Spec.BootDisk = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).ToNot(HaveKey("boot_disk_size_gib"))
		})

		It("handles nil Spec gracefully for billing dimensions", func() {
			ci.Spec = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(BeEmpty())
		})

		It("rejects events with nil Metadata (no tenant_id)", func() {
			ci.Metadata = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no tenant_id")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("rejects events with empty tenant_id", func() {
			ci.Metadata = &privatev1.Metadata{Tenant: "", Project: "some-project"}

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no tenant_id")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("handles nil Status gracefully", func() {
			ci.Status = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data["current_state"]).To(Equal("UNSPECIFIED"))
		})

		It("sets project_id to null when project is empty string", func() {
			ci.Metadata.Project = ""

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("project_id"))
			Expect(data["project_id"]).To(BeNil())
		})

		It("sets template_id to null when template is nil", func() {
			ci.Spec.Template = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("template_id"))
			Expect(data["template_id"]).To(BeNil())
		})

		It("sets catalog_item_id to null when catalog_item is nil", func() {
			ci.Spec.CatalogItem = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("catalog_item_id"))
			Expect(data["catalog_item_id"]).To(BeNil())
		})
	})

	Context("transition time resolution", func() {
		It("uses creation_timestamp for CREATED events", func() {
			createTime := timestamppb.New(time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC))
			ci.Metadata.CreationTimestamp = createTime

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time()).To(Equal(createTime.AsTime()))
		})

		It("rejects CREATED events without creation_timestamp", func() {
			ci.Metadata.CreationTimestamp = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no timestamp for event type")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("uses state_transition_time for UPDATED events", func() {
			transTime := timestamppb.New(time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC))
			ci.Status.StateTransitionTime = transTime

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time()).To(Equal(transTime.AsTime()))
		})

		It("rejects UPDATED events without state_transition_time", func() {
			ci.Status.StateTransitionTime = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no timestamp for event type")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})

		It("uses deletion_timestamp for DELETED events", func() {
			deleteTime := timestamppb.New(time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
			ci.Metadata.DeletionTimestamp = deleteTime

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Time()).To(Equal(deleteTime.AsTime()))
		})

		It("rejects DELETED events without deletion_timestamp", func() {
			ci.Metadata.DeletionTimestamp = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no timestamp for event type")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})
	})
})

var _ = Describe("DimensionsEqual", func() {
	It("returns true for equal maps", func() {
		a := map[string]any{"k": "v", "n": int32(50)}
		b := map[string]any{"k": "v", "n": int32(50)}
		Expect(events.DimensionsEqual(a, b)).To(BeTrue())
	})

	It("returns false for different values", func() {
		a := map[string]any{"k": "v1"}
		b := map[string]any{"k": "v2"}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse())
	})

	It("returns false for different lengths", func() {
		a := map[string]any{"k": "v"}
		b := map[string]any{"k": "v", "k2": "v2"}
		Expect(events.DimensionsEqual(a, b)).To(BeFalse())
	})

	It("handles int32 vs float64 from JSON round-trip", func() {
		a := map[string]any{"boot_disk_size_gib": int32(50)}
		b := map[string]any{"boot_disk_size_gib": float64(50)}
		Expect(events.DimensionsEqual(a, b)).To(BeTrue())
	})

	It("handles large numbers without scientific notation mismatch", func() {
		a := map[string]any{"count": int32(1000000)}
		b := map[string]any{"count": float64(1000000)}
		Expect(events.DimensionsEqual(a, b)).To(BeTrue())
	})

	It("returns true for empty maps", func() {
		Expect(events.DimensionsEqual(map[string]any{}, map[string]any{})).To(BeTrue())
	})

	It("returns true for nil maps", func() {
		Expect(events.DimensionsEqual(nil, nil)).To(BeTrue())
	})
})

var _ = Describe("VMaaS transition table completeness", func() {
	stateProtoMap := map[string]privatev1.ComputeInstanceState{
		"RUNNING":     privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
		"STOPPED":     privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED,
		"PAUSED":      privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED,
		"FAILED":      privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED,
		"STOPPING":    privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING,
		"STARTING":    privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
		"DELETING":    privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING,
		"UNSPECIFIED": privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED,
	}

	It("covers every (from, to) state pair from all proto states plus empty initial", func() {
		fromStates := []string{"", "RUNNING", "STOPPED", "PAUSED", "FAILED", "STOPPING", "STARTING", "DELETING", "UNSPECIFIED"}
		toStates := []string{"RUNNING", "STOPPED", "PAUSED", "FAILED", "STOPPING", "STARTING", "DELETING", "UNSPECIFIED"}

		for _, from := range fromStates {
			for _, to := range toStates {
				ci := &privatev1.ComputeInstance{
					Id:       "ci-completeness",
					Metadata: &privatev1.Metadata{Tenant: "t", CreationTimestamp: timestamppb.Now()},
					Spec:     &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State:               stateProtoMap[to],
						StateTransitionTime: timestamppb.Now(),
					},
				}

				event := &privatev1.Event{
					Id:      "evt-completeness",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
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
