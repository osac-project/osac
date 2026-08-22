/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/adapters"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeKafkaChecker struct {
	err   error
	block chan struct{} // if non-nil, RefreshMetadata blocks until closed
}

func (f *fakeKafkaChecker) RefreshMetadata(_ ...string) error {
	if f.block != nil {
		<-f.block
	}
	return f.err
}

func (f *fakeKafkaChecker) Close() error {
	return nil
}

var _ = Describe("kafkaSubmitter", func() {
	var (
		producer *mocks.SyncProducer
		sub      *kafkaSubmitter
	)

	BeforeEach(func() {
		producer = mocks.NewSyncProducer(GinkgoT(), nil)
		sub = newKafkaSubmitter(
			producer,
			&fakeKafkaChecker{},
			map[string]string{
				"vmaas": "m360.metering.vmaas",
				"caas":  "m360.metering.caas",
				"maas":  "m360.metering.maas",
			},
			logr.Discard(),
		)
	})

	Describe("submit", func() {
		It("sends JSON payload to the correct topic", func() {
			producer.ExpectSendMessageAndSucceed()

			payload := map[string]any{
				"event_id":      "ce-123",
				"resource_type": "compute_instance",
				"resource_id":   "vm-001",
			}
			err := sub.submit(context.Background(), "vmaas", payload)

			Expect(err).NotTo(HaveOccurred())
		})

		It("uses resource_id as message key", func() {
			var capturedMsg *sarama.ProducerMessage
			producer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			payload := map[string]any{
				"event_id":    "ce-456",
				"resource_id": "vm-002",
			}
			err := sub.submit(context.Background(), "vmaas", payload)

			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMsg.Topic).To(Equal("m360.metering.vmaas"))
			key, _ := capturedMsg.Key.Encode()
			Expect(string(key)).To(Equal("vm-002"))
		})

		It("sends valid JSON as message value", func() {
			producer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					val, _ := msg.Value.Encode()
					var decoded map[string]any
					Expect(json.Unmarshal(val, &decoded)).To(Succeed())
					Expect(decoded["event_id"]).To(Equal("ce-789"))
					return nil
				},
			)

			payload := map[string]any{"event_id": "ce-789", "resource_id": "vm-003"}
			err := sub.submit(context.Background(), "vmaas", payload)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns NonRetryableError for unknown route", func() {
			err := sub.submit(context.Background(), "unknown", map[string]any{})

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeTrue())
		})

		It("returns retryable error on send failure", func() {
			producer.ExpectSendMessageAndFail(sarama.ErrBrokerNotAvailable)

			payload := map[string]any{"event_id": "ce-fail", "resource_id": "vm-fail"}
			err := sub.submit(context.Background(), "vmaas", payload)

			Expect(err).To(HaveOccurred())
			var nonRetryable *adapters.NonRetryableError
			Expect(errors.As(err, &nonRetryable)).To(BeFalse())
		})

		It("uses empty key when resource_id is missing", func() {
			producer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					key, _ := msg.Key.Encode()
					Expect(string(key)).To(BeEmpty())
					return nil
				},
			)

			payload := map[string]any{"event_id": "ce-no-rid"}
			err := sub.submit(context.Background(), "vmaas", payload)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("healthCheck", func() {
		It("returns nil when metadata refresh succeeds", func() {
			sub.client = &fakeKafkaChecker{}
			err := sub.healthCheck(context.Background())
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error when metadata refresh fails", func() {
			sub.client = &fakeKafkaChecker{err: errors.New("broker unreachable")}
			err := sub.healthCheck(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("M360 Kafka health check"))
		})

		It("returns error when context is cancelled", func() {
			sub.client = &fakeKafkaChecker{block: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := sub.healthCheck(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("M360 Kafka health check"))
		})
	})

	Describe("close", func() {
		It("closes the producer without error", func() {
			err := sub.close()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
