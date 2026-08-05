package watch_test

import (
	"context"
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
		c := watch.NewConsumer(client, pub, logr.Discard())
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
	})
})
