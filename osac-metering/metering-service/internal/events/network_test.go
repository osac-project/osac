package events_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("networking mappers", func() {
	It("uses resource creation time for ExternalIP create events", func() {
		creationTime := timestamppb.New(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
		eventTime := timestamppb.New(time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC))
		event := &privatev1.Event{
			Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			Timestamp: eventTime,
			Payload: &privatev1.Event_ExternalIp{ExternalIp: &privatev1.ExternalIP{
				Id:       "ip-1",
				Metadata: &privatev1.Metadata{CreationTimestamp: creationTime},
			}},
		}

		mapper := events.NewExternalIPMapper(event.GetExternalIp(), "deployment-1", map[string]string{})
		transitionTime, err := mapper.TransitionTime(event, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(transitionTime).To(Equal(creationTime.AsTime()))
	})

	It("uses the event time for ExternalIP delete events", func() {
		creationTime := timestamppb.New(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
		eventTime := timestamppb.New(time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC))
		event := &privatev1.Event{
			Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			Timestamp: eventTime,
			Payload: &privatev1.Event_ExternalIp{ExternalIp: &privatev1.ExternalIP{
				Id:       "ip-1",
				Metadata: &privatev1.Metadata{CreationTimestamp: creationTime},
			}},
		}

		mapper := events.NewExternalIPMapper(event.GetExternalIp(), "deployment-1", map[string]string{})
		transitionTime, err := mapper.TransitionTime(event, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(transitionTime).To(Equal(eventTime.AsTime()))
	})

	It("uses resource creation time for NATGateway create events", func() {
		creationTime := timestamppb.New(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
		eventTime := timestamppb.New(time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC))
		event := &privatev1.Event{
			Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			Timestamp: eventTime,
			Payload: &privatev1.Event_NatGateway{NatGateway: &privatev1.NATGateway{
				Id:       "nat-1",
				Metadata: &privatev1.Metadata{CreationTimestamp: creationTime},
			}},
		}

		mapper := events.NewNATGatewayMapper(event.GetNatGateway(), "deployment-1")
		transitionTime, err := mapper.TransitionTime(event, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(transitionTime).To(Equal(creationTime.AsTime()))
	})

	It("uses the event time for NATGateway delete events", func() {
		creationTime := timestamppb.New(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
		eventTime := timestamppb.New(time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC))
		event := &privatev1.Event{
			Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
			Timestamp: eventTime,
			Payload: &privatev1.Event_NatGateway{NatGateway: &privatev1.NATGateway{
				Id:       "nat-1",
				Metadata: &privatev1.Metadata{CreationTimestamp: creationTime},
			}},
		}

		mapper := events.NewNATGatewayMapper(event.GetNatGateway(), "deployment-1")
		transitionTime, err := mapper.TransitionTime(event, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(transitionTime).To(Equal(eventTime.AsTime()))
	})

	It("meters an unattached allocated ExternalIP with immutable pool dimensions", func() {
		ip := &privatev1.ExternalIP{
			Id:       "ip-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", Project: "project-1"},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
			Status:   &privatev1.ExternalIPStatus{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED},
		}

		dimensions, err := events.ExternalIPBillingDimensions(ip, "deployment-1", map[string]string{"pool-1": "ipv4"})

		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions).To(Equal(map[string]any{
			"deployment": "deployment-1",
			"pool":       "pool-1",
			"ip_family":  "ipv4",
			"attached":   false,
			"tenant_id":  "tenant-1",
			"project_id": "project-1",
		}))
		Expect(events.IsExternalIPBillableState(events.ExternalIPCurrentState(ip))).To(BeTrue())
	})

	It("uses settled attribution and rejects a pool cache miss", func() {
		ip := &privatev1.ExternalIP{
			Id:       "ip-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
			Status: &privatev1.ExternalIPStatus{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Attached: true,
				Attribution: &privatev1.ExternalIPAttribution{
					Target: &privatev1.ExternalIPAttribution_ComputeInstance{
						ComputeInstance: &privatev1.ComputeInstanceLocalReference{Id: "vm-1"},
					},
				},
			},
		}

		_, err := events.ExternalIPBillingDimensions(ip, "deployment-1", map[string]string{"pool-2": "ipv4"})

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("includes IPv6 pool family in ExternalIP dimensions", func() {
		ip := &privatev1.ExternalIP{
			Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
			Status:   &privatev1.ExternalIPStatus{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED},
		}

		dimensions, err := events.ExternalIPBillingDimensions(ip, "deployment-1", map[string]string{"pool-1": "ipv6"})

		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions["ip_family"]).To(Equal("ipv6"))
	})

	It("rejects an attached ExternalIP without an attachment transition time", func() {
		ip := &privatev1.ExternalIP{
			Id:       "ip-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
			Status: &privatev1.ExternalIPStatus{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Attached: true,
			},
		}

		_, err := events.ExternalIPTransitionTime(ip)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("uses allocation time when the first observed allocated snapshot is attached", func() {
		stateTime := timestamppb.New(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
		attachmentTime := timestamppb.New(time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC))
		event := &privatev1.Event{
			Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			Payload: &privatev1.Event_ExternalIp{ExternalIp: &privatev1.ExternalIP{
				Id:       "ip-1",
				Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
				Status: &privatev1.ExternalIPStatus{
					State:                    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
					Attached:                 true,
					StateTransitionTime:      stateTime,
					AttachmentTransitionTime: attachmentTime,
				},
			}},
		}

		mapper := events.NewExternalIPMapper(event.GetExternalIp(), "deployment-1", map[string]string{"pool-1": "ipv4"})
		transitionTime, err := mapper.TransitionTime(event, events.ExternalIPStatePending)

		Expect(err).NotTo(HaveOccurred())
		Expect(transitionTime).To(Equal(stateTime.AsTime()))
	})

	It("requires a tenant dimension for ExternalIP usage", func() {
		ip := &privatev1.ExternalIP{
			Metadata: &privatev1.Metadata{},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
		}
		mapper := events.NewExternalIPMapper(ip, "deployment-1", map[string]string{"pool-1": "ipv4"})

		_, err := mapper.BillingDimensionsMap()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("maps settled Cluster API attribution", func() {
		ip := &privatev1.ExternalIP{
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", Project: "project-1"},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
			Status: &privatev1.ExternalIPStatus{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Attached: true,
				Attribution: &privatev1.ExternalIPAttribution{
					Target:   &privatev1.ExternalIPAttribution_Cluster{Cluster: &privatev1.ClusterLocalReference{Id: "cluster-1"}},
					Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				},
			},
		}

		dimensions, err := events.ExternalIPBillingDimensions(ip, "deployment-1", map[string]string{"pool-1": "ipv4"})

		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions["attribution_type"]).To(Equal(events.ResourceTypeClusterOrder))
		Expect(dimensions["attribution_id"]).To(Equal("cluster-1"))
		Expect(dimensions["attribution_endpoint"]).To(Equal("api"))
	})

	It("maps settled BareMetalInstance attribution", func() {
		ip := &privatev1.ExternalIP{
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", Project: "project-1"},
			Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
			Status: &privatev1.ExternalIPStatus{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Attached: true,
				Attribution: &privatev1.ExternalIPAttribution{
					Target: &privatev1.ExternalIPAttribution_BaremetalInstance{
						BaremetalInstance: &privatev1.BareMetalInstanceLocalReference{Id: "baremetal-1"},
					},
				},
			},
		}

		dimensions, err := events.ExternalIPBillingDimensions(ip, "deployment-1", map[string]string{"pool-1": "ipv4"})

		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions["attribution_type"]).To(Equal("baremetal_instance"))
		Expect(dimensions["attribution_id"]).To(Equal("baremetal-1"))
	})

	It("treats a deletion timestamp as the ExternalIP billing boundary", func() {
		ip := &privatev1.ExternalIP{
			Id:       "ip-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", DeletionTimestamp: timestamppb.Now()},
			Status:   &privatev1.ExternalIPStatus{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED},
		}

		Expect(events.ExternalIPCurrentState(ip)).To(Equal(events.ExternalIPStateDeleting))
		Expect(events.IsExternalIPBillableState(events.ExternalIPCurrentState(ip))).To(BeFalse())
	})

	It("keeps NATGateway dimensions separate from ExternalIP attribution", func() {
		gateway := &privatev1.NATGateway{
			Id:       "nat-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1", Project: "project-1"},
			Spec: &privatev1.NATGatewaySpec{
				VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Id: "vn-1"},
				ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "ip-1"},
			},
			Status: &privatev1.NATGatewayStatus{State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY},
		}

		mapper := events.NewNATGatewayMapper(gateway, "deployment-1")
		dimensions, err := mapper.BillingDimensionsMap()
		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions).To(Equal(map[string]any{
			"deployment":      "deployment-1",
			"virtual_network": "vn-1",
			"external_ip":     "ip-1",
			"tenant_id":       "tenant-1",
			"project_id":      "project-1",
		}))
		Expect(dimensions).NotTo(HaveKey("attribution_type"))
	})

	It("allows an empty project dimension for the tenant default project", func() {
		gateway := &privatev1.NATGateway{
			Id:       "nat-1",
			Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
			Spec: &privatev1.NATGatewaySpec{
				VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Id: "vn-1"},
				ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "ip-1"},
			},
		}

		mapper := events.NewNATGatewayMapper(gateway, "deployment-1")
		dimensions, err := mapper.BillingDimensionsMap()
		Expect(err).NotTo(HaveOccurred())
		Expect(dimensions["project_id"]).To(Equal(""))
	})

	It("validates NATGateway dimensions through the mapper", func() {
		gateway := &privatev1.NATGateway{
			Id:       "nat-invalid-dimensions",
			Metadata: &privatev1.Metadata{},
			Spec: &privatev1.NATGatewaySpec{
				VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Id: "vn-1"},
				ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "ip-1"},
			},
		}
		mapper := events.NewNATGatewayMapper(gateway, "deployment-1")

		_, err := mapper.BillingDimensionsMap()
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, events.ErrDataQuality)).To(BeTrue())
	})

	It("maps billable networking transitions to started events", func() {
		transition := timestamppb.New(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
		event := &privatev1.Event{
			Id:   "external-ip-updated",
			Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			Payload: &privatev1.Event_ExternalIp{ExternalIp: &privatev1.ExternalIP{
				Id:       "ip-1",
				Metadata: &privatev1.Metadata{Tenant: "tenant-1", CreationTimestamp: transition, Version: 1},
				Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
				Status:   &privatev1.ExternalIPStatus{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, StateTransitionTime: transition},
			}},
		}

		mapper := events.NewExternalIPMapper(event.GetExternalIp(), "deployment-1", map[string]string{"pool-1": "ipv4"})
		dimensions, err := mapper.BillingDimensionsMap()
		Expect(err).NotTo(HaveOccurred())
		cloudEvent, err := events.MapWatchEvent(event, mapper, &events.StateContext{PreviousState: "PENDING"}, dimensions)
		Expect(err).NotTo(HaveOccurred())
		Expect(cloudEvent.Type()).To(Equal(events.EventStarted))
		Expect(cloudEvent.Extensions()["osacresourcetype"]).To(Equal(events.ResourceTypeExternalIP))
	})
})
