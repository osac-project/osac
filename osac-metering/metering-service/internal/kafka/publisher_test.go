/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/kafka"
)

var _ = Describe("Publisher", func() {
	var (
		mockProducer *mocks.SyncProducer
		pub          *kafka.Publisher
		ctx          context.Context
		testEvent    cloudevents.Event
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockProducer = mocks.NewSyncProducer(GinkgoT(), nil)
		pub = kafka.NewPublisher(mockProducer, "osac.metering.lifecycle")

		testEvent = cloudevents.NewEvent()
		testEvent.SetID("test-id")
		testEvent.SetType("osac.metering.lifecycle.created.v1")
		testEvent.SetSource("osac-metering/test")
		testEvent.SetExtension("osacresourceid", "resource-123")
	})

	Describe("Publish", func() {
		It("publishes CloudEvent to the correct topic", func() {
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMsg).NotTo(BeNil())
			Expect(capturedMsg.Topic).To(Equal("osac.metering.lifecycle"))
		})

		It("uses osacresourceid extension as partition key", func() {
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())

			keyBytes, err := capturedMsg.Key.Encode()
			Expect(err).NotTo(HaveOccurred())
			Expect(string(keyBytes)).To(Equal("resource-123"))
		})

		It("serializes the message value as valid JSON CloudEvent", func() {
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())

			valueBytes, err := capturedMsg.Value.Encode()
			Expect(err).NotTo(HaveOccurred())

			var decoded cloudevents.Event
			err = json.Unmarshal(valueBytes, &decoded)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded.ID()).To(Equal("test-id"))
			Expect(decoded.Type()).To(Equal("osac.metering.lifecycle.created.v1"))
			Expect(decoded.Source()).To(Equal("osac-metering/test"))
		})

		It("returns error when osacresourceid extension is missing", func() {
			event := cloudevents.NewEvent()
			event.SetID("no-resource-id")
			event.SetType("osac.resource.created.v1")
			event.SetSource("osac-metering/test")

			err := pub.Publish(ctx, event)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing osacresourceid"))
		})

		It("includes CloudEvent headers in Kafka message", func() {
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())

			headerMap := make(map[string]string)
			for _, h := range capturedMsg.Headers {
				headerMap[string(h.Key)] = string(h.Value)
			}
			Expect(headerMap).To(HaveKeyWithValue("ce_id", "test-id"))
			Expect(headerMap).To(HaveKeyWithValue("ce_type", "osac.metering.lifecycle.created.v1"))
			Expect(headerMap).To(HaveKeyWithValue("ce_source", "osac-metering/test"))
			Expect(headerMap).To(HaveKeyWithValue("ce_specversion", "1.0"))
		})

		It("returns error when send fails", func() {
			publishErr := fmt.Errorf("kafka connection failed")
			mockProducer.ExpectSendMessageAndFail(publishErr)

			err := pub.Publish(ctx, testEvent)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("kafka connection failed"))
		})
	})

	Describe("Close", func() {
		It("closes the producer", func() {
			err := pub.Close()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("NewProducerConfig", func() {
	It("returns config with idempotent producer settings", func() {
		config := kafka.NewProducerConfig()
		Expect(config.Version).To(Equal(sarama.V3_9_0_0))
		Expect(config.Producer.RequiredAcks).To(Equal(sarama.WaitForAll))
		Expect(config.Producer.Idempotent).To(BeTrue())
		Expect(config.Producer.Return.Successes).To(BeTrue())
		Expect(config.Net.MaxOpenRequests).To(Equal(1))
	})
})
