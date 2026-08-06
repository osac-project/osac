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

	Context("event type mapping", func() {
		It("maps OBJECT_CREATED to osac.resource.created.v1", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.created.v1"))
		})

		It("maps OBJECT_UPDATED with RUNNING state to osac.resource.started.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.started.v1"))
		})

		It("maps OBJECT_UPDATED with STOPPED state to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
		})

		It("maps OBJECT_UPDATED with PAUSED state to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
		})

		It("maps OBJECT_UPDATED with FAILED state to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
		})

		It("returns ErrTransientState for STOPPING state", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrTransientState)).To(BeTrue())
		})

		It("returns ErrTransientState for STARTING state", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING

			event := &privatev1.Event{
				Id:      "evt-starting",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, events.ErrTransientState)).To(BeTrue())
		})

		It("maps DELETING to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING

			event := &privatev1.Event{
				Id:      "evt-deleting",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "RUNNING"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
		})

		It("maps STOPPED→RUNNING to osac.resource.resumed.v1 with state context", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-resumed-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "STOPPED"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.resumed.v1"))
		})

		It("maps PAUSED→RUNNING to osac.resource.resumed.v1 with state context", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-resumed-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "PAUSED"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.resumed.v1"))
		})

		It("maps STARTING→RUNNING to osac.resource.started.v1 with state context", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-started",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "STARTING"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.started.v1"))
		})

		It("maps FAILED→RUNNING to osac.resource.started.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-failed-to-running",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "FAILED"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.started.v1"))
		})

		It("maps RUNNING→RUNNING (prev=RUNNING) to osac.resource.started.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			event := &privatev1.Event{
				Id:      "evt-running-to-running",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "RUNNING"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.started.v1"))
		})

		It("maps UNSPECIFIED to osac.resource.updated.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED

			event := &privatev1.Event{
				Id:      "evt-unspecified",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.updated.v1"))
		})

		It("returns error for unknown state (default branch)", func() {
			ci.Status.State = privatev1.ComputeInstanceState(9999)

			event := &privatev1.Event{
				Id:      "evt-unknown-state",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := mapEvent(event, &events.StateContext{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unexpected compute instance state transition"))
			Expect(errors.Is(err, events.ErrTransientState)).To(BeFalse())
		})

		It("maps RUNNING→STOPPED with duration to osac.resource.suspended.v1", func() {
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
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
			Expect(data["duration_seconds"]).To(BeNumerically("==", 7200.0))
		})

		It("maps RUNNING→FAILED (prev=RUNNING) to osac.resource.suspended.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED

			event := &privatev1.Event{
				Id:      "evt-running-to-failed",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stateCtx := &events.StateContext{PreviousState: "RUNNING"}
			ce, err := mapEvent(event, stateCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
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

		It("maps OBJECT_DELETED to osac.resource.deleted.v1", func() {
			ci.Metadata.DeletionTimestamp = timestamppb.Now()

			event := &privatev1.Event{
				Id:      "evt-3",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := mapEvent(event, &events.StateContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.deleted.v1"))
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

		It("returns an error for non-ComputeInstance payload", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: &privatev1.Cluster{}},
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
			Expect(err).To(MatchError(ContainSubstring("no creation_timestamp")))
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
			Expect(err).To(MatchError(ContainSubstring("no state_transition_time")))
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
			Expect(err).To(MatchError(ContainSubstring("no deletion_timestamp")))
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
