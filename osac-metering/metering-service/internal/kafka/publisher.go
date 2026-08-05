/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

type EventPublisher interface {
	Publish(ctx context.Context, event cloudevents.Event) error
}

type Publisher struct {
	producer sarama.SyncProducer
	topic    string
}

func NewPublisher(producer sarama.SyncProducer, topic string) *Publisher {
	return &Publisher{
		producer: producer,
		topic:    topic,
	}
}

// Publish serializes a CloudEvent as JSON and sends it to the configured Kafka topic.
// The osacresourceid extension attribute is used as the partition key to ensure
// per-resource ordering.
func (p *Publisher) Publish(ctx context.Context, event cloudevents.Event) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish aborted: %w", err)
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("serializing CloudEvent: %w", err)
	}

	extensions := event.Extensions()
	resourceID, ok := extensions["osacresourceid"]
	if !ok {
		return fmt.Errorf("missing osacresourceid extension")
	}

	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(fmt.Sprintf("%v", resourceID)),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("ce_specversion"), Value: []byte(event.SpecVersion())},
			{Key: []byte("ce_type"), Value: []byte(event.Type())},
			{Key: []byte("ce_source"), Value: []byte(event.Source())},
			{Key: []byte("ce_id"), Value: []byte(event.ID())},
		},
	}

	_, _, err = p.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("publishing to Kafka: %w", err)
	}

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
