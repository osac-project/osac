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
	"github.com/osac-project/osac-metering/internal/events"
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
			Expect(client.calls[0].GetFilter()).To(Equal("has(event.compute_instance) || has(event.cluster)"))
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

		It("skips metadata-only update with no state_transition_time without killing the stream", func() {
			store := newMockStore()
			store.states["vm-meta"] = projection.ResourceState{
				ResourceID:   "vm-meta",
				ResourceType: "compute_instance",
				TenantID:     "tenant-1",
				CurrentState: "STARTING",
			}

			ciNoTimestamp := makeComputeInstance("vm-meta", "tenant-1")
			ciNoTimestamp.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING
			ciNoTimestamp.Status.StateTransitionTime = nil

			ciRunning := makeComputeInstance("vm-meta", "tenant-1")
			ciRunning.Status.State = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
			ciRunning.Metadata.Version = 2

			stream := &mockWatchStream{
				responses: []*privatev1.EventsWatchResponse{
					makeResponse(&privatev1.Event{
						Id:      "evt-meta-update",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciNoTimestamp},
					}),
					makeResponse(&privatev1.Event{
						Id:      "evt-running",
						Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
						Payload: &privatev1.Event_ComputeInstance{ComputeInstance: ciRunning},
					}),
				},
			}
			client.results = []mockStreamResult{{stream: stream}}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Single stream — no reconnect
			Expect(client.watchCallCount()).To(Equal(1))

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].Type()).To(Equal("osac.resource.resumed.v1"))
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
			client.results = []mockStreamResult{
				{stream: stream1},
				{stream: stream2},
			}

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Stream reconnected — real data quality issue, fail fast
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

		It("emits resumed.v1 for STOPPED to RUNNING transition", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-resume"] = projection.ResourceState{
				ResourceID:         "vm-resume",
				ResourceType:       events.ResourceTypeComputeInstance,
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
			Expect(pub.published[0].Type()).To(Equal(events.EventDeleted))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-del"))
		})

		It("publishes event but skips projection update on stale version", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["vm-stale"] = projection.ResourceState{
				ResourceID:         "vm-stale",
				ResourceType:       events.ResourceTypeComputeInstance,
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

			pub := &mockPublisher{published: make([]cloudevents.Event, 0, 1), cancelFunc: cancel}
			consumer := newConsumerWithStore(pub, store)

			err := consumer.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1), "event published even when projection is stale")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-stale"].FulfillmentVersion).To(Equal(int32(10)))
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
				"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 2},
				"cpu-workers": {HostType: &privatev1.HostTypeReference{Name: "cpu-only"}, Size: 3},
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
				Id:      "evt-flat-del",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
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
				"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 4},
				"cpu-workers": {HostType: &privatev1.HostTypeReference{Name: "cpu-only"}, Size: 3},
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
				"tpu-workers": {HostType: &privatev1.HostTypeReference{Name: "tpu-v5"}, Size: 2},
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
				"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 4},
				"tpu-workers": {HostType: &privatev1.HostTypeReference{Name: "tpu-v5"}, Size: 2},
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
				"cpu-workers": {HostType: &privatev1.HostTypeReference{Name: "cpu-only"}, Size: 5},
				"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 2},
			})
			clAtT1.Status.StateTransitionTime = timestamppb.New(t1)
			eventT1 := &privatev1.Event{
				Id:      "evt-t1",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				Payload: &privatev1.Event_Cluster{Cluster: clAtT1},
			}

			// T2: gpu-workers scales 2->4, cpu-workers stays at 5 (unchanged since T1).
			clAtT2 := makeCluster("cl-staggered", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, map[string]*privatev1.ClusterNodeSet{
				"cpu-workers": {HostType: &privatev1.HostTypeReference{Name: "cpu-only"}, Size: 5},
				"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: 4},
			})
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
				Id:      "evt-del",
				Type:    privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
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
			Expect(pub.published[0].Type()).To(Equal(events.EventDeleted))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("cl-del"))
		})
	})
})
