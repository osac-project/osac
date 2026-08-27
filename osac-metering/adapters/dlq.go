/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"errors"
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

// DLQOccupier reports how many records are currently retained in a DLQ topic.
type DLQOccupier interface {
	Occupancy() (int64, error)
	Topic() string
}

// kafkaOffsets is the subset of sarama.Client used to scrape DLQ log occupancy.
type kafkaOffsets interface {
	Partitions(topic string) ([]int32, error)
	GetOffset(topic string, partitionID int32, time int64) (int64, error)
}

var _ kafkaOffsets = sarama.Client(nil)

// DLQProducer publishes failed events to a Kafka DLQ topic.
type DLQProducer struct {
	producer sarama.SyncProducer
	client   sarama.Client
	offsets  kafkaOffsets
	topic    string
}

// NewDLQProducer creates a DLQ producer connected to the given brokers.
func NewDLQProducer(brokers string, topic string, kafkaCfg KafkaConfig) (*DLQProducer, error) {
	sc, err := newProducerConfig(kafkaCfg)
	if err != nil {
		return nil, fmt.Errorf("creating DLQ producer config: %w", err)
	}

	addrs := splitAndTrimBrokers(brokers, ",")
	client, err := sarama.NewClient(addrs, sc)
	if err != nil {
		return nil, fmt.Errorf("creating DLQ Kafka client: %w", err)
	}

	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("creating DLQ sync producer: %w", err)
	}

	return &DLQProducer{producer: producer, client: client, offsets: client, topic: topic}, nil
}

// NewDLQProducerFromSyncProducer creates a DLQ producer from an existing
// Sarama SyncProducer. Useful for testing with mock producers.
func NewDLQProducerFromSyncProducer(producer sarama.SyncProducer, topic string) *DLQProducer {
	return &DLQProducer{producer: producer, topic: topic}
}

// Topic returns the DLQ topic this producer writes to.
func (d *DLQProducer) Topic() string {
	return d.topic
}

// Send publishes a failed consumer message to the DLQ topic. The original
// message value is preserved as-is; failure metadata is stored in Kafka
// record headers.
func (d *DLQProducer) Send(msg *sarama.ConsumerMessage, reason string, attempts int) error {
	if len(reason) > maxReasonLen {
		runes := []rune(reason)
		if len(runes) > maxReasonLen {
			reason = string(runes[:maxReasonLen])
		}
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

// Occupancy returns the number of records currently retained in the DLQ topic
// (sum over partitions of OffsetNewest − OffsetOldest). It does not measure
// consumer lag. Test-only producers constructed without a Kafka client return
// an error.
func (d *DLQProducer) Occupancy() (int64, error) {
	if d.offsets == nil {
		return 0, fmt.Errorf("DLQ occupancy requires a Kafka client")
	}

	partitions, err := d.offsets.Partitions(d.topic)
	if err != nil {
		return 0, fmt.Errorf("listing DLQ partitions for %s: %w", d.topic, err)
	}

	var total int64
	for _, p := range partitions {
		newest, err := d.offsets.GetOffset(d.topic, p, sarama.OffsetNewest)
		if err != nil {
			return 0, fmt.Errorf("newest offset for %s/%d: %w", d.topic, p, err)
		}
		oldest, err := d.offsets.GetOffset(d.topic, p, sarama.OffsetOldest)
		if err != nil {
			return 0, fmt.Errorf("oldest offset for %s/%d: %w", d.topic, p, err)
		}
		if newest > oldest {
			total += newest - oldest
		}
	}
	return total, nil
}

// Close shuts down the underlying Kafka producer and client.
func (d *DLQProducer) Close() error {
	var errs []error
	if d.producer != nil {
		errs = append(errs, d.producer.Close())
	}
	if d.client != nil {
		errs = append(errs, d.client.Close())
	}
	return errors.Join(errs...)
}

// DLQOptionFromEnv creates a DLQ RunnerOption from environment variables.
// Returns (nil, noop, nil) unless DLQ_ENABLED is true (case-insensitive).
// Chart deployments set DLQ_ENABLED=true; requiring opt-in keeps image-before-chart
// upgrades on the previous drop-and-continue path until matching KafkaUser ACLs exist.
// The caller must defer the returned close function to release resources.
func DLQOptionFromEnv(brokers string, kafkaCfg KafkaConfig) (RunnerOption, func() error, error) {
	noop := func() error { return nil }
	if !strings.EqualFold(os.Getenv("DLQ_ENABLED"), "true") {
		return nil, noop, nil
	}

	topic := os.Getenv("DLQ_TOPIC")
	if topic == "" {
		topic = TopicDLQ
	}

	dlq, err := NewDLQProducer(brokers, topic, kafkaCfg)
	if err != nil {
		return nil, noop, fmt.Errorf("creating DLQ producer: %w", err)
	}

	return WithDLQ(dlq), dlq.Close, nil
}
