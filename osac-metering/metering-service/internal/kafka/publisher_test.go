/*
Copyright (c) 2026 Red Hat, Inc.

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
		pub = kafka.NewPublisher(mockProducer)

		testEvent = cloudevents.NewEvent()
		testEvent.SetID("test-id")
		testEvent.SetType("osac.resource.created.v1")
		testEvent.SetSource("osac-metering/test")
		testEvent.SetExtension("osacresourceid", "resource-123")
		testEvent.SetExtension("osacresourcetype", "compute_instance")
	})

	Describe("Publish", func() {
		It("routes lifecycle events to osac.metering.lifecycle topic", func() {
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMsg.Topic).To(Equal("osac.metering.lifecycle"))
		})

		It("routes heartbeat events to osac.metering.heartbeat topic", func() {
			testEvent.SetType("osac.resource.heartbeat.v1")
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMsg.Topic).To(Equal("osac.metering.heartbeat"))
		})

		It("routes correction events to osac.metering.corrections topic", func() {
			testEvent.SetType("osac.resource.correction.v1")
			var capturedMsg *sarama.ProducerMessage
			mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
				func(msg *sarama.ProducerMessage) error {
					capturedMsg = msg
					return nil
				},
			)

			err := pub.Publish(ctx, testEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedMsg.Topic).To(Equal("osac.metering.corrections"))
		})

		It("routes all lifecycle event types to the lifecycle topic", func() {
			lifecycleTypes := []string{
				"osac.resource.created.v1",
				"osac.resource.started.v1",
				"osac.resource.updated.v1",
				"osac.resource.suspended.v1",
				"osac.resource.resumed.v1",
				"osac.resource.deleted.v1",
			}
			for _, eventType := range lifecycleTypes {
				topic, err := kafka.TopicFor(eventType)
				Expect(err).NotTo(HaveOccurred())
				Expect(topic).To(Equal("osac.metering.lifecycle"),
					"event type %s should route to lifecycle topic", eventType)
			}
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
			Expect(decoded.Type()).To(Equal("osac.resource.created.v1"))
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
			Expect(headerMap).To(HaveKeyWithValue("ce_type", "osac.resource.created.v1"))
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

	Describe("TopicFor", func() {
		It("returns error on unknown event type", func() {
			_, err := kafka.TopicFor("osac.resource.unknown.v1")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no topic route"))
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
