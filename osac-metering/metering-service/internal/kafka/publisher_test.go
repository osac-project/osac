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
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type bmaasTestMapper struct {
	currentState string
}

func (m *bmaasTestMapper) ResourceType() string { return schema.ResourceTypeBareMetalInstance }
func (m *bmaasTestMapper) ResourceID() string   { return "bmi-001" }
func (m *bmaasTestMapper) TenantID() string     { return "tenant-1" }
func (m *bmaasTestMapper) ProjectID() *string {
	projectID := "project-1"
	return &projectID
}
func (m *bmaasTestMapper) CatalogItemID() *string {
	catalogItemID := "bmi-gpu-workstation"
	return &catalogItemID
}
func (m *bmaasTestMapper) TemplateID() *string {
	templateID := "tmpl-bmaas"
	return &templateID
}
func (m *bmaasTestMapper) CurrentState() string      { return m.currentState }
func (m *bmaasTestMapper) FulfillmentVersion() int32 { return 1 }
func (m *bmaasTestMapper) IsBillable() bool          { return true }
func (m *bmaasTestMapper) BillingDimensionsMap() (map[string]any, error) {
	return map[string]any{
		"bm_instance_type": "bmi-type-gpu-large",
		"catalog_item":     "bmi-gpu-workstation",
	}, nil
}
func (m *bmaasTestMapper) TransitionTime(_ *privatev1.Event, _ string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *bmaasTestMapper) CloudEventType(privatev1.EventType, string) (string, error) {
	return events.EventStarted, nil
}

func capturePublishedMessage(mockProducer *mocks.SyncProducer, pub *kafka.Publisher, ctx context.Context, event cloudevents.Event) *sarama.ProducerMessage {
	var capturedMsg *sarama.ProducerMessage
	mockProducer.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(
		func(msg *sarama.ProducerMessage) error {
			capturedMsg = msg
			return nil
		},
	)

	Expect(pub.Publish(ctx, event)).NotTo(HaveOccurred())
	return capturedMsg
}

func assertPublishedBMaaSEnvelope(msg *sarama.ProducerMessage, topic, eventType, meterType, idSuffix string) {
	Expect(msg.Topic).To(Equal(topic))
	keyBytes, err := msg.Key.Encode()
	Expect(err).NotTo(HaveOccurred())
	Expect(string(keyBytes)).To(Equal("bmi-001"))

	valueBytes, err := msg.Value.Encode()
	Expect(err).NotTo(HaveOccurred())
	var decoded cloudevents.Event
	Expect(json.Unmarshal(valueBytes, &decoded)).To(Succeed())
	Expect(decoded.SpecVersion()).To(Equal("1.0"))
	Expect(decoded.Type()).To(Equal(eventType))
	Expect(decoded.Source()).NotTo(BeEmpty())
	Expect(decoded.Time()).NotTo(BeZero())
	Expect(decoded.ID()).To(ContainSubstring(idSuffix))
	Expect(decoded.Extensions()).To(HaveKeyWithValue(schema.ExtResourceType, schema.ResourceTypeBareMetalInstance))
	Expect(decoded.Extensions()).NotTo(HaveKey("meter_type"))

	var data map[string]any
	Expect(json.Unmarshal(decoded.Data(), &data)).To(Succeed())
	Expect(data["resource_type"]).To(Equal(schema.ResourceTypeBareMetalInstance))
	billingDimensions, ok := data["billing_dimensions"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(billingDimensions).To(HaveKeyWithValue("meter_type", meterType))
	Expect(billingDimensions).To(HaveKeyWithValue("bm_instance_type", "bmi-type-gpu-large"))
}

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
			Expect(capturedMsg.Topic).To(Equal(kafka.TopicLifecycle))
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
			Expect(capturedMsg.Topic).To(Equal(kafka.TopicHeartbeat))
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
			Expect(capturedMsg.Topic).To(Equal(kafka.TopicCorrections))
		})

		It("routes producer-shaped BMaaS events through the existing shared topics", func() {
			mapper := &bmaasTestMapper{currentState: "RUNNING"}
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			allocationSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 30, 0, 0, time.UTC)
			dims, err := mapper.BillingDimensionsMap()
			Expect(err).NotTo(HaveOccurred())

			built, err := events.DecomposeBMIEvents(
				dims,
				"evt-bmaas-1",
				transitionTime,
				events.BMaaSMeterIntervals{
					AllocationSince:  &allocationSince,
					ConsumptionSince: &consumptionSince,
				},
				func(request events.BMaaSEventBuildRequest) (cloudevents.Event, error) {
					return events.BuildLifecycleEvent(
						request.EventID,
						request.EventType,
						mapper,
						request.BillingDims,
						"PROVISIONING",
						request.DurationSeconds,
						transitionTime,
					)
				},
				events.EventStarted,
				events.EventStarted,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(built).To(HaveLen(2))

			for _, event := range built {
				meterType := "allocation"
				if strings.Contains(event.ID(), "/consumption") {
					meterType = "consumption"
				}
				assertPublishedBMaaSEnvelope(
					capturePublishedMessage(mockProducer, pub, ctx, event),
					kafka.TopicLifecycle,
					events.EventStarted,
					meterType,
					"/"+meterType,
				)
			}

			heartbeat := cloudevents.NewEvent()
			heartbeat.SetID("hb/bmi-001/1726315200/allocation")
			heartbeat.SetSource("osac-metering")
			heartbeat.SetType(events.EventHeartbeat)
			heartbeat.SetTime(transitionTime)
			events.SetOSACExtensions(&heartbeat, "bmi-001", schema.ResourceTypeBareMetalInstance, "tenant-1", "project-1")
			Expect(heartbeat.SetData(cloudevents.ApplicationJSON, map[string]any{
				"resource_id": "bmi-001", "resource_type": schema.ResourceTypeBareMetalInstance,
				"tenant_id": "tenant-1", "project_id": "project-1", "current_state": "STOPPED",
				"duration_seconds": 3600.0, "billing_dimensions": map[string]any{
					"meter_type": "allocation", "bm_instance_type": "bmi-type-gpu-large",
				}, "schema_version": schema.SchemaVersion,
			})).To(Succeed())
			assertPublishedBMaaSEnvelope(
				capturePublishedMessage(mockProducer, pub, ctx, heartbeat),
				kafka.TopicHeartbeat,
				events.EventHeartbeat,
				"allocation",
				"/allocation",
			)

			correction := cloudevents.NewEvent()
			correction.SetID("correction/bmi-001/allocation")
			correction.SetSource("osac-metering/reconciler")
			correction.SetType(events.EventCorrection)
			correction.SetTime(transitionTime)
			events.SetOSACExtensions(&correction, "bmi-001", schema.ResourceTypeBareMetalInstance, "tenant-1", "project-1")
			Expect(correction.SetData(cloudevents.ApplicationJSON, map[string]any{
				"resource_id": "bmi-001", "resource_type": schema.ResourceTypeBareMetalInstance,
				"tenant_id": "tenant-1", "project_id": "project-1", "reason": "state_drift",
				"billing_dimensions": map[string]any{
					"meter_type": "allocation", "bm_instance_type": "bmi-type-gpu-large",
				}, "schema_version": schema.SchemaVersion,
			})).To(Succeed())
			assertPublishedBMaaSEnvelope(
				capturePublishedMessage(mockProducer, pub, ctx, correction),
				kafka.TopicCorrections,
				events.EventCorrection,
				"allocation",
				"/allocation",
			)
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
				Expect(topic).To(Equal(kafka.TopicLifecycle),
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
