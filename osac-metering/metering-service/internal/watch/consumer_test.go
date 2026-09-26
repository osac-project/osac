package watch_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/projection"
	"github.com/osac-project/osac-metering/internal/watch"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type mockWatchStream struct {
	grpc.ClientStream
	responses []*privatev1.EventsWatchResponse
	idx       int
	finalErr  error
	ctx       context.Context
}

func (m *mockWatchStream) Recv() (*privatev1.EventsWatchResponse, error) {
	if m.idx < len(m.responses) {
		resp := m.responses[m.idx]
		m.idx++
		return resp, nil
	}
	if m.ctx != nil {
		<-m.ctx.Done()
		return nil, m.ctx.Err()
	}
	if m.finalErr != nil {
		return nil, m.finalErr
	}
	return nil, io.EOF
}

type mockStreamResult struct {
	stream *mockWatchStream
	err    error
}

type mockEventsClient struct {
	mu      sync.Mutex
	results []mockStreamResult
	callIdx int
	calls   []*privatev1.EventsWatchRequest
}

type mockExternalIPPoolClient struct{}

func (mockExternalIPPoolClient) Get(context.Context, *privatev1.ExternalIPPoolsGetRequest, ...grpc.CallOption) (*privatev1.ExternalIPPoolsGetResponse, error) {
	return nil, errors.New("unexpected ExternalIP pool lookup")
}

func (m *mockEventsClient) Watch(ctx context.Context, req *privatev1.EventsWatchRequest, _ ...grpc.CallOption) (privatev1.Events_WatchClient, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	if m.callIdx >= len(m.results) {
		m.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result := m.results[m.callIdx]
	m.callIdx++
	m.mu.Unlock()
	if result.err != nil {
		return nil, result.err
	}
	return result.stream, nil
}

func (m *mockEventsClient) watchCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

type mockPublisher struct {
	mu         sync.Mutex
	published  []cloudevents.Event
	err        error
	failUntil  int
	callCount  int
	cancelFunc context.CancelFunc
}

func (m *mockPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.callCount <= m.failUntil {
		return m.err
	}
	m.published = append(m.published, event)
	if m.cancelFunc != nil && len(m.published) >= cap(m.published) {
		m.cancelFunc()
	}
	return nil
}

type mockStore struct {
	mu         sync.Mutex
	states     map[string]projection.ResourceState
	upsertErrs map[string]error
}

func newMockStore() *mockStore {
	return &mockStore{
		states:     map[string]projection.ResourceState{},
		upsertErrs: map[string]error{},
	}
}

func (s *mockStore) Get(_ context.Context, resourceID string) (*projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[resourceID]
	if !ok {
		return nil, nil
	}
	return &state, nil
}

func (s *mockStore) Upsert(_ context.Context, state projection.ResourceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.upsertErrs[state.ResourceID]; ok {
		return err
	}
	if existing, ok := s.states[state.ResourceID]; ok {
		if existing.FulfillmentVersion > state.FulfillmentVersion {
			return projection.ErrStaleVersion
		}
	}
	s.states[state.ResourceID] = state
	return nil
}

func (s *mockStore) Delete(_ context.Context, resourceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, resourceID)
	return nil
}

func (s *mockStore) ListBillable(_ context.Context) ([]projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []projection.ResourceState
	for _, state := range s.states {
		if state.IsBillable {
			result = append(result, state)
		}
	}
	return result, nil
}

func (s *mockStore) ListAll(_ context.Context) ([]projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []projection.ResourceState
	for _, state := range s.states {
		result = append(result, state)
	}
	return result, nil
}

func (s *mockStore) UpdateLastHeartbeat(_ context.Context, _ []string, _ time.Time) error {
	return nil
}

func makeComputeInstance(id, tenant string) *privatev1.ComputeInstance {
	return &privatev1.ComputeInstance{
		Id: id,
		Metadata: &privatev1.Metadata{
			Tenant:            tenant,
			CreationTimestamp: timestamppb.Now(),
		},
		Status: &privatev1.ComputeInstanceStatus{
			State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			StateTransitionTime: timestamppb.Now(),
		},
	}
}

func makeBareMetalInstance(id, tenant string) *privatev1.BareMetalInstance {
	return &privatev1.BareMetalInstance{
		Id: id,
		Metadata: &privatev1.Metadata{
			Tenant:            tenant,
			Project:           "project-alpha",
			Version:           1,
			CreationTimestamp: timestamppb.Now(),
		},
		Spec: &privatev1.BareMetalInstanceSpec{
			CatalogItem: &privatev1.BareMetalInstanceCatalogItemReference{Name: "catalog-item-1"},
			InstanceType: &privatev1.BareMetalInstanceTypeLocalReference{
				Id:   "bmi-type-gpu-large",
				Name: "GPU large",
			},
		},
		Status: &privatev1.BareMetalInstanceStatus{
			State:               privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
			StateTransitionTime: timestamppb.Now(),
		},
	}
}

func makeEvent(id string, eventType privatev1.EventType) *privatev1.Event {
	return &privatev1.Event{
		Id:      id,
		Type:    eventType,
		Payload: &privatev1.Event_ComputeInstance{ComputeInstance: makeComputeInstance(id, "tenant-1")},
	}
}

func makeResponse(event *privatev1.Event) *privatev1.EventsWatchResponse {
	return &privatev1.EventsWatchResponse{Event: event}
}

var _ = Describe("Consumer", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *mockEventsClient
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		client = &mockEventsClient{}
	})

	AfterEach(func() {
		cancel()
	})

	newConsumer := func(pub *mockPublisher) *watch.Consumer {
		mapperFactory, err := watch.NewMapperFactory(mockExternalIPPoolClient{}, "deployment-1", map[string]string{})
		Expect(err).NotTo(HaveOccurred())
		c, err := watch.NewConsumer(client, pub, newMockStore(), logr.Discard(), mapperFactory)
		Expect(err).NotTo(HaveOccurred())
		c.InitialDelay = time.Millisecond
		c.MaxDelay = time.Millisecond
		return c
	}

	newConsumerWithStore := func(pub *mockPublisher, store *mockStore) *watch.Consumer {
		mapperFactory, err := watch.NewMapperFactory(mockExternalIPPoolClient{}, "deployment-1", map[string]string{})
		Expect(err).NotTo(HaveOccurred())
		c, err := watch.NewConsumer(client, pub, store, logr.Discard(), mapperFactory)
		Expect(err).NotTo(HaveOccurred())
		c.InitialDelay = time.Millisecond
		c.MaxDelay = time.Millisecond
		return c
	}

	Describe("Run", func() {
		It("meters a block Volume from creation through availability", func() {
			creationTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			availableTime := creationTime.Add(time.Minute)
			volume := func(state privatev1.VolumeState, version int32, transition time.Time, vendorID string) *privatev1.Volume {
				return &privatev1.Volume{
					Id: "volume-1",
					Metadata: &privatev1.Metadata{
						Tenant:            "tenant-1",
						Project:           "project-1",
						Version:           version,
						CreationTimestamp: timestamppb.New(creationTime),
					},
					Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
					Status: &privatev1.VolumeStatus{
						State:          state,
						VendorVolumeId: vendorID,
						Protocol:       privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
						ProvisionedSizeGib: func() int64 {
							if state == privatev1.VolumeState_VOLUME_STATE_AVAILABLE {
								return 100
							}
							return 0
						}(),
						StateTransitionTime: timestamppb.New(transition),
					},
				}
			}
			creating := volume(privatev1.VolumeState_VOLUME_STATE_CREATING, 1, creationTime, "")
			available := volume(privatev1.VolumeState_VOLUME_STATE_AVAILABLE, 2, availableTime, "vendor-1")

			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "volume-created",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
						Payload: &privatev1.Event_Volume{Volume: creating},
					}),
					makeResponse(&privatev1.Event{
						Id:      "volume-available",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_Volume{Volume: available},
					}),
				},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}
			consumer := newConsumer(pub)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted))
			Expect(pub.published[1].Time()).To(Equal(availableTime))
		})

		It("publishes a same-version deletion boundary", func() {
			deletionTime := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
			volume := &privatev1.Volume{
				Id: "volume-delete",
				Metadata: &privatev1.Metadata{
					Tenant:            "tenant-1",
					Project:           "project-1",
					Version:           7,
					CreationTimestamp: timestamppb.New(deletionTime.Add(-time.Hour)),
					DeletionTimestamp: timestamppb.New(deletionTime),
				},
				Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
				Status: &privatev1.VolumeStatus{
					State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
					VendorVolumeId:      "vendor-1",
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					ProvisionedSizeGib:  100,
					StateTransitionTime: timestamppb.New(deletionTime.Add(-time.Hour)),
				},
			}
			store := newMockStore()
			billableSince := deletionTime.Add(-30 * time.Minute)
			store.states[volume.GetId()] = projection.ResourceState{
				ResourceID:         volume.GetId(),
				ResourceType:       events.ResourceTypeVolume,
				TenantID:           "tenant-1",
				ProjectID:          "project-1",
				CurrentState:       events.VolumeStateAvailable,
				IsBillable:         true,
				BillableSince:      &billableSince,
				FulfillmentVersion: 7,
				BillingDimensions: map[string]any{
					"volume_id": volume.GetId(), "tenant_id": "tenant-1", "project_id": "project-1",
					"storage_tier": "gold", "size_gib": int64(100),
				},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
				makeResponse(&privatev1.Event{
					Id:        "volume-delete",
					Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Timestamp: timestamppb.New(deletionTime),
					Payload:   &privatev1.Event_Volume{Volume: volume},
				}),
			}}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventSuspended))
			Expect(pub.published[0].Time()).To(Equal(deletionTime))
		})

		It("meters expansion from committed capacity at the feedback event time", func() {
			creationTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			availableTime := creationTime.Add(time.Minute)
			capacityTime := availableTime.Add(time.Minute)
			volume := func(size, committed int64, version int32) *privatev1.Volume {
				return &privatev1.Volume{
					Id: "volume-resize",
					Metadata: &privatev1.Metadata{
						Tenant:            "tenant-1",
						Project:           "project-1",
						Version:           version,
						CreationTimestamp: timestamppb.New(creationTime),
					},
					Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: size},
					Status: &privatev1.VolumeStatus{
						State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
						VendorVolumeId:      "vendor-1",
						Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
						ProvisionedSizeGib:  committed,
						StateTransitionTime: timestamppb.New(availableTime),
					},
				}
			}
			creating := &privatev1.Volume{
				Id: "volume-resize",
				Metadata: &privatev1.Metadata{
					Tenant:            "tenant-1",
					Project:           "project-1",
					Version:           1,
					CreationTimestamp: timestamppb.New(creationTime),
				},
				Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
				Status: &privatev1.VolumeStatus{
					State:               privatev1.VolumeState_VOLUME_STATE_CREATING,
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					StateTransitionTime: timestamppb.New(creationTime),
				},
			}
			available := volume(100, 100, 2)
			requested := volume(200, 100, 3)
			committed := volume(200, 200, 4)

			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{Id: "resize-created", Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, Payload: &privatev1.Event_Volume{Volume: creating}}),
					makeResponse(&privatev1.Event{Id: "resize-available", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Payload: &privatev1.Event_Volume{Volume: available}}),
					makeResponse(&privatev1.Event{Id: "resize-requested", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Timestamp: timestamppb.New(availableTime), Payload: &privatev1.Event_Volume{Volume: requested}}),
					makeResponse(&privatev1.Event{Id: "resize-committed", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Timestamp: timestamppb.New(capacityTime), Payload: &privatev1.Event_Volume{Volume: committed}}),
				},
			}}}

			store := newMockStore()
			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			Expect(pub.published).To(HaveLen(3))
			Expect(pub.published[2].Type()).To(Equal(events.EventUpdated))
			Expect(pub.published[2].Time()).To(Equal(capacityTime))
			pub.mu.Unlock()

			projected, err := store.Get(ctx, "volume-resize")
			Expect(err).ToNot(HaveOccurred())
			Expect(projected.BillableSince).ToNot(BeNil())
			Expect(projected.BillableSince.Equal(capacityTime)).To(BeTrue())
			Expect(projected.BillingDimensions["size_gib"]).To(Equal(int64(200)))
		})

		It("fails fast on a same-state capacity change without its boundary timestamp", func() {
			availableTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			store := newMockStore()
			billableSince := availableTime.Add(-time.Hour)
			store.states["volume-resize-untimed"] = projection.ResourceState{
				ResourceID:         "volume-resize-untimed",
				ResourceType:       events.ResourceTypeVolume,
				TenantID:           "tenant-1",
				ProjectID:          "project-1",
				CurrentState:       events.VolumeStateAvailable,
				IsBillable:         true,
				BillableSince:      &billableSince,
				FulfillmentVersion: 1,
				TransitionTime:     availableTime,
				BillingDimensions: map[string]any{
					"volume_id": "volume-resize-untimed", "tenant_id": "tenant-1", "project_id": "project-1",
					"storage_tier": "gold", "size_gib": int64(100),
				},
			}

			resized := &privatev1.Volume{
				Id: "volume-resize-untimed",
				Metadata: &privatev1.Metadata{
					Tenant:            "tenant-1",
					Project:           "project-1",
					Version:           2,
					CreationTimestamp: timestamppb.New(availableTime.Add(-time.Hour)),
				},
				Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 200},
				Status: &privatev1.VolumeStatus{
					State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
					VendorVolumeId:      "vendor-1",
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					ProvisionedSizeGib:  200,
					StateTransitionTime: timestamppb.New(availableTime),
				},
			}
			unreachableEvent := makeEvent("evt-before-volume-boundary", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			goodEvent := makeEvent("evt-after-volume-boundary", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			client.results = []mockStreamResult{
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-volume-resize-no-boundary",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_Volume{Volume: resized},
					}),
					makeResponse(unreachableEvent),
				}}},
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
					makeResponse(goodEvent),
				}}},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(Equal(2))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal(goodEvent.GetId()))

			projected, err := store.Get(ctx, "volume-resize-untimed")
			Expect(err).ToNot(HaveOccurred())
			Expect(projected.FulfillmentVersion).To(Equal(int32(1)))
			Expect(projected.BillingDimensions["size_gib"]).To(Equal(int64(100)))
		})

		It("meters an ExternalIP through the resource mapper factory", func() {
			creationTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			allocatedTime := creationTime.Add(time.Minute)
			poolClient := &poolGetter{response: &privatev1.ExternalIPPoolsGetResponse{Object: &privatev1.ExternalIPPool{
				Id:   "pool-1",
				Spec: &privatev1.ExternalIPPoolSpec{IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4},
			}}}
			mapperFactory, err := watch.NewMapperFactory(poolClient, "deployment-1", map[string]string{})
			Expect(err).NotTo(HaveOccurred())

			externalIP := func(version int32, state privatev1.ExternalIPState, stateTime time.Time) *privatev1.ExternalIP {
				return &privatev1.ExternalIP{
					Id: "ip-1",
					Metadata: &privatev1.Metadata{
						Tenant:            "tenant-1",
						Project:           "project-1",
						Version:           version,
						CreationTimestamp: timestamppb.New(creationTime),
					},
					Spec: &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
					Status: &privatev1.ExternalIPStatus{
						State:               state,
						StateTransitionTime: timestamppb.New(stateTime),
					},
				}
			}
			pending := externalIP(1, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING, creationTime)
			allocated := externalIP(2, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, allocatedTime)

			client.results = []mockStreamResult{{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
				makeResponse(&privatev1.Event{Id: "ip-created", Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED, Payload: &privatev1.Event_ExternalIp{ExternalIp: pending}}),
				makeResponse(&privatev1.Event{Id: "ip-allocated", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Payload: &privatev1.Event_ExternalIp{ExternalIp: allocated}}),
			}}}}

			consumer, err := watch.NewConsumer(client, &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}, newMockStore(), logr.Discard(), mapperFactory)
			Expect(err).NotTo(HaveOccurred())
			Expect(consumer.Run(ctx)).To(Succeed())

			Expect(poolClient.calls).To(Equal(1))
		})

		It("meters a NATGateway lifecycle without a network provider", func() {
			creationTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			readyTime := creationTime.Add(time.Minute)
			deletionTime := creationTime.Add(2 * time.Hour)
			finalDeleteTime := deletionTime.Add(time.Minute)
			gateway := func(version int32, state privatev1.NATGatewayState, stateTime, deletedAt *timestamppb.Timestamp) *privatev1.NATGateway {
				metadata := &privatev1.Metadata{
					Tenant:            "tenant-1",
					Project:           "project-1",
					Version:           version,
					CreationTimestamp: timestamppb.New(creationTime),
					DeletionTimestamp: deletedAt,
				}
				return &privatev1.NATGateway{
					Id:       "nat-1",
					Metadata: metadata,
					Spec: &privatev1.NATGatewaySpec{
						VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Id: "vnet-1"},
						ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "ip-1"},
					},
					Status: &privatev1.NATGatewayStatus{
						State:               state,
						StateTransitionTime: stateTime,
					},
				}
			}
			pending := gateway(1, privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING, timestamppb.New(creationTime), nil)
			ready := gateway(2, privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY, timestamppb.New(readyTime), nil)
			deleting := gateway(3, privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY, timestamppb.New(readyTime), timestamppb.New(deletionTime))
			deleted := gateway(4, privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY, timestamppb.New(readyTime), timestamppb.New(deletionTime))

			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:        "nat-created",
						Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
						Timestamp: timestamppb.New(creationTime),
						Payload:   &privatev1.Event_NatGateway{NatGateway: pending},
					}),
					makeResponse(&privatev1.Event{
						Id:        "nat-ready",
						Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Timestamp: timestamppb.New(readyTime),
						Payload:   &privatev1.Event_NatGateway{NatGateway: ready},
					}),
					makeResponse(&privatev1.Event{
						Id:        "nat-deleting",
						Type:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Timestamp: timestamppb.New(deletionTime),
						Payload:   &privatev1.Event_NatGateway{NatGateway: deleting},
					}),
					makeResponse(&privatev1.Event{
						Id:        "nat-deleted",
						Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
						Timestamp: timestamppb.New(finalDeleteTime),
						Payload:   &privatev1.Event_NatGateway{NatGateway: deleted},
					}),
				},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 4), cancelFunc: cancel}
			consumer := newConsumer(pub)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(4))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted))
			Expect(pub.published[1].Time()).To(Equal(readyTime))
			Expect(pub.published[2].Type()).To(Equal(events.EventSuspended))
			Expect(pub.published[2].Time()).To(Equal(deletionTime))
			Expect(pub.published[3].Type()).To(Equal(events.EventDeleted))
			Expect(pub.published[3].Time()).To(Equal(finalDeleteTime))
		})

		It("maps and publishes events", func() {
			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("evt-1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
					makeResponse(makeEvent("evt-2", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED)),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))
		})

		It("publishes CloudEvents with correct type based on state", func() {
			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("evt-1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
		})

		It("stops gracefully on context cancellation", func() {
			blockingStream := &mockWatchStream{ctx: ctx}
			client.results = []mockStreamResult{{stream: blockingStream}}

			pub := &mockPublisher{}
			consumer := newConsumer(pub)

			done := make(chan error, 1)
			go func() {
				done <- consumer.Run(ctx)
			}()

			time.Sleep(10 * time.Millisecond)
			cancel()

			Eventually(done, time.Second).Should(Receive(BeNil()))
		})

		It("reconnects after stream error", func() {
			errorStream := &mockWatchStream{
				finalErr: errors.New("connection reset"),
			}
			successStream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("reconnect-evt", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			client.results = []mockStreamResult{
				{stream: errorStream},
				{stream: successStream},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
		})

		It("retries publish errors before reconnecting", func() {
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("evt-1", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("evt-2", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

			pub := &mockPublisher{
				err:        errors.New("kafka unavailable"),
				failUntil:  3,
				published:  make([]cloudevents.Event, 0, 1),
				cancelFunc: cancel,
			}
			consumer := newConsumer(pub)
			consumer.HandlerRetries = 3

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("fails fast on unmappable events", func() {
			badEvent := &privatev1.Event{
				Id:   "bad-evt",
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			}
			goodEvent := makeEvent("good-evt", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(badEvent),
				},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
			}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("good-evt"))
		})

		It("sets metering filter on watch request", func() {
			blockingStream := &mockWatchStream{ctx: ctx}
			client.results = []mockStreamResult{{stream: blockingStream}}

			pub := &mockPublisher{}
			consumer := newConsumer(pub)

			done := make(chan error, 1)
			go func() {
				done <- consumer.Run(ctx)
			}()

			Eventually(func() int {
				return client.watchCallCount()
			}, time.Second).Should(BeNumerically(">=", 1))

			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			client.mu.Lock()
			defer client.mu.Unlock()
			Expect(client.calls).ToNot(BeEmpty())
			Expect(client.calls[0].GetFilter()).To(Equal("has(event.compute_instance) || has(event.cluster) || has(event.external_ip) || has(event.nat_gateway) || has(event.volume) || has(event.bare_metal_instance)"))
		})

		It("fails fast on unknown payload type and reconnects", func() {
			unknownPayload := &privatev1.Event{
				Id:      "unknown-evt",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: &privatev1.Cluster{}},
			}
			goodEvent := makeEvent("good-evt", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(unknownPayload)},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
			}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("good-evt"))
		})

		It("fails fast on missing timestamp and reconnects", func() {
			ci := makeComputeInstance("no-ts", "tenant-1")
			ci.Metadata.CreationTimestamp = nil

			badEvent := &privatev1.Event{
				Id:      "no-ts",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}
			goodEvent := makeEvent("good-evt", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(badEvent)},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
			}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("fails fast on missing tenant_id and reconnects", func() {
			ci := makeComputeInstance("no-tenant", "")

			badEvent := &privatev1.Event{
				Id:      "no-tenant",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}
			goodEvent := makeEvent("good-evt", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(badEvent)},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
			}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("skips OBJECT_SIGNALED without killing the stream", func() {
			ciRunning := makeComputeInstance("vm-signaled", "tenant-1")
			ciRunning.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-signaled",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciRunning},
					}),
					makeResponse(makeEvent("evt-after", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Single stream — no reconnect
			Expect(client.watchCallCount()).To(Equal(1))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal("osac.resource.created.v1"))
		})

		It("skips an untimed no-op update and continues the Watch stream", func() {
			store := newMockStore()
			previousTransition := time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC)
			store.states["cluster-meta"] = projection.ResourceState{
				ResourceID:         "cluster-meta",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       events.ClusterStateUnspecified,
				FulfillmentVersion: 1,
				TransitionTime:     previousTransition,
				BillingDimensions:  map[string]any{},
			}

			clusterNoTimestamp := &privatev1.Cluster{
				Id: "cluster-meta",
				Metadata: &privatev1.Metadata{
					Tenant:            "tenant-1",
					Version:           2,
					CreationTimestamp: timestamppb.New(previousTransition.Add(-time.Hour)),
					DeletionTimestamp: timestamppb.New(previousTransition.Add(time.Minute)),
				},
				Status: &privatev1.ClusterStatus{
					State: privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED,
				},
			}

			goodEvent := makeEvent("evt-after-noop", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-meta-update",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_Cluster{Cluster: clusterNoTimestamp},
					}),
					makeResponse(goodEvent),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(Equal(1))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal(goodEvent.GetId()))

			projected, err := store.Get(ctx, "cluster-meta")
			Expect(err).ToNot(HaveOccurred())
			Expect(projected.FulfillmentVersion).To(Equal(int32(2)))
			Expect(projected.TransitionTime).To(Equal(previousTransition),
				"an untimed metadata update must not move the last lifecycle boundary")
		})

		It("fails fast on data quality error when state actually changed", func() {
			store := newMockStore()
			store.states["vm-dq"] = projection.ResourceState{
				ResourceID:   "vm-dq",
				ResourceType: "compute_instance",
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
			}

			ciStopped := makeComputeInstance("vm-dq", "tenant-1")
			ciStopped.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			ciStopped.Status.StateTransitionTime = nil

			goodEvent := makeEvent("evt-after", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			stream1 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-dq",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciStopped},
					}),
				},
			}
			stream2 := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
			}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Stream reconnected — real data quality issue, fail fast
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("fails fast on invalid Volume billing dimensions", func() {
			volume := &privatev1.Volume{
				Id:       "volume-invalid-dimensions",
				Metadata: &privatev1.Metadata{Tenant: ""},
				Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
				Status: &privatev1.VolumeStatus{
					State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
					Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					VendorVolumeId:      "vendor-1",
					ProvisionedSizeGib:  100,
					StateTransitionTime: timestamppb.Now(),
				},
			}
			stream1 := &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
				makeResponse(&privatev1.Event{
					Id:      "volume-invalid-dimensions",
					Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_Volume{Volume: volume},
				}),
			}}
			stream2 := &mockWatchStream{responses: []*privatev1.EventsWatchResponse{makeResponse(makeEvent("evt-after-volume-dq", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))}}
			client.results = []mockStreamResult{{stream: stream1}, {stream: stream2}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, newMockStore())

			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("skips same-state same-dimensions updates", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-1"] = projection.ResourceState{
				ResourceID:         "vm-1",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("vm-1", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED)),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()

			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("reconnects when advancing a skipped projection update fails", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-upsert-error"] = projection.ResourceState{
				ResourceID:         "vm-upsert-error",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
			}
			store.upsertErrs["vm-upsert-error"] = errors.New("database unavailable")

			failed := makeComputeInstance("vm-upsert-error", "tenant-1")
			failed.Metadata.Version = 2
			failed.Status.StateTransitionTime = timestamppb.New(now.Add(time.Second))
			client.results = []mockStreamResult{
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "vm-upsert-error-update",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: failed},
					}),
				}}},
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("after-reconnect", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				}}},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("after-reconnect"))
		})

		It("emits resumed.v1 for STOPPED to RUNNING transition", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-resume"] = projection.ResourceState{
				ResourceID:         "vm-resume",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				EverBillable:       true, // was RUNNING before this stop -- a genuine resume
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
				TransitionTime:     now,
			}

			ci := makeComputeInstance("vm-resume", "tenant-1")
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			ci.Metadata.Version = 2
			event := &privatev1.Event{
				Id:      "evt-resume",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventResumed))
		})

		It("deletes projection on OBJECT_DELETED and publishes event", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-del"] = projection.ResourceState{
				ResourceID:         "vm-del",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
			}

			ci := makeComputeInstance("vm-del", "tenant-1")
			ci.Metadata.Version = 2
			ci.Metadata.DeletionTimestamp = timestamppb.Now()
			event := &privatev1.Event{
				Id:        "vm-del",
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Timestamp: timestamppb.Now(),
				Payload:   &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventDeleted))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-del"))
		})

		It("does not publish a stale Watch snapshot after a newer snapshot advanced the projection", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-stale"] = projection.ResourceState{
				ResourceID:         "vm-stale",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
				TransitionTime:     now,
			}

			newerCI := makeComputeInstance("vm-stale", "tenant-1")
			newerCI.Metadata.Version = 10
			newerCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			newerEvent := &privatev1.Event{
				Id:      "evt-newer",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: newerCI},
			}

			staleCI := makeComputeInstance("vm-stale", "tenant-1")
			staleCI.Metadata.Version = 5
			staleCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			staleEvent := &privatev1.Event{
				Id:      "evt-stale",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: staleCI},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(newerEvent),
					makeResponse(staleEvent),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("evt-newer"))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-stale"].FulfillmentVersion).To(Equal(int32(10)))
			Expect(store.states["vm-stale"].CurrentState).To(Equal("RUNNING"))
		})

		It("does not publish a conflicting snapshot with the same fulfillment version", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-conflict"] = projection.ResourceState{
				ResourceID:         "vm-conflict",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 10,
				BillingDimensions:  map[string]any{},
				TransitionTime:     now,
			}

			ci := makeComputeInstance("vm-conflict", "tenant-1")
			ci.Metadata.Version = 10
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			event := &privatev1.Event{
				Id:      "evt-conflict",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-conflict"].CurrentState).To(Equal("RUNNING"))
		})

		It("publishes updated.v1 and updates projection on dimension change while billable (RUNNING->RUNNING)", func() {
			store := newMockStore()
			originalStart := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Microsecond)
			store.states["vm-resize"] = projection.ResourceState{
				ResourceID:         "vm-resize",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &originalStart,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
				TransitionTime:     originalStart,
			}

			newType := "m5.xlarge"
			ci := makeComputeInstance("vm-resize", "tenant-1")
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			ci.Metadata.Version = 2
			ci.Spec = &privatev1.ComputeInstanceSpec{InstanceType: &privatev1.InstanceTypeReference{Name: newType}}

			event := &privatev1.Event{
				Id:      "evt-resize",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			// RUNNING->RUNNING is Skip; dimension change now publishes
			// updated.v1 for VMaaS via BuildDimensionChangeEvents.
			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventUpdated))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["instance_type"]).To(Equal("m5.xlarge"))

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-resize"]
			Expect(updated.BillingDimensions["instance_type"]).To(Equal("m5.xlarge"))
			Expect(updated.BillableSince).ToNot(BeNil())
			Expect(updated.BillableSince.After(originalStart)).To(BeTrue())
		})

		It("publishes updated.v1 when VMaaS billing dimensions change while RUNNING", func() {
			store := newMockStore()
			originalStart := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Microsecond)
			store.states["vm-dim-change"] = projection.ResourceState{
				ResourceID:         "vm-dim-change",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &originalStart,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
				TransitionTime:     originalStart,
			}

			ci := makeComputeInstance("vm-dim-change", "tenant-1")
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			ci.Status.StateTransitionTime = timestamppb.New(originalStart)
			ci.Metadata.Version = 2
			ci.Spec = &privatev1.ComputeInstanceSpec{InstanceType: &privatev1.InstanceTypeReference{Name: "m5.xlarge"}}

			event := &privatev1.Event{
				Id:      "evt-dim-change",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(Equal(1))
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventUpdated))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["instance_type"]).To(Equal("m5.xlarge"))
			Expect(data["duration_seconds"]).ToNot(BeNil())

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-dim-change"]
			Expect(updated.BillingDimensions["instance_type"]).To(Equal("m5.xlarge"))
		})

		It("preserves billing context through RUNNING→STOPPING→STOPPED sequence", func() {
			store := newMockStore()
			billableStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			store.states["vm-stop-seq"] = projection.ResourceState{
				ResourceID:         "vm-stop-seq",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &billableStart,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
				TransitionTime:     billableStart,
			}

			// Two events: RUNNING→STOPPING, then STOPPING→STOPPED
			stoppingTime := billableStart.Add(30 * time.Minute)
			stoppedTime := billableStart.Add(1 * time.Hour)

			ciStopping := makeComputeInstance("vm-stop-seq", "tenant-1")
			ciStopping.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING
			ciStopping.Status.StateTransitionTime = timestamppb.New(stoppingTime)
			ciStopping.Metadata.Version = 2

			ciStopped := makeComputeInstance("vm-stop-seq", "tenant-1")
			ciStopped.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			ciStopped.Status.StateTransitionTime = timestamppb.New(stoppedTime)
			ciStopped.Metadata.Version = 3

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-stopping",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciStopping},
					}),
					makeResponse(&privatev1.Event{
						Id:      "evt-stopped",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciStopped},
					}),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()

			// STOPPING is transient — no CloudEvent published for it.
			// Only suspended.v1 for STOPPED should be published.
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventSuspended))

			// suspended.v1 should have duration_seconds = 3600 (1 hour from
			// BillableSince to STOPPED transition time), proving billing context
			// was preserved through STOPPING, not reset to STOPPING time.
			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["previous_state"]).To(Equal("RUNNING"))
			Expect(data["duration_seconds"]).To(BeNumerically("~", 3600.0, 0.1))

			// Projection should show STOPPED, non-billable
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-stop-seq"].CurrentState).To(Equal("STOPPED"))
			Expect(store.states["vm-stop-seq"].IsBillable).To(BeFalse())
		})

		It("upserts projection for first-seen resource", func() {
			store := newMockStore()
			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(makeEvent("vm-new", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("vm-new"))
			Expect(store.states["vm-new"].CurrentState).To(Equal("RUNNING"))
			Expect(store.states["vm-new"].IsBillable).To(BeTrue())
			Expect(store.states["vm-new"].BillableSince).ToNot(BeNil())
		})

		It("emits started.v1 then resumed.v1 across a real first-boot and resume cycle", func() {
			// Real lifecycle shape: CREATE observes the resource before it's
			// running (STARTING -- matching what the controller reports at
			// creation), then a separate UPDATE reports RUNNING. No pre-seeded
			// store state. makeComputeInstance/makeEvent hardcode RUNNING even
			// at CREATE, which hides this path -- build the events by hand here.
			//
			// Continues through a stop + restart so the same consumer/store
			// also proves the seam a first-boot-only test can't: that
			// buildProjectionState actually persists EverBillable=true after
			// the first activation, and a later reactivation reads that back
			// as resumed.v1 -- not just that a hand-seeded EverBillable:true
			// fixture produces resumed.v1 in isolation.
			store := newMockStore()
			baseTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			runningTime := baseTime.Add(time.Minute)
			stoppedTime := baseTime.Add(2 * time.Hour)
			restartedTime := baseTime.Add(3 * time.Hour)
			testCtx, testCancel := context.WithTimeout(ctx, time.Second)
			defer testCancel()

			createCI := makeComputeInstance("vm-fresh", "tenant-1")
			createCI.Metadata.CreationTimestamp = timestamppb.New(baseTime)
			createCI.Status.StateTransitionTime = timestamppb.New(baseTime)
			createCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING
			createEvent := &privatev1.Event{
				Id:      "evt-create",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: createCI},
			}

			runningCI := makeComputeInstance("vm-fresh", "tenant-1")
			runningCI.Metadata.CreationTimestamp = timestamppb.New(baseTime)
			runningCI.Status.StateTransitionTime = timestamppb.New(runningTime)
			runningCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			runningCI.Metadata.Version = 2
			runningEvent := &privatev1.Event{
				Id:      "evt-running",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: runningCI},
			}

			stoppedCI := makeComputeInstance("vm-fresh", "tenant-1")
			stoppedCI.Metadata.CreationTimestamp = timestamppb.New(baseTime)
			stoppedCI.Status.StateTransitionTime = timestamppb.New(stoppedTime)
			stoppedCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			stoppedCI.Metadata.Version = 3
			stoppedEvent := &privatev1.Event{
				Id:      "evt-stopped",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: stoppedCI},
			}

			restartedCI := makeComputeInstance("vm-fresh", "tenant-1")
			restartedCI.Metadata.CreationTimestamp = timestamppb.New(baseTime)
			restartedCI.Status.StateTransitionTime = timestamppb.New(restartedTime)
			restartedCI.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			restartedCI.Metadata.Version = 4
			restartedEvent := &privatev1.Event{
				Id:      "evt-restarted",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: restartedCI},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(createEvent),
					makeResponse(runningEvent),
					makeResponse(stoppedEvent),
					makeResponse(restartedEvent),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 4), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(testCtx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(4))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted),
				"first-ever activation of a brand-new resource must be started.v1, not resumed.v1")
			Expect(pub.published[2].Type()).To(Equal(events.EventSuspended))
			Expect(pub.published[3].Type()).To(Equal(events.EventResumed),
				"reactivation must be resumed.v1 once the consumer has recorded EverBillable itself")
		})
	})

	Describe("CaaS Cluster events", func() {
		makeCluster := func(id, tenant string, state privatev1.ClusterState, nodeSets map[string]*privatev1.ClusterNodeSet) *privatev1.Cluster {
			return &privatev1.Cluster{
				Id: id,
				Metadata: &privatev1.Metadata{
					Tenant:            tenant,
					Version:           2,
					CreationTimestamp: timestamppb.Now(),
				},
				Spec: &privatev1.ClusterSpec{
					Template: &privatev1.ClusterTemplateReference{Name: "ocp-ci-small"},
					Version:  &privatev1.ClusterVersionReference{Id: "4.17.0", Name: "4.17.0"},
					NodeSets: nodeSets,
				},
				Status: &privatev1.ClusterStatus{
					State:               state,
					StateTransitionTime: timestamppb.Now(),
				},
			}
		}

		defaultNodeSets := func() map[string]*privatev1.ClusterNodeSet {
			return map[string]*privatev1.ClusterNodeSet{
				"gpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "gpu-h100"}, Size: proto.Int32(2)},
				"cpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "cpu-only"}, Size: proto.Int32(3)},
			}
		}

		clusterBillingDims := func() map[string]any {
			return map[string]any{
				"cluster_template": "ocp-ci-small",
				"release_image":    "4.17.0",
				"components": []any{
					map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
					map[string]any{"node_set": "cpu-workers", "component": "worker", "host_type": "cpu-only", "node_count": int32(3)},
					map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
				},
			}
		}

		It("publishes exactly 1 event for cluster CREATED (not N+1)", func() {
			cl := makeCluster("cl-1", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-create",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			Expect(pub.published[0].Extensions()["osacresourcetype"]).To(Equal(events.ResourceTypeClusterOrder))
		})

		It("cluster created.v1 has flat billing_dimensions without components", func() {
			cl := makeCluster("cl-flat", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-flat-create",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(HaveKey("cluster_template"))
			Expect(bd).NotTo(HaveKey("components"))
		})

		It("cluster deleted.v1 has flat billing_dimensions without components", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-flat-del"] = projection.ResourceState{
				ResourceID:         "cl-flat-del",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "DELETING",
				IsBillable:         false,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				TransitionTime:     now,
			}

			cl := makeCluster("cl-flat-del", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_DELETING, defaultNodeSets())
			cl.Metadata.DeletionTimestamp = timestamppb.Now()
			event := &privatev1.Event{
				Id:        "evt-flat-del",
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Timestamp: timestamppb.Now(),
				Payload:   &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventDeleted))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd).To(HaveKey("cluster_template"))
			Expect(bd).NotTo(HaveKey("components"))
		})

		It("publishes N+1 started.v1 events for new cluster PROGRESSING", func() {
			cl := makeCluster("cl-start", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-start",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			// 3 events: 1 control_plane + 2 workers
			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(3))
			for _, e := range pub.published {
				Expect(e.Type()).To(Equal(events.EventStarted))
			}
		})

		It("emits started.v1 (not resumed.v1) for a brand-new cluster's real first-progress sequence", func() {
			// Companion to the test above, which unrealistically feeds a bare
			// UPDATE into an empty store (existing==nil gives PreviousState=""
			// for free). Real clusters get a CREATE first, same as ComputeInstance
			// -- CREATE seeds a real (non-empty) CurrentState before PROGRESSING
			// is ever observed, so PreviousState is never "" again. No pre-seeded
			// store state here; build both events by hand.
			store := newMockStore()

			createCluster := makeCluster("cl-fresh", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED, defaultNodeSets())
			createEvent := &privatev1.Event{
				Id:      "evt-create",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_Cluster{Cluster: createCluster},
			}

			progressingCluster := makeCluster("cl-fresh", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			progressingCluster.Metadata.Version = 3
			progressingEvent := &privatev1.Event{
				Id:      "evt-progressing",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: progressingCluster},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(createEvent),
					makeResponse(progressingEvent),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			// 1 created.v1 + 3 started.v1 (control_plane + 2 workers)
			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 4), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(4))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			for _, e := range pub.published[1:] {
				Expect(e.Type()).To(Equal(events.EventStarted),
					"first-ever activation of a brand-new cluster must be started.v1, not resumed.v1")
			}
		})

		It("each decomposed event has distinct per-component billing_dimensions", func() {
			cl := makeCluster("cl-decomp", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-decomp",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(3))

			components := map[string]bool{}
			for _, e := range pub.published {
				var data map[string]any
				Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
				bd := data["billing_dimensions"].(map[string]any)
				comp := bd["component"].(string) + ":" + bd["host_type"].(string)
				components[comp] = true
				Expect(bd).To(HaveKey("cluster_template"))
				Expect(bd).To(HaveKey("node_count"))
				Expect(bd).NotTo(HaveKey("components"))
			}
			Expect(components).To(HaveLen(3))
			Expect(components).To(HaveKey("control_plane:_control_plane"))
			Expect(components).To(HaveKey("worker:cpu-only"))
			Expect(components).To(HaveKey("worker:gpu-h100"))
		})

		It("each decomposed event has deterministic component-scoped ID", func() {
			cl := makeCluster("cl-ids", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_PROGRESSING, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-ids",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			ids := map[string]bool{}
			for _, e := range pub.published {
				Expect(e.ID()).To(ContainSubstring("evt-ids/"))
				ids[e.ID()] = true
			}
			Expect(ids).To(HaveLen(3))
		})

		It("skips publish on PROGRESSING→READY (both billable) but updates projection", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-ready"] = projection.ResourceState{
				ResourceID:         "cl-ready",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "PROGRESSING",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				TransitionTime:     now,
			}

			cl := makeCluster("cl-ready", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, defaultNodeSets())
			clEvent := &privatev1.Event{
				Id:      "evt-ready",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(clEvent)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()

			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["cl-ready"].CurrentState).To(Equal("READY"))
			Expect(store.states["cl-ready"].IsBillable).To(BeTrue())
			Expect(store.states["cl-ready"].BillableSince).To(Equal(&now))
		})

		It("publishes N+1 suspended.v1 on READY→FAILED", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-fail"] = projection.ResourceState{
				ResourceID:         "cl-fail",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				TransitionTime:     now,
			}

			cl := makeCluster("cl-fail", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_FAILED, defaultNodeSets())
			event := &privatev1.Event{
				Id:      "evt-fail",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(3))
			for _, e := range pub.published {
				Expect(e.Type()).To(Equal(events.EventSuspended))
			}

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["cl-fail"].IsBillable).To(BeFalse())
			Expect(store.states["cl-fail"].BillableSince).To(BeNil())
		})

		It("publishes updated.v1 only for changed component on scaling", func() {
			store := newMockStore()
			now := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Microsecond)
			store.states["cl-scale"] = projection.ResourceState{
				ResourceID:         "cl-scale",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				ComponentBillableSince: map[string]time.Time{
					"_control_plane": now,
					"cpu-workers":    now,
					"gpu-workers":    now,
				},
				TransitionTime: now,
			}

			// Scale gpu-h100 from 2 to 4, cpu-only stays at 3
			scaledNodeSets := map[string]*privatev1.ClusterNodeSet{
				"gpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "gpu-h100"}, Size: proto.Int32(4)},
				"cpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "cpu-only"}, Size: proto.Int32(3)},
			}
			cl := makeCluster("cl-scale", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, scaledNodeSets)
			event := &privatev1.Event{
				Id:      "evt-scale",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventUpdated))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["host_type"]).To(Equal("gpu-h100"))
			Expect(bd["node_count"]).To(BeNumerically("==", 4))
			Expect(data["duration_seconds"]).ToNot(BeNil())
		})

		It("sets duration_seconds=nil for newly-added component (no prior billing interval)", func() {
			store := newMockStore()
			billableStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			store.states["cl-add"] = projection.ResourceState{
				ResourceID:         "cl-add",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &billableStart,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"cluster_template": "ocp-ci-small",
					"release_image":    "4.17.0",
					"components": []any{
						map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
					},
				},
				TransitionTime: billableStart,
			}

			addedNodeSets := map[string]*privatev1.ClusterNodeSet{
				"tpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "tpu-v5"}, Size: proto.Int32(2)},
			}
			cl := makeCluster("cl-add", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, addedNodeSets)
			event := &privatev1.Event{
				Id:      "evt-add-component",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["node_set"]).To(Equal("tpu-workers"))
			Expect(data["duration_seconds"]).To(BeNil(),
				"newly-added component has no prior billing interval, duration must be nil")
		})

		It("sets duration for modified component and nil for new component in same scaling event", func() {
			store := newMockStore()
			billableStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			store.states["cl-mixed"] = projection.ResourceState{
				ResourceID:         "cl-mixed",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &billableStart,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"cluster_template": "ocp-ci-small",
					"release_image":    "4.17.0",
					"components": []any{
						map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": int32(1)},
						map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": int32(2)},
					},
				},
				ComponentBillableSince: map[string]time.Time{
					"_control_plane": billableStart,
					"gpu-workers":    billableStart,
				},
				TransitionTime: billableStart,
			}

			mixedNodeSets := map[string]*privatev1.ClusterNodeSet{
				"gpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "gpu-h100"}, Size: proto.Int32(4)},
				"tpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "tpu-v5"}, Size: proto.Int32(2)},
			}
			cl := makeCluster("cl-mixed", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, mixedNodeSets)
			event := &privatev1.Event{
				Id:      "evt-mixed",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))

			eventsByNodeSet := map[string]map[string]any{}
			for _, e := range pub.published {
				var data map[string]any
				Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
				bd := data["billing_dimensions"].(map[string]any)
				eventsByNodeSet[bd["node_set"].(string)] = data
			}

			Expect(eventsByNodeSet).To(HaveKey("gpu-workers"))
			Expect(eventsByNodeSet["gpu-workers"]["duration_seconds"]).ToNot(BeNil(),
				"modified component should have duration_seconds (closes prior billing interval)")

			Expect(eventsByNodeSet).To(HaveKey("tpu-workers"))
			Expect(eventsByNodeSet["tpu-workers"]["duration_seconds"]).To(BeNil(),
				"newly-added component should have nil duration_seconds (no prior interval)")
		})

		It("computes duration_seconds from each component's own last change, not the cluster-wide reset, across two sequential scaling events", func() {
			store := newMockStore()
			t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			t1 := t0.Add(1 * time.Hour)
			t2 := t0.Add(3 * time.Hour)

			store.states["cl-staggered"] = projection.ResourceState{
				ResourceID:         "cl-staggered",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &t0,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				ComponentBillableSince: map[string]time.Time{
					"_control_plane": t0,
					"cpu-workers":    t0,
					"gpu-workers":    t0,
				},
				TransitionTime: t0,
			}

			// T1: cpu-workers scales 3->5, gpu-workers stays at 2 (unchanged since T0).
			clAtT1 := makeCluster("cl-staggered", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, map[string]*privatev1.ClusterNodeSet{
				"cpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "cpu-only"}, Size: proto.Int32(5)},
				"gpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "gpu-h100"}, Size: proto.Int32(2)},
			})
			clAtT1.Status.StateTransitionTime = timestamppb.New(t1)
			eventT1 := &privatev1.Event{
				Id:      "evt-t1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: clAtT1},
			}

			// T2: gpu-workers scales 2->4, cpu-workers stays at 5 (unchanged since T1).
			clAtT2 := makeCluster("cl-staggered", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, map[string]*privatev1.ClusterNodeSet{
				"cpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "cpu-only"}, Size: proto.Int32(5)},
				"gpu-workers": {BaremetalInstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Name: "gpu-h100"}, Size: proto.Int32(4)},
			})
			clAtT2.Metadata.Version = 3
			clAtT2.Status.StateTransitionTime = timestamppb.New(t2)
			eventT2 := &privatev1.Event{
				Id:      "evt-t2",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: clAtT2},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(eventT1), makeResponse(eventT2)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))

			eventsByNodeSet := map[string]map[string]any{}
			for _, e := range pub.published {
				var data map[string]any
				Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
				bd := data["billing_dimensions"].(map[string]any)
				eventsByNodeSet[bd["node_set"].(string)] = data
			}

			Expect(eventsByNodeSet).To(HaveKey("cpu-workers"))
			Expect(eventsByNodeSet["cpu-workers"]["duration_seconds"]).To(BeNumerically("~", t1.Sub(t0).Seconds(), 1),
				"cpu-workers' first-ever change should close a 1-hour interval since T0")

			Expect(eventsByNodeSet).To(HaveKey("gpu-workers"))
			Expect(eventsByNodeSet["gpu-workers"]["duration_seconds"]).To(BeNumerically("~", t2.Sub(t0).Seconds(), 1),
				"gpu-workers was unchanged since T0, so its closed interval must span T0->T2 (3 hours), "+
					"not T1->T2 (2 hours) from the cluster-wide reset caused by cpu-workers' unrelated change")
		})

		It("advances projection version on same-state-same-dims update with higher version", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-version"] = projection.ResourceState{
				ResourceID:         "vm-version",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{},
				TransitionTime:     now,
			}

			ci := makeComputeInstance("vm-version", "tenant-1")
			ci.Metadata.Version = 10
			ci.Status.StateTransitionTime = timestamppb.New(now)
			event := &privatev1.Event{
				Id:      "evt-version-bump",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "same state+dims should not publish")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-version"].FulfillmentVersion).To(Equal(int32(10)),
				"version should be advanced even when event is skipped")
		})

		It("rejects a higher-version state change with an older transition time", func() {
			store := newMockStore()
			storedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			store.states["vm-monotonic"] = projection.ResourceState{
				ResourceID:         "vm-monotonic",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &storedAt,
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{},
				TransitionTime:     storedAt,
			}

			ci := makeComputeInstance("vm-monotonic", "tenant-1")
			ci.Metadata.Version = 6
			ci.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
			ci.Status.StateTransitionTime = timestamppb.New(storedAt.Add(-time.Minute))
			event := &privatev1.Event{
				Id:      "evt-monotonic",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ci},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty())
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states["vm-monotonic"].CurrentState).To(Equal("RUNNING"))
			Expect(store.states["vm-monotonic"].FulfillmentVersion).To(Equal(int32(5)))
			Expect(store.states["vm-monotonic"].TransitionTime).To(Equal(storedAt))
			store.mu.Unlock()
		})

		It("rejects a higher-version BMaaS metadata update with an older transition time", func() {
			store := newMockStore()
			storedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			store.states["bmi-monotonic-metadata"] = projection.ResourceState{
				ResourceID:         "bmi-monotonic-metadata",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:         true,
				BillableSince:      &storedAt,
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
				TransitionTime:     storedAt,
			}

			bmi := makeBareMetalInstance("bmi-monotonic-metadata", "tenant-1")
			bmi.Metadata.Version = 6
			bmi.Status.StateTransitionTime = timestamppb.New(storedAt.Add(-time.Minute))
			event := &privatev1.Event{
				Id:      "evt-bmi-monotonic-metadata",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty())
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states["bmi-monotonic-metadata"].FulfillmentVersion).To(Equal(int32(5)))
			Expect(store.states["bmi-monotonic-metadata"].TransitionTime).To(Equal(storedAt))
			store.mu.Unlock()
		})

		It("rejects a higher-version BMaaS STOPPED transition with an older timestamp", func() {
			store := newMockStore()
			storedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			store.states["bmi-monotonic-stopped"] = projection.ResourceState{
				ResourceID:    "bmi-monotonic-stopped",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:    true,
				BillableSince: &storedAt,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation:  projection.MeterState{ActiveSince: &storedAt, FirstStartedAt: &storedAt},
					Consumption: projection.MeterState{ActiveSince: &storedAt, FirstStartedAt: &storedAt},
				},
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
				TransitionTime:     storedAt,
			}

			bmi := makeBareMetalInstance("bmi-monotonic-stopped", "tenant-1")
			bmi.Metadata.Version = 6
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED
			bmi.Status.StateTransitionTime = timestamppb.New(storedAt.Add(-time.Minute))
			event := &privatev1.Event{
				Id:      "evt-bmi-monotonic-stopped",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty(), "a stale STOPPED transition must not suspend consumption")
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states["bmi-monotonic-stopped"].CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_RUNNING"))
			Expect(store.states["bmi-monotonic-stopped"].FulfillmentVersion).To(Equal(int32(5)))
			Expect(store.states["bmi-monotonic-stopped"].TransitionTime).To(Equal(storedAt))
			store.mu.Unlock()
		})

		It("accepts a newer BMaaS STOPPED transition", func() {
			store := newMockStore()
			storedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			stoppedAt := storedAt.Add(time.Hour)
			store.states["bmi-monotonic-newer"] = projection.ResourceState{
				ResourceID:    "bmi-monotonic-newer",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:    true,
				BillableSince: &storedAt,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation:  projection.MeterState{ActiveSince: &storedAt, FirstStartedAt: &storedAt},
					Consumption: projection.MeterState{ActiveSince: &storedAt, FirstStartedAt: &storedAt},
				},
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
				TransitionTime:     storedAt,
			}

			bmi := makeBareMetalInstance("bmi-monotonic-newer", "tenant-1")
			bmi.Metadata.Version = 6
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED
			bmi.Status.StateTransitionTime = timestamppb.New(stoppedAt)
			event := &privatev1.Event{
				Id:      "evt-bmi-monotonic-newer",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(Equal(1))
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published[0].Type()).To(Equal(events.EventSuspended))
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states["bmi-monotonic-newer"].CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_STOPPED"))
			Expect(store.states["bmi-monotonic-newer"].FulfillmentVersion).To(Equal(int32(6)))
			Expect(store.states["bmi-monotonic-newer"].TransitionTime).To(Equal(stoppedAt))
			Expect(store.states["bmi-monotonic-newer"].BMaaSMeterState.Consumption.ActiveSince).To(BeNil())
			store.mu.Unlock()
		})

		It("rejects a higher-version BMaaS deletion with an older timestamp", func() {
			store := newMockStore()
			storedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			staleAt := storedAt.Add(-time.Minute)
			store.states["bmi-monotonic-delete"] = projection.ResourceState{
				ResourceID:         "bmi-monotonic-delete",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:         true,
				BillableSince:      &storedAt,
				FulfillmentVersion: 5,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
				TransitionTime:     storedAt,
			}

			bmi := makeBareMetalInstance("bmi-monotonic-delete", "tenant-1")
			bmi.Metadata.Version = 6
			bmi.Metadata.DeletionTimestamp = timestamppb.New(staleAt)
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING
			event := &privatev1.Event{
				Id:        "evt-bmi-monotonic-delete",
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Timestamp: timestamppb.New(staleAt),
				Payload:   &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty())
			pub.mu.Unlock()
			store.mu.Lock()
			_, exists := store.states["bmi-monotonic-delete"]
			Expect(exists).To(BeTrue(), "a stale deletion must not delete the projection")
			store.mu.Unlock()
		})

		It("publishes exactly 1 event for cluster DELETED (not N+1)", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-del"] = projection.ResourceState{
				ResourceID:         "cl-del",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "DELETING",
				IsBillable:         false,
				FulfillmentVersion: 1,
				BillingDimensions:  clusterBillingDims(),
				TransitionTime:     now,
			}

			cl := makeCluster("cl-del", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_DELETING, defaultNodeSets())
			cl.Metadata.DeletionTimestamp = timestamppb.Now()
			event := &privatev1.Event{
				Id:        "evt-del",
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Timestamp: timestamppb.Now(),
				Payload:   &privatev1.Event_Cluster{Cluster: cl},
			}

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal(events.EventDeleted))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("cl-del"))
		})

		It("publishes created.v1 and seeds a billable projection for a RUNNING BMaaS object", func() {
			bmi := makeBareMetalInstance("bmi-1", "tenant-1")
			event := &privatev1.Event{
				Id:      "evt-bmi-created",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			runCtx, runCancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer runCancel()
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       runCtx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3), cancelFunc: runCancel}
			store := newMockStore()
			consumer := newConsumerWithStore(pub, store)

			Expect(consumer.Run(runCtx)).To(Succeed())

			pub.mu.Lock()
			Expect(pub.published).To(HaveLen(3))
			Expect(pub.published[0].Type()).To(Equal(events.EventCreated))
			Expect(pub.published[0].Extensions()["osacresourcetype"]).To(Equal(events.ResourceTypeBareMetalInstance))
			Expect(pub.published[1].ID()).To(Equal("evt-bmi-created/allocation"))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted))
			Expect(pub.published[2].ID()).To(Equal("evt-bmi-created/consumption"))
			Expect(pub.published[2].Type()).To(Equal(events.EventStarted))

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["resource_id"]).To(Equal("bmi-1"))
			Expect(data["tenant_id"]).To(Equal("tenant-1"))
			Expect(data["project_id"]).To(Equal("project-alpha"))
			Expect(data["current_state"]).To(Equal("BARE_METAL_INSTANCE_STATE_RUNNING"))
			Expect(data["billing_dimensions"]).To(Equal(map[string]any{
				"bm_instance_type": "bmi-type-gpu-large",
				"catalog_item":     "catalog-item-1",
			}))
			pub.mu.Unlock()

			store.mu.Lock()
			defer store.mu.Unlock()
			state, ok := store.states["bmi-1"]
			Expect(ok).To(BeTrue())
			Expect(state.CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_RUNNING"))
			Expect(state.IsBillable).To(BeTrue())
			Expect(state.BillableSince).ToNot(BeNil())
		})

		It("fails fast on a BMaaS deletion with a missing event timestamp", func() {
			store := newMockStore()
			startedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store.states["bmi-delete-missing-timestamp"] = projection.ResourceState{
				ResourceID:    "bmi-delete-missing-timestamp",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_DELETING",
				IsBillable:    true,
				BillableSince: &startedAt,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &startedAt, FirstStartedAt: &startedAt,
					},
					Consumption: projection.MeterState{
						ActiveSince: &startedAt, FirstStartedAt: &startedAt,
					},
				},
				FulfillmentVersion: 4,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large"},
			}

			bmi := makeBareMetalInstance("bmi-delete-missing-timestamp", "tenant-1")
			bmi.Metadata.Version = 5
			bmi.Metadata.DeletionTimestamp = timestamppb.New(startedAt.Add(time.Minute))
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING
			badDelete := &privatev1.Event{
				Id:      "evt-bmi-delete-missing-timestamp",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			goodEvent := makeEvent("evt-vm-after-delete", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			client.results = []mockStreamResult{
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{makeResponse(badDelete)}}},
				{stream: &mockWatchStream{responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)}}},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("evt-vm-after-delete"))
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states).To(HaveKey("bmi-delete-missing-timestamp"))
			store.mu.Unlock()
		})

		It("skips BMaaS data quality errors without disrupting later Watch events", func() {
			bmi := makeBareMetalInstance("bmi-invalid", "tenant-1")
			bmi.Spec.InstanceType.Id = ""
			badEvent := &privatev1.Event{
				Id:      "evt-bmi-invalid",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			goodEvent := makeEvent("evt-vm-after-bmi", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
			client.results = []mockStreamResult{
				{stream: &mockWatchStream{
					responses: []*privatev1.EventsWatchResponse{makeResponse(badEvent)},
				}},
				{stream: &mockWatchStream{
					responses: []*privatev1.EventsWatchResponse{makeResponse(goodEvent)},
				}},
			}
			store := newMockStore()
			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))

			pub.mu.Lock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("evt-vm-after-bmi"))
			pub.mu.Unlock()
			store.mu.Lock()
			Expect(store.states).NotTo(HaveKey("bmi-invalid"))
			Expect(store.states).To(HaveKey("evt-vm-after-bmi"))
			store.mu.Unlock()
		})

		It("emits started events for first allocation after FAILED", func() {
			store := newMockStore()
			failedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			recoveredAt := failedAt.Add(time.Hour)
			store.states["bmi-first-recovery"] = projection.ResourceState{
				ResourceID:         "bmi-first-recovery",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_FAILED",
				TransitionTime:     failedAt,
				FulfillmentVersion: 3,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bmi-type-gpu-large",
					"catalog_item":     "catalog-item-1",
				},
			}

			bmi := makeBareMetalInstance("bmi-first-recovery", "tenant-1")
			bmi.Metadata.Version = 4
			bmi.Status.StateTransitionTime = timestamppb.New(recoveredAt)
			event := &privatev1.Event{
				Id:      "evt-bmi-first-recovery",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-first-recovery/allocation"))
			Expect(pub.published[1].ID()).To(Equal("evt-bmi-first-recovery/consumption"))
			Expect(pub.published[0].Type()).To(Equal(events.EventStarted))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted))
		})

		It("rejects BMaaS dimension drift without relabeling an active interval", func() {
			store := newMockStore()
			startedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store.states["bmi-running-dimension-update"] = projection.ResourceState{
				ResourceID:    "bmi-running-dimension-update",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:    true,
				BillableSince: &startedAt,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &startedAt, FirstStartedAt: &startedAt,
					},
				},
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bmi-type-gpu-large",
					"catalog_item":     "catalog-item-1",
				},
			}

			bmi := makeBareMetalInstance("bmi-running-dimension-update", "tenant-1")
			bmi.Metadata.Version = 2
			bmi.Spec.InstanceType.Id = "bmi-type-gpu-xlarge"
			transitionAt := startedAt.Add(time.Hour)
			bmi.Status.StateTransitionTime = timestamppb.New(transitionAt)
			event := &privatev1.Event{
				Id:      "evt-bmi-running-dimension-update",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty())
			pub.mu.Unlock()
			store.mu.Lock()
			updated := store.states["bmi-running-dimension-update"]
			Expect(updated.ComponentBillableSince).To(BeNil())
			Expect(updated.BillingDimensions["bm_instance_type"]).To(Equal("bmi-type-gpu-large"))
			Expect(updated.FulfillmentVersion).To(Equal(int32(1)))
			store.mu.Unlock()
		})

		It("skips invalid BMaaS transitions and processes later events on the same stream", func() {
			store := newMockStore()
			now := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store.states["bmi-invalid-transition"] = projection.ResourceState{
				ResourceID:    "bmi-invalid-transition",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:    true,
				BillableSince: &now,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &now, FirstStartedAt: &now,
					},
					Consumption: projection.MeterState{
						ActiveSince: &now, FirstStartedAt: &now,
					},
				},
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bmi-type-gpu-large",
					"catalog_item":     "catalog-item-1",
				},
			}

			invalid := makeBareMetalInstance("bmi-invalid-transition", "tenant-1")
			invalid.Metadata.Version = 2
			invalid.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_PROVISIONING
			invalid.Status.StateTransitionTime = timestamppb.New(now.Add(time.Minute))
			valid := makeBareMetalInstance("bmi-invalid-transition", "tenant-1")
			valid.Metadata.Version = 3
			valid.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING
			valid.Status.StateTransitionTime = timestamppb.New(now.Add(2 * time.Minute))
			invalidEvent := &privatev1.Event{Id: "evt-bmi-invalid-transition", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: invalid}}
			validEvent := &privatev1.Event{Id: "evt-bmi-valid-after-invalid", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: valid}}
			client.results = []mockStreamResult{
				{stream: &mockWatchStream{
					responses: []*privatev1.EventsWatchResponse{makeResponse(invalidEvent), makeResponse(validEvent)},
				}},
				{stream: &mockWatchStream{
					responses: []*privatev1.EventsWatchResponse{makeResponse(validEvent)},
				}},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())
			Expect(client.watchCallCount()).To(Equal(1))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-valid-after-invalid/consumption"))
			Expect(pub.published[0].Type()).To(Equal(events.EventSuspended))
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["bmi-invalid-transition"].CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_STOPPING"))
		})

		It("does not open an allocation interval for FAILED to DELETING skip", func() {
			store := newMockStore()
			failedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store.states["bmi-failed-deleting"] = projection.ResourceState{
				ResourceID:         "bmi-failed-deleting",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_FAILED",
				TransitionTime:     failedAt,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bmi-type-gpu-large",
					"catalog_item":     "catalog-item-1",
				},
			}

			bmi := makeBareMetalInstance("bmi-failed-deleting", "tenant-1")
			bmi.Metadata.Version = 2
			deletingAt := failedAt.Add(time.Hour)
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING
			bmi.Status.StateTransitionTime = timestamppb.New(deletingAt)
			event := &privatev1.Event{
				Id:      "evt-bmi-failed-deleting",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published).To(BeEmpty())
			pub.mu.Unlock()
			store.mu.Lock()
			updated := store.states["bmi-failed-deleting"]
			Expect(updated.CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_DELETING"))
			Expect(updated.IsBillable).To(BeFalse())
			Expect(updated.BillableSince).To(BeNil())
			store.mu.Unlock()
		})

		It("emits independent allocation and consumption events when BMaaS enters RUNNING", func() {
			store := newMockStore()
			t0 := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := t0.Add(time.Hour)
			store.states["bmi-running"] = projection.ResourceState{
				ResourceID:         "bmi-running",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_PROVISIONING",
				TransitionTime:     t0,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
			}

			bmi := makeBareMetalInstance("bmi-running", "tenant-1")
			bmi.Metadata.Version = 2
			bmi.Status.StateTransitionTime = timestamppb.New(t1)
			event := &privatev1.Event{
				Id:      "evt-bmi-running",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2)}
			consumer := newConsumerWithStore(pub, store)

			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(BeNumerically(">=", 1))
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-running/allocation"))
			Expect(pub.published[1].ID()).To(Equal("evt-bmi-running/consumption"))
			Expect(pub.published[0].Type()).To(Equal(events.EventStarted))
			Expect(pub.published[1].Type()).To(Equal(events.EventStarted))

			for _, published := range pub.published {
				var data map[string]any
				Expect(json.Unmarshal(published.Data(), &data)).To(Succeed())
				billingDims, ok := data["billing_dimensions"].(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(billingDims["bm_instance_type"]).To(Equal("bmi-type-gpu-large"))
				Expect(billingDims["meter_type"]).NotTo(BeNil())
			}
		})

		It("suspends only consumption when BMaaS leaves RUNNING", func() {
			store := newMockStore()
			t0 := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := t0.Add(time.Hour)
			store.states["bmi-stopping"] = projection.ResourceState{
				ResourceID:    "bmi-stopping",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "RUNNING",
				IsBillable:    true,
				BillableSince: &t0,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &t0, FirstStartedAt: &t0,
					},
					Consumption: projection.MeterState{
						ActiveSince: &t0, FirstStartedAt: &t0,
					},
				},
				TransitionTime:     t0,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
			}

			bmi := makeBareMetalInstance("bmi-stopping", "tenant-1")
			bmi.Metadata.Version = 2
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING
			bmi.Status.StateTransitionTime = timestamppb.New(t1)
			event := &privatev1.Event{
				Id:      "evt-bmi-stopping",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1)}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(Equal(1))
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-stopping/consumption"))
			Expect(pub.published[0].Type()).To(Equal(events.EventSuspended))
			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["duration_seconds"]).To(BeNumerically("==", 3600))
			pub.mu.Unlock()

			store.mu.Lock()
			updated := store.states["bmi-stopping"]
			Expect(updated.IsBillable).To(BeTrue())
			Expect(updated.BillableSince).To(Equal(&t0))
			Expect(updated.ComponentBillableSince).To(BeNil())
			store.mu.Unlock()
		})

		It("covers BMaaS meter boundaries at the Watch handler", func() {
			t0 := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := t0.Add(time.Hour)
			for _, test := range []struct {
				name            string
				id              string
				previousState   string
				currentState    privatev1.BareMetalInstanceState
				allocationSince *time.Time
				meterState      projection.BMaaSMeterState
				wantIDs         []string
				wantTypes       []string
			}{
				{
					name:          "provisioning to starting",
					id:            "provisioning-starting",
					previousState: "PROVISIONING",
					currentState:  privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STARTING,
					wantIDs:       []string{"evt-boundary/provisioning-starting/allocation"},
					wantTypes:     []string{events.EventStarted},
				},
				{
					name:          "provisioning to stopped",
					id:            "provisioning-stopped",
					previousState: "PROVISIONING",
					currentState:  privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED,
					wantIDs:       []string{"evt-boundary/provisioning-stopped/allocation"},
					wantTypes:     []string{events.EventStarted},
				},
				{
					name:            "stopping to running with consumption already started",
					id:              "stopping-running",
					previousState:   "STOPPING",
					currentState:    privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
					allocationSince: &t0,
					meterState: projection.BMaaSMeterState{
						Allocation:  projection.MeterState{ActiveSince: &t0, FirstStartedAt: &t0},
						Consumption: projection.MeterState{FirstStartedAt: &t0},
					},
					wantIDs:   []string{"evt-boundary/stopping-running/consumption"},
					wantTypes: []string{events.EventResumed},
				},
				{
					name:            "running to deleting",
					id:              "running-deleting",
					previousState:   "BARE_METAL_INSTANCE_STATE_RUNNING",
					currentState:    privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING,
					allocationSince: &t0,
					meterState: projection.BMaaSMeterState{
						Allocation:  projection.MeterState{ActiveSince: &t0, FirstStartedAt: &t0},
						Consumption: projection.MeterState{ActiveSince: &t0, FirstStartedAt: &t0},
					},
					wantIDs:   []string{"evt-boundary/running-deleting/consumption"},
					wantTypes: []string{events.EventSuspended},
				},
				{
					name:            "first stopped to running",
					id:              "first-stopped-running",
					previousState:   "BARE_METAL_INSTANCE_STATE_STOPPED",
					currentState:    privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
					allocationSince: &t0,
					meterState: projection.BMaaSMeterState{
						Allocation: projection.MeterState{ActiveSince: &t0, FirstStartedAt: &t0},
					},
					wantIDs:   []string{"evt-boundary/first-stopped-running/consumption"},
					wantTypes: []string{events.EventStarted},
				},
				{
					name:            "later stopped to running",
					id:              "later-stopped-running",
					previousState:   "BARE_METAL_INSTANCE_STATE_STOPPED",
					currentState:    privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
					allocationSince: &t0,
					meterState: projection.BMaaSMeterState{
						Allocation:  projection.MeterState{ActiveSince: &t0, FirstStartedAt: &t0},
						Consumption: projection.MeterState{FirstStartedAt: &t0},
					},
					wantIDs:   []string{"evt-boundary/later-stopped-running/consumption"},
					wantTypes: []string{events.EventResumed},
				},
			} {
				By(test.name)
				client = &mockEventsClient{}
				store := newMockStore()
				resourceID := "bmi-" + test.name
				store.states[resourceID] = projection.ResourceState{
					ResourceID:         resourceID,
					ResourceType:       events.ResourceTypeBareMetalInstance,
					TenantID:           "tenant-1",
					CurrentState:       test.previousState,
					IsBillable:         test.allocationSince != nil,
					BillableSince:      test.allocationSince,
					BMaaSMeterState:    test.meterState,
					FulfillmentVersion: 1,
					BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
				}
				bmi := makeBareMetalInstance(resourceID, "tenant-1")
				bmi.Metadata.Version = 2
				bmi.Status.State = test.currentState
				bmi.Status.StateTransitionTime = timestamppb.New(t1)
				eventID := "evt-boundary/" + test.id
				event := &privatev1.Event{
					Id: eventID, Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
					Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
				}
				runCtx, runCancel := context.WithCancel(context.Background())
				client.results = []mockStreamResult{{stream: &mockWatchStream{
					responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
				}}}
				pub := &mockPublisher{published: make([]cloudevents.Event, 0, len(test.wantIDs)), cancelFunc: runCancel}
				consumer := newConsumerWithStore(pub, store)
				Expect(consumer.Run(runCtx)).To(Succeed())
				runCancel()

				pub.mu.Lock()
				Expect(pub.published).To(HaveLen(len(test.wantIDs)))
				for i, wantID := range test.wantIDs {
					Expect(pub.published[i].ID()).To(Equal(wantID))
					Expect(pub.published[i].Type()).To(Equal(test.wantTypes[i]))
				}
				pub.mu.Unlock()
				store.mu.Lock()
				updated := store.states[resourceID]
				Expect(updated.IsBillable).To(BeTrue())
				Expect(updated.BillableSince).NotTo(BeNil())
				Expect(updated.ComponentBillableSince).To(BeNil())
				store.mu.Unlock()
			}
		})

		It("closes active BMaaS meters at the authoritative deletion timestamp", func() {
			store := newMockStore()
			startedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			deletionRequestedAt := startedAt.Add(30 * time.Minute)
			deletionCompletedAt := startedAt.Add(2 * time.Hour)
			store.states["bmi-delete"] = projection.ResourceState{
				ResourceID:    "bmi-delete",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_DELETING",
				IsBillable:    true,
				BillableSince: &startedAt,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &startedAt, FirstStartedAt: &startedAt,
					},
					Consumption: projection.MeterState{
						ActiveSince: &startedAt, FirstStartedAt: &startedAt,
					},
				},
				TransitionTime:     deletionRequestedAt,
				FulfillmentVersion: 4,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
			}

			bmi := makeBareMetalInstance("bmi-delete", "tenant-1")
			bmi.Metadata.Version = 4
			bmi.Metadata.DeletionTimestamp = timestamppb.New(deletionRequestedAt)
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING
			event := &privatev1.Event{
				Id:        "evt-bmi-delete",
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
				Timestamp: timestamppb.New(deletionCompletedAt),
				Payload:   &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 3)}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(Equal(3))
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-delete/allocation"))
			Expect(pub.published[1].ID()).To(Equal("evt-bmi-delete/consumption"))
			Expect(pub.published[2].ID()).To(Equal("evt-bmi-delete"))
			Expect(pub.published[0].Time()).To(Equal(deletionCompletedAt))
			Expect(pub.published[1].Time()).To(Equal(deletionCompletedAt))
			Expect(pub.published[2].Time()).To(Equal(deletionCompletedAt))
			pub.mu.Unlock()

			store.mu.Lock()
			Expect(store.states).NotTo(HaveKey("bmi-delete"))
			store.mu.Unlock()
		})

		It("resumes both BMaaS meters after recovery from FAILED", func() {
			store := newMockStore()
			failedAt := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			recoveredAt := failedAt.Add(time.Hour)
			store.states["bmi-recovered"] = projection.ResourceState{
				ResourceID:         "bmi-recovered",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				TenantID:           "tenant-1",
				CurrentState:       "BARE_METAL_INSTANCE_STATE_FAILED",
				EverBillable:       true,
				TransitionTime:     failedAt,
				FulfillmentVersion: 3,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation:  projection.MeterState{FirstStartedAt: &failedAt},
					Consumption: projection.MeterState{FirstStartedAt: &failedAt},
				},
				BillingDimensions: map[string]any{
					"bm_instance_type": "bmi-type-gpu-large",
					"catalog_item":     "catalog-item-1",
				},
			}

			bmi := makeBareMetalInstance("bmi-recovered", "tenant-1")
			bmi.Metadata.Version = 4
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING
			bmi.Status.StateTransitionTime = timestamppb.New(recoveredAt)
			event := &privatev1.Event{
				Id:      "evt-bmi-recovered",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 2)}
			consumer := newConsumerWithStore(pub, store)
			done := make(chan error, 1)
			go func() { done <- consumer.Run(ctx) }()
			Eventually(func() int {
				pub.mu.Lock()
				defer pub.mu.Unlock()
				return len(pub.published)
			}, time.Second).Should(Equal(2))
			cancel()
			Eventually(done, time.Second).Should(Receive(BeNil()))

			pub.mu.Lock()
			Expect(pub.published[0].Type()).To(Equal(events.EventResumed))
			Expect(pub.published[1].Type()).To(Equal(events.EventResumed))
			pub.mu.Unlock()

			store.mu.Lock()
			updated := store.states["bmi-recovered"]
			Expect(updated.CurrentState).To(Equal("BARE_METAL_INSTANCE_STATE_RUNNING"))
			Expect(updated.BillableSince).NotTo(BeNil())
			Expect(updated.ComponentBillableSince).To(BeNil())
			store.mu.Unlock()
		})

		It("does not republish a duplicate BMaaS transition", func() {
			store := newMockStore()
			t0 := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store.states["bmi-duplicate"] = projection.ResourceState{
				ResourceID:    "bmi-duplicate",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "BARE_METAL_INSTANCE_STATE_RUNNING",
				IsBillable:    true,
				BillableSince: &t0,
				BMaaSMeterState: projection.BMaaSMeterState{
					Allocation: projection.MeterState{
						ActiveSince: &t0, FirstStartedAt: &t0,
					},
					Consumption: projection.MeterState{
						ActiveSince: &t0, FirstStartedAt: &t0,
					},
				},
				TransitionTime:     t0,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"bm_instance_type": "bmi-type-gpu-large", "catalog_item": "catalog-item-1"},
			}

			bmi := makeBareMetalInstance("bmi-duplicate", "tenant-1")
			bmi.Metadata.Version = 2
			bmi.Status.State = privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING
			bmi.Status.StateTransitionTime = timestamppb.New(t0.Add(time.Hour))
			event := &privatev1.Event{
				Id:      "evt-bmi-duplicate",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_BareMetalInstance{BareMetalInstance: bmi},
			}
			client.results = []mockStreamResult{{stream: &mockWatchStream{
				ctx:       ctx,
				responses: []*privatev1.EventsWatchResponse{makeResponse(event), makeResponse(event)},
			}}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)
			Expect(consumer.Run(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(Equal("evt-bmi-duplicate/consumption"))
		})
	})
})
