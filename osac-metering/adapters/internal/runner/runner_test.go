/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// testTopic stands in for a tenant heartbeat topic.
const testTopic = "osac.metering.heartbeat.test"

// --- Mock types ---

type mockAdapter struct {
	mu          sync.Mutex
	name        string
	submitErr   error
	submitFn    func(adapters.MeteringEvent) error
	flushErr    error
	submitCalls []adapters.MeteringEvent
	flushCalls  int
	closed      bool
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Submit(_ context.Context, event adapters.MeteringEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submitCalls = append(m.submitCalls, event)
	if m.submitFn != nil {
		return m.submitFn(event)
	}
	return m.submitErr
}
func (m *mockAdapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushCalls++
	return adapters.SubmitResult{}, m.flushErr
}
func (m *mockAdapter) HealthCheck(_ context.Context) error { return nil }
func (m *mockAdapter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type mockSession struct {
	ctx       context.Context
	mu        sync.Mutex
	marks     []markEntry
	committed int
}

type markEntry struct {
	topic     string
	partition int32
	offset    int64
}

func (s *mockSession) Claims() map[string][]int32 { return nil }
func (s *mockSession) MemberID() string           { return "test-member" }
func (s *mockSession) GenerationID() int32        { return 1 }
func (s *mockSession) MarkOffset(topic string, partition int32, offset int64, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.marks = append(s.marks, markEntry{topic, partition, offset})
}
func (s *mockSession) Commit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.committed++
}
func (s *mockSession) ResetOffset(string, int32, int64, string)    {}
func (s *mockSession) MarkMessage(*sarama.ConsumerMessage, string) {}
func (s *mockSession) Context() context.Context                    { return s.ctx }

type mockClaim struct {
	topic     string
	partition int32
	messages  chan *sarama.ConsumerMessage
}

func (c *mockClaim) Topic() string                            { return c.topic }
func (c *mockClaim) Partition() int32                         { return c.partition }
func (c *mockClaim) InitialOffset() int64                     { return 0 }
func (c *mockClaim) HighWaterMarkOffset() int64               { return 0 }
func (c *mockClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

type mockDLQSender struct {
	mu      sync.Mutex
	sends   []dlqSendEntry
	sendErr error
	sendFn  func(msg *sarama.ConsumerMessage) error
	closed  bool
}

type dlqSendEntry struct {
	msg      *sarama.ConsumerMessage
	reason   string
	attempts int
}

func (m *mockDLQSender) Send(msg *sarama.ConsumerMessage, reason string, attempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, dlqSendEntry{msg: msg, reason: reason, attempts: attempts})
	if m.sendFn != nil {
		return m.sendFn(msg)
	}
	return m.sendErr
}

func (m *mockDLQSender) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type mockDLQOccupier struct {
	mockDLQSender
	occupancy    int64
	occupancyErr error
	topic        string
}

func (m *mockDLQOccupier) Occupancy() (int64, error) {
	return m.occupancy, m.occupancyErr
}

func (m *mockDLQOccupier) Topic() string {
	if m.topic == "" {
		return testTopic
	}
	return m.topic
}

// --- Helpers ---

func newTestMessage(id, resourceID string, offset int64) *sarama.ConsumerMessage {
	return newTestMessageWithTime(id, resourceID, offset, time.Now().UTC())
}

func newTestMessageWithTime(id, resourceID string, offset int64, tt time.Time) *sarama.ConsumerMessage {
	ce := cloudevents.NewEvent()
	ce.SetID(id)
	ce.SetType("osac.metering.lifecycle.started.v1")
	ce.SetSource("osac-metering/test")
	ce.SetExtension("osacresourceid", resourceID)

	data := map[string]string{"transition_time": tt.Format(time.RFC3339)}
	dataBytes, _ := json.Marshal(data)
	_ = ce.SetData(cloudevents.ApplicationJSON, dataBytes)

	ceJSON, _ := json.Marshal(ce)
	return &sarama.ConsumerMessage{
		Topic:     testTopic,
		Partition: 0,
		Offset:    offset,
		Value:     ceJSON,
	}
}

func newTestMessageWithoutTransitionTime(id, resourceID string, offset int64) *sarama.ConsumerMessage {
	ce := cloudevents.NewEvent()
	ce.SetID(id)
	ce.SetType("osac.inference.usage.v1")
	ce.SetSource("osac-metering/test")
	ce.SetExtension("osacresourceid", resourceID)
	_ = ce.SetData(cloudevents.ApplicationJSON, json.RawMessage(`{"usage": 42}`))

	ceJSON, _ := json.Marshal(ce)
	return &sarama.ConsumerMessage{
		Topic:     testTopic,
		Partition: 0,
		Offset:    offset,
		Value:     ceJSON,
	}
}

func newRunner(adapter adapters.ProviderAdapter) *Runner {
	return NewRunner(adapter, RunnerConfig{
		TopicsProvider: func(context.Context) ([]string, error) {
			return []string{testTopic}, nil
		},
		FlushInterval: 10 * time.Second,
		DedupTTL:      10 * time.Minute,
		MaxRetries:    3,
	}, logr.Discard())
}

func newRunnerWithDLQ(adapter adapters.ProviderAdapter, dlq DLQSender) *Runner {
	return NewRunner(adapter, RunnerConfig{
		TopicsProvider: func(context.Context) ([]string, error) {
			return []string{testTopic}, nil
		},
		FlushInterval: 10 * time.Second,
		DedupTTL:      10 * time.Minute,
		MaxRetries:    3,
	}, logr.Discard(), WithDLQ(dlq))
}

func feedMessages(claim *mockClaim, msgs ...*sarama.ConsumerMessage) {
	go func() {
		for _, msg := range msgs {
			claim.messages <- msg
		}
		close(claim.messages)
	}()
}

// --- Tests ---

var _ = Describe("Runner", func() {
	var (
		adapter *mockAdapter
		runner  *Runner
		session *mockSession
		claim   *mockClaim
	)

	BeforeEach(func() {
		adapter = &mockAdapter{name: "test-provider"}
		runner = newRunner(adapter)
		session = &mockSession{ctx: context.Background()}
		claim = &mockClaim{
			topic:     testTopic,
			partition: 0,
			messages:  make(chan *sarama.ConsumerMessage, 10),
		}
		// Set session on runner (simulates Setup call)
		_ = runner.Setup(session)
	})

	Describe("ConsumeClaim — message processing", func() {
		It("calls Submit for each valid message", func() {
			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 0),
				newTestMessage("evt-2", "res-2", 1),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(2))
			Expect(adapter.submitCalls[0].CloudEvent.ID()).To(Equal("evt-1"))
			Expect(adapter.submitCalls[1].CloudEvent.ID()).To(Equal("evt-2"))
		})

		It("tracks offsets for commit", func() {
			feedMessages(claim, newTestMessage("evt-1", "res-1", 5))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(5)))
		})

		It("increments events_submitted_total metric", func() {
			feedMessages(claim, newTestMessage("evt-1", "res-1", 0))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(
				runner.metrics.eventsSubmitted.WithLabelValues("test-provider", testTopic),
			)
			Expect(val).To(Equal(float64(1)))
		})
	})

	Describe("ConsumeClaim — dedup suppression", func() {
		It("suppresses duplicate CloudEvent IDs", func() {
			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 0),
				newTestMessage("evt-1", "res-1", 1), // duplicate ID
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(1))

			val := testutil.ToFloat64(runner.metrics.duplicatesSuppressed.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})

		It("tracks offsets for duplicate messages", func() {
			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 0),
				newTestMessage("evt-1", "res-1", 3), // duplicate ID at higher offset
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(3)))
		})
	})

	Describe("ConsumeClaim — out-of-order detection", func() {
		It("detects out-of-order events and increments metric", func() {
			t1 := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
			t2 := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC) // earlier

			feedMessages(claim,
				newTestMessageWithTime("evt-1", "res-1", 0, t1),
				newTestMessageWithTime("evt-2", "res-1", 1, t2),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			// Both events are submitted (out-of-order events still go to Submit)
			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(2))

			val := testutil.ToFloat64(runner.metrics.outOfOrderEvents.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})

		It("skips detection for events without transition_time", func() {
			feedMessages(claim,
				newTestMessageWithoutTransitionTime("evt-1", "res-1", 0),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(1))

			val := testutil.ToFloat64(runner.metrics.outOfOrderEvents.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(0)))
		})
	})

	Describe("ConsumeClaim — error handling", func() {
		It("skips messages with invalid JSON", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     testTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte("not valid json{{{"),
			}
			feedMessages(claim, msg)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(0))

			val := testutil.ToFloat64(runner.metrics.eventsFailed.WithLabelValues("test-provider", "deserialization"))
			Expect(val).To(Equal(float64(1)))

			dropped := testutil.ToFloat64(runner.metrics.eventsDropped.WithLabelValues("test-provider"))
			Expect(dropped).To(Equal(float64(1)))

			runner.mu.Lock()
			offset, ok := runner.offsets[topicPartition{Topic: testTopic, Partition: 0}]
			runner.mu.Unlock()
			Expect(ok).To(BeTrue())
			Expect(offset).To(Equal(int64(0)))
		})

		It("increments non_retryable metric on NonRetryableError", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad schema")}
			feedMessages(claim, newTestMessage("evt-1", "res-1", 0))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(runner.metrics.eventsFailed.WithLabelValues("test-provider", "non_retryable"))
			Expect(val).To(Equal(float64(1)))
		})

		It("increments retries_exhausted metric after max retries", func() {
			adapter.submitErr = errors.New("always fails")
			feedMessages(claim, newTestMessage("evt-1", "res-1", 0))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(runner.metrics.eventsFailed.WithLabelValues("test-provider", "retries_exhausted"))
			Expect(val).To(Equal(float64(1)))
		})

		It("tracks offsets for non-retryable errors", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad schema")}
			feedMessages(claim, newTestMessage("evt-1", "res-1", 7))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(7)))
		})

		It("tracks offsets for exhausted retries", func() {
			adapter.submitErr = errors.New("always fails")
			feedMessages(claim, newTestMessage("evt-1", "res-1", 12))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(12)))
		})

		It("does not track offset when context is cancelled during retry", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancelSession := &mockSession{ctx: ctx}
			_ = runner.Setup(cancelSession)

			adapter.submitErr = errors.New("temporary failure")

			origSleep := sleepFunc
			sleepFunc = func(sleepCtx context.Context, _ time.Duration) error {
				cancel()
				return sleepCtx.Err()
			}
			defer func() { sleepFunc = origSleep }()

			msg := newTestMessage("evt-1", "res-1", 10)
			runner.processMessage(ctx, msg, "test-provider")

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			_, tracked := runner.offsets[tp]
			Expect(tracked).To(BeFalse(), "offset should not be tracked when context is cancelled")
		})
	})

	Describe("ConsumeClaim — inference dead letters", func() {
		It("dead-letters a rejected inference record and keeps consuming the partition", func() {
			// The DLQ send routes through the per-tenant producer's topicFor, so a
			// rejected inference record must land on the inference dead-letter topic
			// instead of failing and stopping the partition claim.
			dlqProducer := mocks.NewSyncProducer(GinkgoT(), nil)
			dlqProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					Expect(pm.Topic).To(Equal(kafka.InferenceDeadLetter),
						"a rejected inference record must land on the inference DLQ")
					return nil
				},
			)
			dlq := &DLQProducer{producer: dlqProducer, perTenant: true}
			inferenceRunner := newRunnerWithDLQ(adapter, dlq)
			_ = inferenceRunner.Setup(session)

			// The first record is rejected non-retryably; the second is accepted.
			adapter.submitFn = func(event adapters.MeteringEvent) error {
				if event.CloudEvent.ID() == "inf-bad" {
					return &adapters.NonRetryableError{Err: errors.New("bad schema")}
				}
				return nil
			}
			bad := newTestMessageWithoutTransitionTime("inf-bad", "res-1", 0)
			bad.Topic = kafka.TopicInference
			good := newTestMessageWithoutTransitionTime("inf-good", "res-2", 1)
			good.Topic = kafka.TopicInference

			claim := &mockClaim{
				topic:     kafka.TopicInference,
				partition: 0,
				messages:  make(chan *sarama.ConsumerMessage, 2),
			}
			feedMessages(claim, bad, good)

			err := inferenceRunner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(2),
				"the partition must keep consuming after a dead-lettered inference record")

			inferenceRunner.mu.Lock()
			defer inferenceRunner.mu.Unlock()
			offset := inferenceRunner.offsets[topicPartition{Topic: kafka.TopicInference, Partition: 0}]
			Expect(offset).To(Equal(int64(1)), "the dead-lettered record must be acknowledged")
		})
	})

	Describe("flush", func() {
		It("commits offsets on successful flush", func() {
			// Simulate processed messages by setting tracked offsets
			runner.mu.Lock()
			runner.offsets[topicPartition{Topic: testTopic, Partition: 0}] = 5
			runner.mu.Unlock()

			err := runner.flush(context.Background())
			Expect(err).NotTo(HaveOccurred())

			session.mu.Lock()
			defer session.mu.Unlock()
			Expect(session.marks).To(HaveLen(1))
			Expect(session.marks[0]).To(Equal(markEntry{
				topic: testTopic, partition: 0, offset: 6, // offset+1
			}))
			Expect(session.committed).To(Equal(1))
		})

		It("does not commit offsets when flush fails", func() {
			adapter.flushErr = errors.New("provider unavailable")
			runner.mu.Lock()
			runner.offsets[topicPartition{Topic: testTopic, Partition: 0}] = 5
			runner.mu.Unlock()

			err := runner.flush(context.Background())
			Expect(err).To(HaveOccurred())

			session.mu.Lock()
			defer session.mu.Unlock()
			Expect(session.marks).To(BeEmpty())
			Expect(session.committed).To(Equal(0))

			val := testutil.ToFloat64(runner.metrics.flushErrors.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})

		It("clears tracked offsets after successful flush", func() {
			runner.mu.Lock()
			runner.offsets[topicPartition{Topic: testTopic, Partition: 0}] = 5
			runner.mu.Unlock()

			err := runner.flush(context.Background())
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			Expect(runner.offsets).To(BeEmpty())
		})

		It("records flush duration in histogram", func() {
			err := runner.flush(context.Background())
			Expect(err).NotTo(HaveOccurred())

			count := testutil.CollectAndCount(runner.metrics.flushDuration)
			Expect(count).To(Equal(1))
		})

		It("preserves offsets when no session is active", func() {
			// Simulate session ending (rebalance)
			_ = runner.Cleanup(session)

			runner.mu.Lock()
			runner.offsets[topicPartition{Topic: testTopic, Partition: 0}] = 5
			runner.mu.Unlock()

			err := runner.flush(context.Background())
			Expect(err).NotTo(HaveOccurred())

			// Offsets should be retained for the next session
			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(5)))
		})
	})

	Describe("MetricsHandler", func() {
		It("returns a non-nil HTTP handler", func() {
			handler := runner.MetricsHandler()
			Expect(handler).NotTo(BeNil())
		})
	})

	Describe("Setup and Cleanup", func() {
		It("stores and clears the session", func() {
			newSession := &mockSession{ctx: context.Background()}
			err := runner.Setup(newSession)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			Expect(runner.session).To(Equal(newSession))
			runner.mu.Unlock()

			err = runner.Cleanup(newSession)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			Expect(runner.session).To(BeNil())
			runner.mu.Unlock()
		})
	})

	Describe("end-to-end: ConsumeClaim → flush → commit", func() {
		It("processes messages and commits offsets on flush", func() {
			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 0),
				newTestMessage("evt-2", "res-2", 1),
				newTestMessage("evt-3", "res-3", 2),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			err = runner.flush(context.Background())
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			Expect(adapter.submitCalls).To(HaveLen(3))
			Expect(adapter.flushCalls).To(Equal(1))
			adapter.mu.Unlock()

			session.mu.Lock()
			defer session.mu.Unlock()
			Expect(session.marks).To(HaveLen(1))
			Expect(session.marks[0].offset).To(Equal(int64(3))) // highest offset (2) + 1
			Expect(session.committed).To(Equal(1))
		})
	})
})

var _ = Describe("Runner DLQ integration", func() {
	var (
		adapter   *mockAdapter
		dlqSender *mockDLQSender
		runner    *Runner
		session   *mockSession
		claim     *mockClaim
	)

	BeforeEach(func() {
		adapter = &mockAdapter{name: "test-provider"}
		dlqSender = &mockDLQSender{}
		runner = newRunnerWithDLQ(adapter, dlqSender)
		session = &mockSession{ctx: context.Background()}
		claim = &mockClaim{
			topic:     testTopic,
			partition: 0,
			messages:  make(chan *sarama.ConsumerMessage, 10),
		}
		_ = runner.Setup(session)
	})

	Describe("non-retryable errors", func() {
		It("routes to DLQ with correct metadata", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad schema")}
			feedMessages(claim, newTestMessage("evt-1", "res-1", 5))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			dlqSender.mu.Lock()
			defer dlqSender.mu.Unlock()
			Expect(dlqSender.sends).To(HaveLen(1))
			Expect(dlqSender.sends[0].reason).To(ContainSubstring("bad schema"))
			Expect(dlqSender.sends[0].attempts).To(Equal(1))
		})

		It("tracks offset after DLQ send", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			feedMessages(claim, newTestMessage("evt-1", "res-1", 7))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(7)))
		})

		It("increments dlq_events_total without changing occupancy-based dlq_depth", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			feedMessages(claim, newTestMessage("evt-1", "res-1", 0))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(runner.metrics.dlqEventsTotal.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
			depth := testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues(testTopic))
			Expect(depth).To(Equal(float64(0)))
		})
	})

	Describe("empty CloudEvent id", func() {
		It("dead-letters the event with a reason and keeps consuming", func() {
			feedMessages(claim,
				newTestMessage("", "res-1", 0), // empty CloudEvent ID
				newTestMessage("evt-2", "res-2", 1),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			dlqSender.mu.Lock()
			defer dlqSender.mu.Unlock()
			Expect(dlqSender.sends).To(HaveLen(1))
			Expect(dlqSender.sends[0].reason).To(ContainSubstring("empty event id"))
			Expect(dlqSender.sends[0].attempts).To(Equal(1))

			// The well-formed event after the empty-ID one is submitted, not
			// suppressed as a duplicate of it.
			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(1))
			Expect(adapter.submitCalls[0].CloudEvent.ID()).To(Equal("evt-2"))

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(1)), "the dead-lettered offset must be tracked")

			val := testutil.ToFloat64(runner.metrics.eventsFailed.WithLabelValues("test-provider", "non_retryable"))
			Expect(val).To(Equal(float64(1)))
		})
	})

	Describe("exhausted retries", func() {
		It("routes to DLQ after all retries exhausted", func() {
			adapter.submitErr = errors.New("always fails")
			feedMessages(claim, newTestMessage("evt-1", "res-1", 10))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			dlqSender.mu.Lock()
			defer dlqSender.mu.Unlock()
			Expect(dlqSender.sends).To(HaveLen(1))
			Expect(dlqSender.sends[0].reason).To(ContainSubstring("always fails"))
			Expect(dlqSender.sends[0].attempts).To(Equal(3)) // maxRetries=3
		})
	})

	Describe("deserialization errors", func() {
		It("routes raw bytes to DLQ", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     testTopic,
				Partition: 0,
				Offset:    42,
				Value:     []byte("not valid json{{{"),
			}
			feedMessages(claim, msg)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			dlqSender.mu.Lock()
			defer dlqSender.mu.Unlock()
			Expect(dlqSender.sends).To(HaveLen(1))
			Expect(dlqSender.sends[0].reason).To(Equal("deserialization"))
			Expect(dlqSender.sends[0].attempts).To(Equal(1))
			Expect(string(dlqSender.sends[0].msg.Value)).To(Equal("not valid json{{{"))
		})

		It("stops partition claim when DLQ send fails for deserialization errors", func() {
			dlqSender.sendErr = errors.New("DLQ broker down")
			msg := &sarama.ConsumerMessage{
				Topic:     testTopic,
				Partition: 0,
				Offset:    42,
				Value:     []byte("not valid json{{{"),
			}
			feedMessages(claim, msg, newTestMessage("evt-2", "res-2", 43))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			_, tracked := runner.offsets[tp]
			Expect(tracked).To(BeFalse(), "offset must not be tracked when DLQ send fails")
		})

		It("tracks offset after DLQ send for deserialization errors", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     testTopic,
				Partition: 0,
				Offset:    99,
				Value:     []byte("bad"),
			}
			feedMessages(claim, msg)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			Expect(runner.offsets[tp]).To(Equal(int64(99)))
		})
	})

	Describe("DLQ send failures", func() {
		It("stops partition claim and does not track offset when DLQ send fails", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			dlqSender.sendErr = errors.New("DLQ broker down")

			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 5),
				newTestMessage("evt-2", "res-2", 6),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			runner.mu.Lock()
			defer runner.mu.Unlock()
			tp := topicPartition{Topic: testTopic, Partition: 0}
			_, tracked := runner.offsets[tp]
			Expect(tracked).To(BeFalse(), "offset must not be tracked when DLQ send fails")
		})

		It("does not process subsequent messages after DLQ send failure", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			dlqSender.sendErr = errors.New("DLQ broker down")

			feedMessages(claim,
				newTestMessage("evt-1", "res-1", 5),
				newTestMessage("evt-2", "res-2", 6),
			)

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.submitCalls).To(HaveLen(1), "second message must not be processed")
		})

		It("increments dlq_send_errors metric", func() {
			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			dlqSender.sendErr = errors.New("DLQ broker down")

			feedMessages(claim, newTestMessage("evt-1", "res-1", 0))

			err := runner.ConsumeClaim(session, claim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(runner.metrics.dlqSendErrors.WithLabelValues("test-provider"))
			Expect(val).To(Equal(float64(1)))
		})
	})

	Describe("backward compatibility", func() {
		It("still works without DLQ (nil sender)", func() {
			noDLQRunner := newRunner(adapter)
			_ = noDLQRunner.Setup(session)
			noDLQClaim := &mockClaim{
				topic:     testTopic,
				partition: 0,
				messages:  make(chan *sarama.ConsumerMessage, 10),
			}

			adapter.submitErr = &adapters.NonRetryableError{Err: errors.New("bad")}
			feedMessages(noDLQClaim, newTestMessage("evt-1", "res-1", 0))

			err := noDLQRunner.ConsumeClaim(session, noDLQClaim)
			Expect(err).NotTo(HaveOccurred())

			val := testutil.ToFloat64(noDLQRunner.metrics.eventsFailed.WithLabelValues("test-provider", "non_retryable"))
			Expect(val).To(Equal(float64(1)))

			dropped := testutil.ToFloat64(noDLQRunner.metrics.eventsDropped.WithLabelValues("test-provider"))
			Expect(dropped).To(Equal(float64(1)))
		})
	})
})

var _ = Describe("Runner DLQ occupancy metrics", func() {
	var (
		adapter *mockAdapter
		runner  *Runner
		occ     *mockDLQOccupier
	)

	BeforeEach(func() {
		adapter = &mockAdapter{name: "test-provider"}
		occ = &mockDLQOccupier{occupancy: 42}
		runner = NewRunner(adapter, RunnerConfig{
			TopicsProvider: func(context.Context) ([]string, error) {
				return []string{testTopic}, nil
			},
		}, logr.Discard(), WithDLQ(occ))
	})

	It("sets dlq_depth from topic occupancy", func() {
		runner.updateDLQDepthMetrics(occ)
		depth := testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues(testTopic))
		Expect(depth).To(Equal(float64(42)))
	})

	It("labels dlq_depth by topic, not provider", func() {
		occ.topic = "custom.dlq"
		runner.updateDLQDepthMetrics(occ)

		Expect(testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues("custom.dlq"))).To(Equal(float64(42)))
		Expect(testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues("test-provider"))).To(Equal(float64(0)))
	})

	It("keeps the last dlq_depth when occupancy scrape fails", func() {
		runner.updateDLQDepthMetrics(occ)
		occ.occupancyErr = errors.New("broker timeout")
		occ.occupancy = 0

		runner.updateDLQDepthMetrics(occ)

		depth := testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues(testTopic))
		Expect(depth).To(Equal(float64(42)))
	})

	It("scrapes occupancy immediately then returns when the context is already cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		runner.dlqDepthMetricsLoop(ctx, occ)

		depth := testutil.ToFloat64(runner.metrics.dlqDepth.WithLabelValues(testTopic))
		Expect(depth).To(Equal(float64(42)))
	})
})

// mockConsumerGroup records the topics of every consume session and ends each session when
// its context does, like a real group with no rebalances.
type mockConsumerGroup struct {
	mu       sync.Mutex
	sessions [][]string
}

func (g *mockConsumerGroup) Consume(
	ctx context.Context, topics []string, _ sarama.ConsumerGroupHandler,
) error {
	g.mu.Lock()
	g.sessions = append(g.sessions, append([]string(nil), topics...))
	g.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (g *mockConsumerGroup) Errors() <-chan error      { return make(chan error) }
func (g *mockConsumerGroup) Close() error              { return nil }
func (g *mockConsumerGroup) Pause(map[string][]int32)  {}
func (g *mockConsumerGroup) Resume(map[string][]int32) {}
func (g *mockConsumerGroup) PauseAll()                 {}
func (g *mockConsumerGroup) ResumeAll()                {}

func (g *mockConsumerGroup) sessionTopics() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.sessions...)
}

var _ = Describe("Runner session restart", func() {
	It("restarts the consume session so a topic created after it started is consumed", func() {
		discoverCalls := 0
		provider := func(context.Context) ([]string, error) {
			discoverCalls++
			if discoverCalls == 1 {
				return []string{"osac.metering.heartbeat.tenant-a"}, nil
			}
			return []string{
				"osac.metering.heartbeat.tenant-a", "osac.metering.heartbeat.tenant-b",
			}, nil
		}
		group := &mockConsumerGroup{}
		adapter := &mockAdapter{name: "test-provider"}
		r := NewRunner(adapter, RunnerConfig{
			TopicsProvider:  provider,
			SessionInterval: 50 * time.Millisecond,
			FlushInterval:   time.Hour,
			DedupTTL:        time.Hour,
			MaxRetries:      1,
		}, logr.Discard())

		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- r.Run(ctx, group) }()

		Eventually(func() int {
			return len(group.sessionTopics())
		}, "5s", "10ms").Should(BeNumerically(">=", 2))

		sessions := group.sessionTopics()
		Expect(sessions[0]).To(ConsistOf("osac.metering.heartbeat.tenant-a"))
		Expect(sessions[1]).To(ConsistOf(
			"osac.metering.heartbeat.tenant-a", "osac.metering.heartbeat.tenant-b",
		), "a session that started before the topic existed must not pick it up; the next one must")

		cancel()
		Eventually(runDone, "5s").Should(Receive(BeNil()))
		Expect(adapter.closed).To(BeTrue())
	})
})

var _ = Describe("Runner interrupt safety", func() {
	It("stops the claim and keeps the offset when a submit is interrupted", func() {
		adapter := &mockAdapter{name: "test-provider"}
		runner := newRunner(adapter)
		sessionCtx, cancel := context.WithCancel(context.Background())
		session := &mockSession{ctx: sessionCtx}
		claim := &mockClaim{
			topic:     testTopic,
			partition: 0,
			messages:  make(chan *sarama.ConsumerMessage, 10),
		}
		_ = runner.Setup(session)

		// The provider is down and the session ends mid-backoff: the first submit
		// fails retryably and cancels the session the way a timeout would.
		adapter.submitFn = func(_ adapters.MeteringEvent) error {
			cancel()
			return errors.New("provider unavailable")
		}
		feedMessages(claim,
			newTestMessage("evt-1", "res-1", 0),
			newTestMessage("evt-2", "res-2", 1),
		)

		err := runner.ConsumeClaim(session, claim)
		Expect(err).NotTo(HaveOccurred())

		// The failed message's offset is not tracked, so the commit cannot skip it
		// and the next session redelivers it.
		runner.mu.Lock()
		tp := topicPartition{Topic: testTopic, Partition: 0}
		_, tracked := runner.offsets[tp]
		runner.mu.Unlock()
		Expect(tracked).To(BeFalse())

		// The message after the failure was never consumed.
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		Expect(adapter.submitCalls).To(HaveLen(1))

		// The interruption is visible, not silent.
		val := testutil.ToFloat64(
			runner.metrics.eventsFailed.WithLabelValues("test-provider", "retries_interrupted"),
		)
		Expect(val).To(Equal(float64(1)))
	})
})
