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
	return events.MapWatchEvent(event, mapper, stateCtx, mapper.BillingDimensionsMap())
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
				DiskImage: &privatev1.DiskImageReference{
					Name: "rhel-10.2-x86_64",
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
			func(currentState privatev1.ComputeInstanceState, previousState string, everBillable bool, expectedType string, expectSkip, expectTransient bool) {
				ci.Status.State = currentState
				event := &privatev1.Event{
					Id:      "evt-1",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
				}
				stateCtx := &events.StateContext{PreviousState: previousState, EverBillable: everBillable}
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

			// --- From "" (initial observation; existing==nil, so EverBillable is
			// necessarily false -- this row only fires on a missed-CREATE recovery,
			// never on the normal path, see the two dedicated integration tests in
			// consumer_test.go for what actually happens on a normal first boot) ---
			Entry("initial -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "", false, events.EventStarted, false, false),
			Entry("initial -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "", false, "", true, false),
			Entry("initial -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "", false, "", true, false),
			Entry("initial -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "", false, "", true, false),
			Entry("initial -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "", false, "", false, true),
			Entry("initial -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "", false, "", false, true),
			Entry("initial -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "", false, "", true, false),
			Entry("initial -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "", false, "", true, false),

			// --- From RUNNING (down-direction and no-ops -- EverBillable is
			// irrelevant here, suspended.v1 always, so passed as false throughout) ---
			Entry("RUNNING -> RUNNING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "RUNNING", false, "", true, false),
			Entry("RUNNING -> STOPPED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "RUNNING", false, events.EventSuspended, false, false),
			Entry("RUNNING -> PAUSED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "RUNNING", false, events.EventSuspended, false, false),
			Entry("RUNNING -> FAILED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "RUNNING", false, events.EventSuspended, false, false),
			Entry("RUNNING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "RUNNING", false, "", false, true),
			Entry("RUNNING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "RUNNING", false, "", false, true),
			Entry("RUNNING -> DELETING -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "RUNNING", false, events.EventSuspended, false, false),
			Entry("RUNNING -> UNSPECIFIED -> suspended.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "RUNNING", false, events.EventSuspended, false, false),

			// --- From STOPPED -> RUNNING: label depends on EverBillable, not on
			// previousState being "STOPPED" -- both variants proven ---
			Entry("STOPPED -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPED", true, events.EventResumed, false, false),
			Entry("STOPPED -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPED", false, events.EventStarted, false, false),
			Entry("STOPPED -> STOPPED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STOPPED", false, "", true, false),
			Entry("STOPPED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STOPPED", false, "", true, false),
			Entry("STOPPED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STOPPED", false, "", true, false),
			Entry("STOPPED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STOPPED", false, "", false, true),
			Entry("STOPPED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STOPPED", false, "", false, true),
			Entry("STOPPED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STOPPED", false, "", true, false),
			Entry("STOPPED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STOPPED", false, "", true, false),

			// --- From PAUSED -> RUNNING: both variants ---
			Entry("PAUSED -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "PAUSED", true, events.EventResumed, false, false),
			Entry("PAUSED -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "PAUSED", false, events.EventStarted, false, false),
			Entry("PAUSED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "PAUSED", false, "", true, false),
			Entry("PAUSED -> PAUSED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "PAUSED", false, "", true, false),
			Entry("PAUSED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "PAUSED", false, "", true, false),
			Entry("PAUSED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "PAUSED", false, "", false, true),
			Entry("PAUSED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "PAUSED", false, "", false, true),
			Entry("PAUSED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "PAUSED", false, "", true, false),
			Entry("PAUSED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "PAUSED", false, "", true, false),

			// --- From FAILED -> RUNNING: both variants (e.g. failed mid-provisioning,
			// never billed, then succeeds -> started.v1, not resumed.v1) ---
			Entry("FAILED -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "FAILED", true, events.EventResumed, false, false),
			Entry("FAILED -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "FAILED", false, events.EventStarted, false, false),
			Entry("FAILED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "FAILED", false, "", true, false),
			Entry("FAILED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "FAILED", false, "", true, false),
			Entry("FAILED -> FAILED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "FAILED", false, "", true, false),
			Entry("FAILED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "FAILED", false, "", false, true),
			Entry("FAILED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "FAILED", false, "", false, true),
			Entry("FAILED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "FAILED", false, "", true, false),
			Entry("FAILED -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "FAILED", false, "", true, false),

			// --- From STOPPING -> RUNNING: both variants ---
			Entry("STOPPING -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPING", true, events.EventResumed, false, false),
			Entry("STOPPING -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STOPPING", false, events.EventStarted, false, false),
			Entry("STOPPING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STOPPING", false, "", true, false),
			Entry("STOPPING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STOPPING", false, "", true, false),
			Entry("STOPPING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STOPPING", false, "", true, false),
			Entry("STOPPING -> STOPPING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STOPPING", false, "", true, false),
			Entry("STOPPING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STOPPING", false, "", false, true),
			Entry("STOPPING -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STOPPING", false, "", true, false),
			Entry("STOPPING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STOPPING", false, "", true, false),

			// --- From STARTING -> RUNNING: the main real-world case. A brand-new
			// resource's first boot lands here (never billable before -> started.v1);
			// a restart cycling back through STARTING lands here too (billable
			// before -> resumed.v1). Same previousState string, different label,
			// exactly the distinction the old table couldn't make. ---
			Entry("STARTING -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STARTING", true, events.EventResumed, false, false),
			Entry("STARTING -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "STARTING", false, events.EventStarted, false, false),
			Entry("STARTING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "STARTING", false, "", true, false),
			Entry("STARTING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "STARTING", false, "", true, false),
			Entry("STARTING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "STARTING", false, "", true, false),
			Entry("STARTING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "STARTING", false, "", false, true),
			Entry("STARTING -> STARTING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "STARTING", false, "", true, false),
			Entry("STARTING -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "STARTING", false, "", true, false),
			Entry("STARTING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "STARTING", false, "", true, false),

			// --- From DELETING -> RUNNING: both variants ---
			Entry("DELETING -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "DELETING", true, events.EventResumed, false, false),
			Entry("DELETING -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "DELETING", false, events.EventStarted, false, false),
			Entry("DELETING -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "DELETING", false, "", true, false),
			Entry("DELETING -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "DELETING", false, "", true, false),
			Entry("DELETING -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "DELETING", false, "", true, false),
			Entry("DELETING -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "DELETING", false, "", false, true),
			Entry("DELETING -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "DELETING", false, "", false, true),
			Entry("DELETING -> DELETING -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "DELETING", false, "", true, false),
			Entry("DELETING -> UNSPECIFIED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "DELETING", false, "", true, false),

			// --- From UNSPECIFIED -> RUNNING: the other main real-world case (a
			// fresh resource whose CREATE event is observed before the controller
			// populates status at all) ---
			Entry("UNSPECIFIED -> RUNNING, billable before -> resumed.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "UNSPECIFIED", true, events.EventResumed, false, false),
			Entry("UNSPECIFIED -> RUNNING, never billable before -> started.v1",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "UNSPECIFIED", false, events.EventStarted, false, false),
			Entry("UNSPECIFIED -> STOPPED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED, "UNSPECIFIED", false, "", true, false),
			Entry("UNSPECIFIED -> PAUSED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED, "UNSPECIFIED", false, "", true, false),
			Entry("UNSPECIFIED -> FAILED -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED, "UNSPECIFIED", false, "", true, false),
			Entry("UNSPECIFIED -> STOPPING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING, "UNSPECIFIED", false, "", false, true),
			Entry("UNSPECIFIED -> STARTING -> transient",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "UNSPECIFIED", false, "", false, true),
			Entry("UNSPECIFIED -> DELETING -> skip",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING, "UNSPECIFIED", false, "", true, false),
			Entry("UNSPECIFIED -> UNSPECIFIED -> skip (same-state)",
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED, "UNSPECIFIED", false, "", true, false),
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

		It("handles nil DiskImage gracefully", func() {
			ci.Spec.DiskImage = nil

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
