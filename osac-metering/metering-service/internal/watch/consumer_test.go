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
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/projection"
	"github.com/osac-project/osac-metering/internal/watch"
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
	mu     sync.Mutex
	states map[string]projection.ResourceState
}

func newMockStore() *mockStore {
	return &mockStore{states: map[string]projection.ResourceState{}}
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
		c := watch.NewConsumer(client, pub, newMockStore(), logr.Discard())
		c.InitialDelay = time.Millisecond
		c.MaxDelay = time.Millisecond
		return c
	}

	newConsumerWithStore := func(pub *mockPublisher, store *mockStore) *watch.Consumer {
		c := watch.NewConsumer(client, pub, store, logr.Discard())
		c.InitialDelay = time.Millisecond
		c.MaxDelay = time.Millisecond
		return c
	}

	Describe("Run", func() {
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
			Expect(pub.published[0].Type()).To(Equal("osac.resource.created.v1"))
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
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(goodEvent),
				},
			}
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

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

		It("sets compute_instance filter on watch request", func() {
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
			Expect(client.calls[0].GetFilter()).To(Equal("has(event.compute_instance)"))
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
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

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
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

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
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumer(pub)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.watchCallCount()).To(BeNumerically(">=", 2))
		})

		It("skips same-state same-dimensions updates", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-1"] = projection.ResourceState{
				ResourceID:         "vm-1",
				ResourceType:       "compute_instance",
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

		It("emits resumed.v1 for STOPPED to RUNNING transition", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-resume"] = projection.ResourceState{
				ResourceID:         "vm-resume",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
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
			Expect(pub.published[0].Type()).To(Equal("osac.resource.resumed.v1"))
		})

		It("deletes projection on OBJECT_DELETED and publishes event", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-del"] = projection.ResourceState{
				ResourceID:         "vm-del",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{},
			}

			ci := makeComputeInstance("vm-del", "tenant-1")
			ci.Metadata.DeletionTimestamp = timestamppb.Now()
			event := &privatev1.Event{
				Id:      "vm-del",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
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
			Expect(pub.published[0].Type()).To(Equal("osac.resource.deleted.v1"))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-del"))
		})

		It("skips projection update on stale version and does not publish", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-stale"] = projection.ResourceState{
				ResourceID:         "vm-stale",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 10,
				BillingDimensions:  map[string]any{},
				TransitionTime:     now,
			}

			ci := makeComputeInstance("vm-stale", "tenant-1")
			ci.Metadata.Version = 5
			event := &privatev1.Event{
				Id:      "evt-stale",
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
			Expect(store.states["vm-stale"].FulfillmentVersion).To(Equal(int32(10)))
		})

		It("closes billing interval and resets BillableSince on dimension change while billable", func() {
			store := newMockStore()
			originalStart := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Microsecond)
			instanceType := "m5.large"
			store.states["vm-resize"] = projection.ResourceState{
				ResourceID:         "vm-resize",
				ResourceType:       "compute_instance",
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
			_ = instanceType

			event := &privatev1.Event{
				Id:      "evt-resize",
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

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["duration_seconds"]).ToNot(BeNil())
			Expect(data["duration_seconds"]).To(BeNumerically(">", 0))

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-resize"]
			Expect(updated.BillingDimensions["instance_type"]).To(Equal("m5.xlarge"))
			Expect(updated.BillableSince).ToNot(BeNil())
			Expect(updated.BillableSince.After(originalStart)).To(BeTrue())
		})

		It("preserves billing context through RUNNING→STOPPING→STOPPED sequence", func() {
			store := newMockStore()
			billableStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			store.states["vm-stop-seq"] = projection.ResourceState{
				ResourceID:         "vm-stop-seq",
				ResourceType:       "compute_instance",
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
			Expect(pub.published[0].Type()).To(Equal("osac.resource.suspended.v1"))

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
	})
})
