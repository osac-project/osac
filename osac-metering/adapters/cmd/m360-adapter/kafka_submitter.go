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
	"fmt"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/adapters"
)

// kafkaChecker is satisfied by sarama.Client — used for health checks
// and closed on shutdown.
type kafkaChecker interface {
	RefreshMetadata(topics ...string) error
	Close() error
}

// kafkaSubmitter sends M360 events directly to Kafka topics.
type kafkaSubmitter struct {
	producer sarama.SyncProducer
	client   kafkaChecker
	topics   map[string]string
	logger   logr.Logger
}

// newKafkaSubmitter creates a Kafka submitter that routes events to the
// given topic map. The client is used for health checks and closed on
// shutdown; pass the same sarama.Client used to create the producer via
// sarama.NewSyncProducerFromClient.
func newKafkaSubmitter(
	producer sarama.SyncProducer,
	client kafkaChecker,
	topics map[string]string,
	logger logr.Logger,
) *kafkaSubmitter {
	return &kafkaSubmitter{
		producer: producer,
		client:   client,
		topics:   topics,
		logger:   logger,
	}
}

// submit JSON-encodes the payload and sends it to the Kafka topic mapped
// to the given route key. Uses resource_id as the message key for
// partition affinity. Context is accepted for interface conformance but
// not honoured — sarama.SyncProducer.SendMessage has no context parameter.
func (s *kafkaSubmitter) submit(_ context.Context, route string, payload map[string]any) error {
	topic, ok := s.topics[route]
	if !ok {
		return &adapters.NonRetryableError{
			Err: fmt.Errorf("unknown route %q", route),
		}
	}

	value, err := json.Marshal(payload)
	if err != nil {
		return &adapters.NonRetryableError{
			Err: fmt.Errorf("marshal M360 Kafka payload: %w", err),
		}
	}

	resourceID, _ := payload["resource_id"].(string)

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(resourceID),
		Value: sarama.ByteEncoder(value),
	}

	partition, offset, err := s.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("M360 Kafka send to %s: %w", topic, err)
	}

	s.logger.V(1).Info("M360 Kafka event sent",
		"topic", topic, "partition", partition, "offset", offset)
	return nil
}

// healthCheck verifies broker connectivity via metadata refresh.
// Respects context cancellation so K8s probes don't accumulate
// blocked goroutines when the broker is slow.
func (s *kafkaSubmitter) healthCheck(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		done <- s.client.RefreshMetadata()
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("M360 Kafka health check: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("M360 Kafka health check: %w", ctx.Err())
	}
}

// close shuts down the Kafka producer, then the underlying client.
func (s *kafkaSubmitter) close() error {
	return errors.Join(s.producer.Close(), s.client.Close())
}
