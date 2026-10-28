/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/osac-project/osac-metering/schema"
)

var (
	eventsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_events_published_total",
		Help: "Total metering events published to Kafka",
	}, []string{"topic", "event_type", "resource_type"})

	publishErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_kafka_publish_errors_total",
		Help: "Total Kafka publish errors",
	}, []string{"topic", "error_type"})

	publishLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "osac_metering_kafka_publish_latency_seconds",
		Help:    "Kafka publish latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"topic"})
)

type EventPublisher interface {
	Publish(ctx context.Context, event cloudevents.Event) error
}

const (
	TopicLifecycle   = "osac.metering.lifecycle"
	TopicHeartbeat   = "osac.metering.heartbeat"
	TopicCorrections = "osac.metering.corrections"
	TopicInference   = "osac.metering.inference"
)

var Topics = []string{TopicLifecycle, TopicHeartbeat, TopicCorrections}

var topicRoutes = map[string]string{
	"osac.resource.created.v1":    TopicLifecycle,
	"osac.resource.started.v1":    TopicLifecycle,
	"osac.resource.updated.v1":    TopicLifecycle,
	"osac.resource.suspended.v1":  TopicLifecycle,
	"osac.resource.resumed.v1":    TopicLifecycle,
	"osac.resource.deleted.v1":    TopicLifecycle,
	"osac.resource.heartbeat.v1":  TopicHeartbeat,
	"osac.resource.correction.v1": TopicCorrections,
	"osac.inference.usage.v1":     TopicInference,
}

type Publisher struct {
	producer sarama.SyncProducer
}

func NewPublisher(producer sarama.SyncProducer) *Publisher {
	return &Publisher{producer: producer}
}

func TopicFor(eventType string) (string, error) {
	topic, ok := topicRoutes[eventType]
	if !ok {
		return "", fmt.Errorf("no topic route for event type %q", eventType)
	}
	return topic, nil
}

// Publish serializes a CloudEvent as JSON and sends it to the appropriate Kafka
// topic. The "osacresourceid" extension is used as the partition key to ensure
// per-resource ordering.
func (p *Publisher) Publish(ctx context.Context, event cloudevents.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish aborted: %w", err)
	}

	topic, err := TopicFor(event.Type())
	if err != nil {
		publishErrors.WithLabelValues("unknown", "routing").Inc()
		return err
	}

	data, err := json.Marshal(event)
	if err != nil {
		publishErrors.WithLabelValues(topic, "serialization").Inc()
		return fmt.Errorf("serializing CloudEvent: %w", err)
	}

	extensions := event.Extensions()
	resourceID, ok := extensions[schema.ExtResourceID]
	if !ok {
		publishErrors.WithLabelValues(topic, "missing_key").Inc()
		return fmt.Errorf("missing %s extension", schema.ExtResourceID)
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(fmt.Sprintf("%v", resourceID)),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("ce_specversion"), Value: []byte(event.SpecVersion())},
			{Key: []byte("ce_type"), Value: []byte(event.Type())},
			{Key: []byte("ce_source"), Value: []byte(event.Source())},
			{Key: []byte("ce_id"), Value: []byte(event.ID())},
		},
	}

	start := time.Now()
	_, _, err = p.producer.SendMessage(msg)
	publishLatency.WithLabelValues(topic).Observe(time.Since(start).Seconds())

	if err != nil {
		publishErrors.WithLabelValues(topic, "send").Inc()
		return fmt.Errorf("publishing to Kafka topic %s: %w", topic, err)
	}

	resourceType := ""
	if rt, ok := extensions[schema.ExtResourceType]; ok {
		resourceType = fmt.Sprintf("%v", rt)
	}
	eventsPublished.WithLabelValues(topic, event.Type(), resourceType).Inc()

	return nil
}

func (p *Publisher) Close() error {
	return p.producer.Close()
}

// NewProducerConfig returns a sarama configuration suitable for the metering
// producer: idempotent, acks=all, synchronous.
func NewProducerConfig() *sarama.Config {
	config := sarama.NewConfig()
	config.Version = sarama.V3_9_0_0
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Idempotent = true
	config.Producer.Return.Successes = true
	config.Net.MaxOpenRequests = 1
	return config
}
