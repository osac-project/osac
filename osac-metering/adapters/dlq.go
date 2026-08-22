/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

const maxReasonLen = 1024

// DLQSender sends failed events to a dead letter queue.
type DLQSender interface {
	Send(msg *sarama.ConsumerMessage, reason string, attempts int) error
	Close() error
}

// DLQProducer publishes failed events to a Kafka DLQ topic.
type DLQProducer struct {
	producer sarama.SyncProducer
	topic    string
}

// NewDLQProducer creates a DLQ producer connected to the given brokers.
func NewDLQProducer(brokers string, topic string, kafkaCfg KafkaConfig) (*DLQProducer, error) {
	sc, err := newProducerConfig(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("creating DLQ producer config: %w", err)
	}

	addrs := splitAndTrimBrokers(brokers, ",")
	producer, err := sarama.NewSyncProducer(addrs, sc)
	if err != nil {
		return nil, fmt.Errorf("creating DLQ sync producer: %w", err)
	}

	return &DLQProducer{producer: producer, topic: topic}, nil
}

// NewDLQProducerFromSyncProducer creates a DLQ producer from an existing
// Sarama SyncProducer. Useful for testing with mock producers.
func NewDLQProducerFromSyncProducer(producer sarama.SyncProducer, topic string) *DLQProducer {
	return &DLQProducer{producer: producer, topic: topic}
}

// Send publishes a failed consumer message to the DLQ topic. The original
// message value is preserved as-is; failure metadata is stored in Kafka
// record headers.
func (d *DLQProducer) Send(msg *sarama.ConsumerMessage, reason string, attempts int) error {
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	headers := []sarama.RecordHeader{
		{Key: []byte("original-topic"), Value: []byte(msg.Topic)},
		{Key: []byte("original-partition"), Value: []byte(strconv.FormatInt(int64(msg.Partition), 10))},
		{Key: []byte("original-offset"), Value: []byte(strconv.FormatInt(msg.Offset, 10))},
		{Key: []byte("failure-reason"), Value: []byte(reason)},
		{Key: []byte("failure-count"), Value: []byte(strconv.Itoa(attempts))},
		{Key: []byte("failed-at"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	}

	pm := &sarama.ProducerMessage{
		Topic:   d.topic,
		Value:   sarama.ByteEncoder(msg.Value),
		Headers: headers,
	}

	if msg.Key != nil {
		pm.Key = sarama.ByteEncoder(msg.Key)
	}

	_, _, err := d.producer.SendMessage(pm)
	if err != nil {
		return fmt.Errorf("sending to DLQ topic %s: %w", d.topic, err)
	}

	return nil
}

// Close shuts down the underlying Kafka producer.
func (d *DLQProducer) Close() error {
	return d.producer.Close()
}

// DLQOptionFromEnv creates a DLQ RunnerOption from environment variables.
// Returns (nil, noop, nil) when DLQ is disabled via DLQ_ENABLED=false.
// The caller must defer the returned close function to release resources.
func DLQOptionFromEnv(brokers string, kafkaCfg KafkaConfig) (RunnerOption, func() error, error) {
	if strings.EqualFold(os.Getenv("DLQ_ENABLED"), "false") {
		return nil, func() error { return nil }, nil
	}

	topic := os.Getenv("DLQ_TOPIC")
	if topic == "" {
		topic = TopicDLQ
	}

	noop := func() error { return nil }
	dlq, err := NewDLQProducer(brokers, topic, kafkaCfg)
	if err != nil {
		return nil, noop, fmt.Errorf("creating DLQ producer: %w", err)
	}

	return WithDLQ(dlq), dlq.Close, nil
}
