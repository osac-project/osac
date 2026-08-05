package events_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
)

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
				Template:     "tmpl-gpu",
				CatalogItem:  "catalog-item-1",
				InstanceType: &instanceType,
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.suspended.v1"))
		})

		It("maps OBJECT_UPDATED with STARTING state to osac.resource.updated.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.updated.v1"))
		})

		It("maps OBJECT_UPDATED with STOPPING state to osac.resource.updated.v1", func() {
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING

			event := &privatev1.Event{
				Id:      "evt-2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Type()).To(Equal("osac.resource.updated.v1"))
		})

		It("maps OBJECT_DELETED to osac.resource.deleted.v1", func() {
			ci.Metadata.DeletionTimestamp = timestamppb.Now()

			event := &privatev1.Event{
				Id:      "evt-3",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.SpecVersion()).To(Equal("1.0"))
		})

		It("sets source to osac-metering", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Source()).To(Equal("osac-metering"))
		})

		It("preserves fulfillment event ID as CloudEvent ID for dedup", func() {
			event := &privatev1.Event{
				Id:      "fulfillment-evt-abc-123",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.ID()).To(Equal("fulfillment-evt-abc-123"))
		})

		It("sets time to a non-zero RFC3339 timestamp", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osacresourceid"]).To(Equal("ci-abc-123"))
		})

		It("sets osacresourcetype to compute_instance", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(ce.Extensions()["osacresourcetype"]).To(Equal("compute_instance"))
		})

		It("sets osactenant to the tenant from metadata", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			_, err := events.MapWatchEvent(event)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported"))
		})

		It("returns an error for non-ComputeInstance payload", func() {
			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: &privatev1.Cluster{}},
			}

			_, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(HaveKey("instance_type"))
			Expect(bd["instance_type"]).To(BeNil())
		})

		It("handles nil Image gracefully", func() {
			ci.Spec.Image = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(HaveKey("image_ref"))
			Expect(bd["image_ref"]).To(BeNil())
		})

		It("handles nil BootDisk gracefully", func() {
			ci.Spec.BootDisk = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(HaveKey("boot_disk_size_gib"))
			Expect(bd["boot_disk_size_gib"]).To(BeNil())
		})

		It("handles nil Spec gracefully for billing dimensions", func() {
			ci.Spec = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["instance_type"]).To(BeNil())
			Expect(bd["image_ref"]).To(BeNil())
			Expect(bd["boot_disk_size_gib"]).To(BeNil())
		})

		It("rejects events with nil Metadata (no tenant_id)", func() {
			ci.Metadata = nil

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			_, err := events.MapWatchEvent(event)
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

			_, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("project_id"))
			Expect(data["project_id"]).To(BeNil())
		})

		It("sets template_id to null when template is empty string", func() {
			ci.Spec.Template = ""

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
			Expect(err).NotTo(HaveOccurred())

			var data map[string]any
			Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())

			Expect(data).To(HaveKey("template_id"))
			Expect(data["template_id"]).To(BeNil())
		})

		It("sets catalog_item_id to null when catalog_item is empty string", func() {
			ci.Spec.CatalogItem = ""

			event := &privatev1.Event{
				Id:      "evt-1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			ce, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			_, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			_, err := events.MapWatchEvent(event)
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

			ce, err := events.MapWatchEvent(event)
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

			_, err := events.MapWatchEvent(event)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("no deletion_timestamp")))
			Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
		})
	})
})
