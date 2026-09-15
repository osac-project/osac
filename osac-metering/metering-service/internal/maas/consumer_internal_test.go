package maas

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/schema"
)

type mockPublisher struct {
	mu        sync.Mutex
	published []cloudevents.Event
	err       error
}

func (p *mockPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, event)
	return nil
}

func (p *mockPublisher) events() []cloudevents.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]cloudevents.Event{}, p.published...)
}

type mockTenantsClient struct {
	tenants []string
	err     error
}

func (m *mockTenantsClient) List(_ context.Context, _ *privatev1.TenantsListRequest, _ ...grpc.CallOption) (*privatev1.TenantsListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	items := make([]*privatev1.Tenant, len(m.tenants))
	for i, name := range m.tenants {
		items[i] = &privatev1.Tenant{
			Metadata: &privatev1.Metadata{Name: name},
		}
	}
	return &privatev1.TenantsListResponse{Items: items}, nil
}

func makeRawEvent(orgID, model string, promptTokens, completionTokens int) []byte {
	event := map[string]any{
		"specversion":     "1.0",
		"id":              "evt-test-123",
		"source":          "maas-gateway",
		"type":            "inference.tokens.used",
		"time":            "2026-08-15T12:00:00Z",
		"datacontenttype": "application/json",
		"data": map[string]any{
			"user":                  "alice",
			"group":                 "engineering",
			"subscription":          "ai-tenant-acme/sub-1",
			"organization_id":       orgID,
			"cost_center":           "ml-team",
			"provider":              "anthropic",
			"model":                 model,
			"prompt_tokens":         promptTokens,
			"completion_tokens":     completionTokens,
			"total_tokens":          promptTokens + completionTokens,
			"cached_input_tokens":   0,
			"cache_creation_tokens": 0,
			"reasoning_tokens":      0,
			"duration_ms":           1500,
			"user_agent":            "test/1.0",
		},
	}
	data, _ := json.Marshal(event)
	return data
}

var _ = Describe("consumerHandler", func() {
	var (
		publisher *mockPublisher
		cache     *TenantCache
		handler   *consumerHandler
	)

	BeforeEach(func() {
		publisher = &mockPublisher{}
		cache = NewTenantCache(
			&mockTenantsClient{tenants: []string{"acme-corp"}},
			logr.Discard(),
			5*time.Minute,
		)
		Expect(cache.Load(context.Background())).To(Succeed())
		handler = &consumerHandler{
			publisher:   publisher,
			tenantCache: cache,
			logger:      logr.Discard(),
		}
	})

	It("enriches and publishes a valid inference event", func() {
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("acme-corp", "claude-sonnet-4-20250514", 1000, 500),
		}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).NotTo(HaveOccurred())

		published := publisher.events()
		Expect(published).To(HaveLen(1))

		ce := published[0]
		Expect(ce.Type()).To(Equal(events.EventInferenceUsage))
		Expect(ce.Extensions()["osacresourcetype"]).To(Equal(events.ResourceTypeMaaSInference))
		Expect(ce.Extensions()["osactenant"]).To(Equal("acme-corp"))
		Expect(ce.Time()).To(Equal(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)))

		var data schema.LifecycleData
		Expect(json.Unmarshal(ce.Data(), &data)).To(Succeed())
		Expect(data.TenantID).To(Equal("acme-corp"))
		Expect(data.ResourceType).To(Equal(events.ResourceTypeMaaSInference))
		Expect(data.BillingDimensions["model"]).To(Equal("claude-sonnet-4-20250514"))
		Expect(data.BillingDimensions["prompt_tokens"]).To(BeNumerically("==", 1000))
		Expect(data.BillingDimensions["completion_tokens"]).To(BeNumerically("==", 500))
		Expect(data.BillingDimensions["total_tokens"]).To(BeNumerically("==", 1500))
		Expect(data.BillingDimensions["organization_id"]).To(Equal("acme-corp"))
		Expect(data.SchemaVersion).To(Equal("v1"))
	})

	It("permanently rejects events with missing organization_id", func() {
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("", "claude-sonnet-4-20250514", 100, 50),
		}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no organization_id"))
		Expect(errors.As(err, &permanentError{})).To(BeTrue())
		Expect(publisher.events()).To(BeEmpty())
	})

	It("permanently rejects events with unknown tenant", func() {
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("unknown-org", "gpt-4o", 100, 50),
		}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does not match any known tenant"))
		Expect(errors.As(err, &permanentError{})).To(BeTrue())
		Expect(publisher.events()).To(BeEmpty())
	})

	It("returns transient error when tenant API fails", func() {
		failingClient := &mockTenantsClient{tenants: []string{}, err: errors.New("grpc unavailable")}
		failingCache := NewTenantCache(failingClient, logr.Discard(), 5*time.Minute)
		failingHandler := &consumerHandler{
			publisher:   publisher,
			tenantCache: failingCache,
			logger:      logr.Discard(),
		}
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("some-org", "gpt-4o", 100, 50),
		}
		err := failingHandler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("refreshing tenant cache"))
		Expect(errors.As(err, &permanentError{})).To(BeFalse())
		Expect(publisher.events()).To(BeEmpty())
	})

	It("permanently rejects events with missing model", func() {
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("acme-corp", "", 100, 50),
		}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no model"))
		Expect(errors.As(err, &permanentError{})).To(BeTrue())
		Expect(publisher.events()).To(BeEmpty())
	})

	It("permanently rejects events with invalid time", func() {
		event := map[string]any{
			"specversion": "1.0",
			"id":          "evt-bad-time",
			"source":      "maas-gateway",
			"type":        "inference.tokens.used",
			"time":        "not-a-timestamp",
			"data": map[string]any{
				"organization_id": "acme-corp",
				"model":           "gpt-4o",
			},
		}
		data, _ := json.Marshal(event)
		msg := &sarama.ConsumerMessage{Value: data}

		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid time"))
		Expect(errors.As(err, &permanentError{})).To(BeTrue())
	})

	It("permanently rejects malformed JSON", func() {
		msg := &sarama.ConsumerMessage{Value: []byte("not json")}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing CloudEvent"))
		Expect(errors.As(err, &permanentError{})).To(BeTrue())
	})

	It("returns transient error when publisher fails", func() {
		publisher.err = errors.New("kafka unavailable")
		msg := &sarama.ConsumerMessage{
			Value: makeRawEvent("acme-corp", "claude-sonnet-4-20250514", 100, 50),
		}
		err := handler.processMessage(context.Background(), msg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("publishing enriched inference event"))
		Expect(errors.As(err, &permanentError{})).To(BeFalse())
		Expect(publisher.events()).To(BeEmpty())
	})

	It("preserves float64 duration_ms without truncation", func() {
		event := map[string]any{
			"specversion": "1.0",
			"id":          "evt-duration-test",
			"source":      "maas-gateway",
			"type":        "inference.tokens.used",
			"time":        "2026-08-15T12:00:00Z",
			"data": map[string]any{
				"organization_id": "acme-corp",
				"model":           "gpt-4o",
				"prompt_tokens":   100,
				"total_tokens":    100,
				"duration_ms":     1234.567,
			},
		}
		raw, _ := json.Marshal(event)
		msg := &sarama.ConsumerMessage{Value: raw}

		err := handler.processMessage(context.Background(), msg)
		Expect(err).NotTo(HaveOccurred())

		published := publisher.events()
		Expect(published).To(HaveLen(1))

		var ld schema.LifecycleData
		Expect(json.Unmarshal(published[0].Data(), &ld)).To(Succeed())
		Expect(ld.BillingDimensions["duration_ms"]).To(BeNumerically("~", 1234.567, 0.001))
	})
})

var _ = Describe("ConsumeClaim", func() {
	var (
		publisher *mockPublisher
		cache     *TenantCache
		handler   *consumerHandler
	)

	BeforeEach(func() {
		publisher = &mockPublisher{}
		cache = NewTenantCache(
			&mockTenantsClient{tenants: []string{"acme-corp"}},
			logr.Discard(),
			5*time.Minute,
		)
		Expect(cache.Load(context.Background())).To(Succeed())
		handler = &consumerHandler{
			publisher:   publisher,
			tenantCache: cache,
			logger:      logr.Discard(),
		}
	})

	It("marks offset for permanently failed messages and continues", func() {
		session := &mockConsumerGroupSession{}
		messages := make(chan *sarama.ConsumerMessage, 2)
		claim := &mockConsumerGroupClaim{messages: messages}

		messages <- &sarama.ConsumerMessage{Value: []byte("bad json"), Offset: 0, Partition: 0}
		messages <- &sarama.ConsumerMessage{
			Value:     makeRawEvent("acme-corp", "gpt-4o", 100, 50),
			Offset:    1,
			Partition: 0,
		}
		close(messages)

		err := handler.ConsumeClaim(session, claim)
		Expect(err).NotTo(HaveOccurred())

		Expect(session.marked).To(HaveLen(2))
		Expect(session.marked[0]).To(Equal(int64(0)))
		Expect(session.marked[1]).To(Equal(int64(1)))
		Expect(publisher.events()).To(HaveLen(1))
	})

	It("marks offset and skips permanently failed unknown tenant", func() {
		session := &mockConsumerGroupSession{}
		messages := make(chan *sarama.ConsumerMessage, 2)
		claim := &mockConsumerGroupClaim{messages: messages}

		messages <- &sarama.ConsumerMessage{
			Value:     makeRawEvent("nonexistent-org", "gpt-4o", 100, 50),
			Offset:    0,
			Partition: 0,
		}
		messages <- &sarama.ConsumerMessage{
			Value:     makeRawEvent("acme-corp", "gpt-4o", 200, 100),
			Offset:    1,
			Partition: 0,
		}
		close(messages)

		err := handler.ConsumeClaim(session, claim)
		Expect(err).NotTo(HaveOccurred())

		Expect(session.marked).To(HaveLen(2))
		Expect(publisher.events()).To(HaveLen(1))
	})

	It("returns transient error on publish failure without marking", func() {
		publisher.err = errors.New("kafka down")
		session := &mockConsumerGroupSession{}
		messages := make(chan *sarama.ConsumerMessage, 1)
		claim := &mockConsumerGroupClaim{messages: messages}

		messages <- &sarama.ConsumerMessage{
			Value:     makeRawEvent("acme-corp", "gpt-4o", 100, 50),
			Offset:    0,
			Partition: 0,
		}
		close(messages)

		err := handler.ConsumeClaim(session, claim)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("publishing enriched inference event"))

		Expect(session.marked).To(BeEmpty())
		Expect(session.commitCount).To(Equal(1))
	})
})

type mockConsumerGroupSession struct {
	marked      []int64
	commitCount int
	ctx         context.Context
}

func (s *mockConsumerGroupSession) Claims() map[string][]int32 {
	return nil
}
func (s *mockConsumerGroupSession) MemberID() string {
	return "test"
}
func (s *mockConsumerGroupSession) GenerationID() int32 {
	return 1
}
func (s *mockConsumerGroupSession) MarkOffset(string, int32, int64, string)  {}
func (s *mockConsumerGroupSession) ResetOffset(string, int32, int64, string) {}
func (s *mockConsumerGroupSession) MarkMessage(msg *sarama.ConsumerMessage, _ string) {
	s.marked = append(s.marked, msg.Offset)
}
func (s *mockConsumerGroupSession) Commit() { s.commitCount++ }
func (s *mockConsumerGroupSession) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

type mockConsumerGroupClaim struct {
	messages chan *sarama.ConsumerMessage
}

func (c *mockConsumerGroupClaim) Topic() string                            { return "test-topic" }
func (c *mockConsumerGroupClaim) Partition() int32                         { return 0 }
func (c *mockConsumerGroupClaim) InitialOffset() int64                     { return 0 }
func (c *mockConsumerGroupClaim) HighWaterMarkOffset() int64               { return 0 }
func (c *mockConsumerGroupClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }
