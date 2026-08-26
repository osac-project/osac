/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DLQProducer", func() {
	var (
		mockProducer *mocks.SyncProducer
		dlq          *DLQProducer
	)

	BeforeEach(func() {
		mockProducer = mocks.NewSyncProducer(GinkgoT(), nil)
		dlq = NewDLQProducerFromSyncProducer(mockProducer, TopicDLQ)
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
				Topic:     TopicLifecycle,
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
				Topic:     TopicLifecycle,
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
			Expect(sentMsg.Topic).To(Equal(TopicDLQ))

			headers := headerMap(sentMsg.Headers)
			Expect(headers["original-topic"]).To(Equal(TopicLifecycle))
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
				Topic:     TopicLifecycle,
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
				Topic:     TopicLifecycle,
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
				Topic:     TopicLifecycle,
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
				Topic:     TopicLifecycle,
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
				Topic:     TopicLifecycle,
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

	Describe("DLQSender interface", func() {
		It("is implemented by DLQProducer", func() {
			var sender DLQSender = dlq
			Expect(sender).NotTo(BeNil())
		})
	})
})

var _ = Describe("DLQOptionFromEnv", func() {
	It("returns a no-op when DLQ_ENABLED is unset", func() {
		GinkgoT().Setenv("DLQ_ENABLED", "")
		opt, closeFn, err := DLQOptionFromEnv("localhost:9092", KafkaConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(opt).To(BeNil())
		Expect(closeFn).NotTo(BeNil())
		Expect(closeFn()).To(Succeed())
	})

	It("returns a no-op when DLQ_ENABLED is not true", func() {
		GinkgoT().Setenv("DLQ_ENABLED", "false")
		opt, closeFn, err := DLQOptionFromEnv("localhost:9092", KafkaConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(opt).To(BeNil())
		Expect(closeFn()).To(Succeed())
	})

	It("returns a no-op for common falsy values", func() {
		GinkgoT().Setenv("DLQ_ENABLED", "0")
		opt, _, err := DLQOptionFromEnv("localhost:9092", KafkaConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(opt).To(BeNil())
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
			topic: TopicDLQ,
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
			topic: TopicDLQ,
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
			topic: TopicDLQ,
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
		dlq := NewDLQProducerFromSyncProducer(nil, TopicDLQ)
		_, err := dlq.Occupancy()
		Expect(err).To(MatchError(ContainSubstring("Kafka client")))
	})

	It("returns an error when partition listing fails", func() {
		dlq := &DLQProducer{
			topic: TopicDLQ,
			offsets: &fakeOffsets{
				partitionsErr: errors.New("broker down"),
			},
		}

		_, err := dlq.Occupancy()
		Expect(err).To(MatchError(ContainSubstring("listing DLQ partitions")))
	})

	It("returns an error when GetOffset fails and does not report a partial total", func() {
		dlq := &DLQProducer{
			topic: TopicDLQ,
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
})

func headerMap(headers []sarama.RecordHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[string(h.Key)] = string(h.Value)
	}
	return m
}

var _ = Describe("newProducerConfig", func() {
	It("creates an idempotent producer config", func() {
		cfg := KafkaConfig{TLSEnabled: false}
		sc, err := newProducerConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Producer.Idempotent).To(BeTrue())
		Expect(sc.Producer.RequiredAcks).To(Equal(sarama.WaitForAll))
		Expect(sc.Producer.Return.Successes).To(BeTrue())
		Expect(sc.Net.MaxOpenRequests).To(Equal(1))
	})

	It("enables TLS when configured", func() {
		cfg := KafkaConfig{TLSEnabled: true}
		sc, err := newProducerConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Net.TLS.Enable).To(BeTrue())
	})
})

var _ DLQSender = (*DLQProducer)(nil)
var _ DLQOccupier = (*DLQProducer)(nil)
