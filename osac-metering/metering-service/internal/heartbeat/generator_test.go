package heartbeat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/projection"
)

type mockStore struct {
	mu         sync.Mutex
	billable   []projection.ResourceState
	listErr    error
	updatedIDs []string
	updatedAt  time.Time
	updateErr  error
}

func (s *mockStore) Get(_ context.Context, _ string) (*projection.ResourceState, error) {
	return nil, nil
}
func (s *mockStore) Upsert(_ context.Context, _ projection.ResourceState) error    { return nil }
func (s *mockStore) Delete(_ context.Context, _ string) error                      { return nil }
func (s *mockStore) ListAll(_ context.Context) ([]projection.ResourceState, error) { return nil, nil }

func (s *mockStore) ListBillable(_ context.Context) ([]projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.billable, nil
}

func (s *mockStore) UpdateLastHeartbeat(_ context.Context, ids []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updatedIDs = ids
	s.updatedAt = at
	return nil
}

type mockPublisher struct {
	mu        sync.Mutex
	published []cloudevents.Event
	err       error
	failAfter int
	callCount int
}

func (p *mockPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if p.failAfter > 0 && p.callCount > p.failAfter {
		return p.err
	}
	if p.err != nil && p.failAfter == 0 {
		return p.err
	}
	p.published = append(p.published, event)
	return nil
}

func makeBillableState(id string) projection.ResourceState {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return projection.ResourceState{
		ResourceID:    id,
		ResourceType:  "compute_instance",
		TenantID:      "tenant-1",
		ProjectID:     "project-1",
		CurrentState:  "RUNNING",
		IsBillable:    true,
		BillableSince: &now,
		BillingDimensions: map[string]any{
			"instance_type": "m5.large",
		},
	}
}

var _ = Describe("Generator", func() {
	Describe("Run", func() {
		It("publishes heartbeats for billable resources on each tick", func() {
			store := &mockStore{
				billable: []projection.ResourceState{
					makeBillableState("vm-1"),
					makeBillableState("vm-2"),
				},
			}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(len(pub.published)).To(BeNumerically(">=", 2))
			Expect(pub.published[0].Type()).To(Equal("osac.resource.heartbeat.v1"))
		})

		It("stops on context cancellation", func() {
			store := &mockStore{}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), time.Hour)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- gen.Run(ctx) }()

			time.Sleep(10 * time.Millisecond)
			cancel()

			Eventually(done, time.Second).Should(Receive(BeNil()))
		})
	})

	Describe("tick behavior", func() {
		It("does nothing when no billable resources exist", func() {
			store := &mockStore{billable: []projection.ResourceState{}}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("updates last_heartbeat_at after successful tick", func() {
			store := &mockStore{
				billable: []projection.ResourceState{
					makeBillableState("vm-1"),
				},
			}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.updatedIDs).To(ContainElement("vm-1"))
			Expect(store.updatedAt).ToNot(BeZero())
		})

		It("checkpoints published IDs on partial Kafka failure", func() {
			store := &mockStore{
				billable: []projection.ResourceState{
					makeBillableState("vm-1"),
					makeBillableState("vm-2"),
					makeBillableState("vm-3"),
				},
			}
			pub := &mockPublisher{
				err:       fmt.Errorf("kafka unavailable"),
				failAfter: 2,
			}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.updatedIDs).To(HaveLen(2))
		})

		It("fails tick when ListBillable returns error", func() {
			store := &mockStore{listErr: fmt.Errorf("database unavailable")}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("includes billing_dimensions in heartbeat CloudEvent", func() {
			store := &mockStore{
				billable: []projection.ResourceState{
					makeBillableState("vm-1"),
				},
			}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).ToNot(BeEmpty())

			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			bd := data["billing_dimensions"].(map[string]any)
			Expect(bd["instance_type"]).To(Equal("m5.large"))
		})

		It("sets osacresourceid extension on heartbeat events", func() {
			store := &mockStore{
				billable: []projection.ResourceState{
					makeBillableState("vm-1"),
				},
			}
			pub := &mockPublisher{}
			gen := heartbeat.NewGenerator(store, pub, logr.Discard(), 100*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			err := gen.Run(ctx)
			Expect(err).ToNot(HaveOccurred())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).ToNot(BeEmpty())
			Expect(pub.published[0].Extensions()["osacresourceid"]).To(Equal("vm-1"))
		})
	})
})
