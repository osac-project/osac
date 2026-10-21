/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
)

const (
	dlqTestTopic    = "osac.metering.dlq.test"
	sourceTestTopic = "osac.metering.heartbeat.test"
)

var _ = Describe("DLQProducer", func() {
	var (
		mockProducer *mocks.SyncProducer
		dlq          *DLQProducer
	)

	BeforeEach(func() {
		mockProducer = mocks.NewSyncProducer(GinkgoT(), nil)
		dlq = NewDLQProducerFromSyncProducer(mockProducer, dlqTestTopic)
	})

	AfterEach(func() {
		if err := mockProducer.Close(); err != nil {
			GinkgoT().Logf("mock producer close: %v", err)
		}
	})

	Describe("Send", func() {
		It("preserves original message value bytes", func() {
			originalValue := []byte(`{"specversion":"1.0","id":"evt-1","type":"test"}`)
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 2,
				Offset:    42,
				Value:     originalValue,
				Key:       []byte("resource-123"),
			}

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			err := dlq.Send(msg, "bad schema", 1)
			Expect(err).NotTo(HaveOccurred())

			Expect(sentMsg).NotTo(BeNil())
			value, encErr := sentMsg.Value.Encode()
			Expect(encErr).NotTo(HaveOccurred())
			Expect(value).To(Equal(originalValue))
		})

		It("includes correct failure metadata headers", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 3,
				Offset:    100,
				Value:     []byte(`{}`),
				Key:       []byte("res-1"),
			}

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			err := dlq.Send(msg, "retries exhausted", 10)
			Expect(err).NotTo(HaveOccurred())

			Expect(sentMsg).NotTo(BeNil())
			Expect(sentMsg.Topic).To(Equal(dlqTestTopic))

			headers := headerMap(sentMsg.Headers)
			Expect(headers["original-topic"]).To(Equal(sourceTestTopic))
			Expect(headers["original-offset"]).To(Equal("100"))
			Expect(headers["original-partition"]).To(Equal("3"))
			Expect(headers["failure-reason"]).To(Equal("retries exhausted"))
			Expect(headers["failure-count"]).To(Equal("10"))

			failedAt, err := time.Parse(time.RFC3339, headers["failed-at"])
			Expect(err).NotTo(HaveOccurred())
			Expect(failedAt).To(BeTemporally("~", time.Now().UTC(), 2*time.Second))
		})

		It("preserves original message key", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
				Key:       []byte("my-resource-key"),
			}

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			err := dlq.Send(msg, "test", 1)
			Expect(err).NotTo(HaveOccurred())

			key, keyErr := sentMsg.Key.Encode()
			Expect(keyErr).NotTo(HaveOccurred())
			Expect(string(key)).To(Equal("my-resource-key"))
		})

		It("handles nil key gracefully", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
			}

			mockProducer.ExpectSendMessageAndSucceed()

			err := dlq.Send(msg, "test", 1)
			Expect(err).NotTo(HaveOccurred())
		})

		It("truncates long failure-reason header", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
			}

			longReason := strings.Repeat("x", maxReasonLen+500)

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			err := dlq.Send(msg, longReason, 1)
			Expect(err).NotTo(HaveOccurred())

			headers := headerMap(sentMsg.Headers)
			Expect(len([]rune(headers["failure-reason"]))).To(Equal(maxReasonLen))
		})

		It("truncates at UTF-8 rune boundary", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
			}

			reasonWithUTF8 := strings.Repeat("a", maxReasonLen-1) + "🎉" + "x"

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			err := dlq.Send(msg, reasonWithUTF8, 1)
			Expect(err).NotTo(HaveOccurred())

			headers := headerMap(sentMsg.Headers)
			reason := headers["failure-reason"]
			_, err = json.Marshal(reason)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error when producer send fails", func() {
			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
			}

			mockProducer.ExpectSendMessageAndFail(errors.New("broker unavailable"))

			err := dlq.Send(msg, "test", 1)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("broker unavailable"))
		})
	})

	Describe("per-tenant routing", func() {
		It("routes a rejected inference record to the inference dead-letter topic, not the source", func() {
			perTenant := &DLQProducer{producer: mockProducer, perTenant: true}

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			msg := &sarama.ConsumerMessage{
				Topic:     kafka.TopicInference,
				Partition: 1,
				Offset:    7,
				Value:     []byte(`{"specversion":"1.0","id":"inf-1","type":"osac.inference.usage.v1"}`),
			}
			Expect(perTenant.Send(msg, "provider rejected", 3)).NotTo(HaveOccurred())

			Expect(sentMsg).NotTo(BeNil())
			Expect(sentMsg.Topic).To(Equal(kafka.InferenceDeadLetter))
			Expect(sentMsg.Topic).NotTo(Equal(kafka.TopicInference),
				"a dead letter must never go back to the consumed source topic")
		})

		It("keeps routing heartbeat records to the tenant's dead-letter topic", func() {
			perTenant := &DLQProducer{producer: mockProducer, perTenant: true}

			var sentMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(pm *sarama.ProducerMessage) error {
					sentMsg = pm
					return nil
				},
			)

			msg := &sarama.ConsumerMessage{
				Topic:     sourceTestTopic,
				Partition: 0,
				Offset:    0,
				Value:     []byte(`{}`),
			}
			Expect(perTenant.Send(msg, "test", 1)).NotTo(HaveOccurred())

			Expect(sentMsg).NotTo(BeNil())
			Expect(sentMsg.Topic).To(Equal("osac.metering.dlq.test"))
		})
	})

	Describe("DLQSender interface", func() {
		It("is implemented by DLQProducer", func() {
			var sender DLQSender = dlq
			Expect(sender).NotTo(BeNil())
		})
	})
})

type fakeOffsets struct {
	partitions    []int32
	newest        map[int32]int64
	oldest        map[int32]int64
	partitionsErr error
	offsetErr     error
}

func (f *fakeOffsets) Partitions(string) ([]int32, error) {
	if f.partitionsErr != nil {
		return nil, f.partitionsErr
	}
	return f.partitions, nil
}

func (f *fakeOffsets) GetOffset(_ string, partitionID int32, time int64) (int64, error) {
	if f.offsetErr != nil {
		return 0, f.offsetErr
	}
	switch time {
	case sarama.OffsetNewest:
		return f.newest[partitionID], nil
	case sarama.OffsetOldest:
		return f.oldest[partitionID], nil
	default:
		return 0, errors.New("unexpected offset time")
	}
}

var _ = Describe("DLQProducer Occupancy", func() {
	It("sums newest minus oldest across partitions", func() {
		dlq := &DLQProducer{
			topic: dlqTestTopic,
			offsets: &fakeOffsets{
				partitions: []int32{0, 1, 2},
				newest:     map[int32]int64{0: 10, 1: 5, 2: 20},
				oldest:     map[int32]int64{0: 3, 1: 5, 2: 12},
			},
		}

		n, err := dlq.Occupancy()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(15))) // (10-3) + (5-5) + (20-12)
	})

	It("treats empty partitions as zero", func() {
		dlq := &DLQProducer{
			topic: dlqTestTopic,
			offsets: &fakeOffsets{
				partitions: []int32{0},
				newest:     map[int32]int64{0: 0},
				oldest:     map[int32]int64{0: 0},
			},
		}

		n, err := dlq.Occupancy()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(0)))
	})

	It("clamps inverted offsets to zero for that partition", func() {
		dlq := &DLQProducer{
			topic: dlqTestTopic,
			offsets: &fakeOffsets{
				partitions: []int32{0, 1},
				newest:     map[int32]int64{0: 4, 1: 1},
				oldest:     map[int32]int64{0: 8, 1: 0},
			},
		}

		n, err := dlq.Occupancy()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
	})

	It("returns an error when no Kafka client is attached", func() {
		dlq := NewDLQProducerFromSyncProducer(nil, dlqTestTopic)
		_, err := dlq.Occupancy()
		Expect(err).To(MatchError(ContainSubstring("Kafka client")))
	})

	It("returns an error when partition listing fails", func() {
		dlq := &DLQProducer{
			topic: dlqTestTopic,
			offsets: &fakeOffsets{
				partitionsErr: errors.New("broker down"),
			},
		}

		_, err := dlq.Occupancy()
		Expect(err).To(MatchError(ContainSubstring("listing DLQ partitions")))
	})

	It("returns an error when GetOffset fails and does not report a partial total", func() {
		dlq := &DLQProducer{
			topic: dlqTestTopic,
			offsets: &fakeOffsets{
				partitions: []int32{0, 1},
				newest:     map[int32]int64{0: 10},
				oldest:     map[int32]int64{0: 0},
				offsetErr:  errors.New("offset unavailable"),
			},
		}

		n, err := dlq.Occupancy()
		Expect(err).To(HaveOccurred())
		Expect(n).To(Equal(int64(0)))
	})

	It("covers the inference dead-letter topic alongside the tenants' in per-tenant mode", func() {
		client := &fakeOccupancyClient{
			partitions: map[string][]int32{
				"osac.metering.dlq.acme":  {0},
				kafka.InferenceDeadLetter: {0},
			},
			newest: map[string]map[int32]int64{
				"osac.metering.dlq.acme":  {0: 10},
				kafka.InferenceDeadLetter: {0: 4},
			},
			oldest: map[string]map[int32]int64{
				"osac.metering.dlq.acme":  {0: 3},
				kafka.InferenceDeadLetter: {0: 0},
			},
		}
		dlq := &DLQProducer{perTenant: true, client: client, offsets: client}

		n, err := dlq.Occupancy()
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(11))) // (10-3) on the tenant DLQ + (4-0) on the inference DLQ
	})
})

// fakeOccupancyClient stands in for a sarama client that both lists topics and
// serves per-topic offsets, so a per-tenant occupancy can span several DLQs.
type fakeOccupancyClient struct {
	sarama.Client
	partitions map[string][]int32
	newest     map[string]map[int32]int64
	oldest     map[string]map[int32]int64
}

func (c *fakeOccupancyClient) RefreshMetadata(...string) error { return nil }

func (c *fakeOccupancyClient) Topics() ([]string, error) {
	names := make([]string, 0, len(c.partitions))
	for name := range c.partitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (c *fakeOccupancyClient) Partitions(topic string) ([]int32, error) {
	return c.partitions[topic], nil
}

func (c *fakeOccupancyClient) GetOffset(topic string, partitionID int32, offsetTime int64) (int64, error) {
	switch offsetTime {
	case sarama.OffsetNewest:
		return c.newest[topic][partitionID], nil
	case sarama.OffsetOldest:
		return c.oldest[topic][partitionID], nil
	default:
		return 0, errors.New("unexpected offset time")
	}
}

func headerMap(headers []sarama.RecordHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[string(h.Key)] = string(h.Value)
	}
	return m
}

var _ = Describe("newProducerConfig", func() {
	It("creates an idempotent producer config", func() {
		cfg := kafka.KafkaConfig{TLSEnabled: false}
		sc, err := kafka.NewProducerConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Producer.Idempotent).To(BeTrue())
		Expect(sc.Producer.RequiredAcks).To(Equal(sarama.WaitForAll))
		Expect(sc.Producer.Return.Successes).To(BeTrue())
		Expect(sc.Net.MaxOpenRequests).To(Equal(1))
	})

	It("enables TLS when configured", func() {
		cfg := kafka.KafkaConfig{TLSEnabled: true}
		sc, err := kafka.NewProducerConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeTrue())
	})
})

var _ DLQSender = (*DLQProducer)(nil)
var _ DLQOccupier = (*DLQProducer)(nil)
