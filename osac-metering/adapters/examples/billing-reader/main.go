/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// billing-reader consumes every tenant's heartbeat topics under the reader
// name the grant gave it, drops repeated CloudEvent ids, sums each billable
// quantity per tenant, and prints the totals. It is the worked example from
// the metering event contract, not a production adapter.
//
// It stops with a non-zero exit when it cannot read an event. The unread event
// stays uncommitted, so the reader sees it again on the next run rather than
// dropping it.
//
// The reader lists the heartbeat topics once at startup. A tenant created
// after it starts is read on the next restart, not during this run.
//
// Connection values come from the environment; the repository carries none:
//
//	KAFKA_BROKERS              bootstrap address (required)
//	KAFKA_CONSUMER_GROUP       the reader name in its grant (required)
//	KAFKA_TLS_ENABLED          "false" to disable TLS (default: enabled)
//	KAFKA_TLS_CA_CERT          path to the shared OSAC CA file
//	KAFKA_SASL_USERNAME        the reader name
//	KAFKA_SASL_PASSWORD_FILE   path to the password Strimzi issued
//	READER_START_OFFSET        oldest or newest (default: oldest)
//	READER_IDLE_TIMEOUT        stop after this long without a message
//	                           (default: 30s)
//
// Run it against a dev deployment as the README describes:
//
//	go run ./examples/billing-reader
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/adapters/internal/envutil"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
)

func main() {
	brokers := envutil.RequireEnv("KAFKA_BROKERS")
	group := envutil.RequireEnv("KAFKA_CONSUMER_GROUP")

	idleTimeout, err := time.ParseDuration(envutil.EnvOrDefault("READER_IDLE_TIMEOUT", "30s"))
	if err != nil {
		log.Fatalf("invalid READER_IDLE_TIMEOUT: %v", err)
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)

	config, err := kafka.NewConsumerConfig(kafka.KafkaConfigFromEnv())
	if err != nil {
		log.Fatalf("creating Kafka client config: %v", err)
	}
	// A first run with no committed offset starts where this says. Retention is
	// indefinite by default, so "oldest" bills the whole retained history.
	startOffset, err := initialOffset(envutil.EnvOrDefault("READER_START_OFFSET", "oldest"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	config.Consumer.Offsets.Initial = startOffset
	// Marks are set only after a successful sum, so auto-commit never moves
	// past an event this reader could not read.
	config.Consumer.Offsets.AutoCommit.Enable = true

	client, err := sarama.NewClient(kafka.SplitAndTrimBrokers(brokers, ","), config)
	if err != nil {
		log.Fatalf("connecting to Kafka: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Printf("closing Kafka client: %v", err)
		}
	}()

	topics, err := kafka.HeartbeatTopics(client)
	if err != nil {
		log.Fatalf("listing heartbeat topics: %v", err)
	}
	if len(topics) == 0 {
		log.Fatalf("no osac.metering.heartbeat.* topics exist; check that metering has served a tenant")
	}

	consumerGroup, err := sarama.NewConsumerGroupFromClient(group, client)
	if err != nil {
		log.Fatalf("creating consumer group %s: %v", group, err)
	}
	defer func() {
		if err := consumerGroup.Close(); err != nil {
			logger.Printf("closing consumer group: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	handler := &consumer{
		totals: NewTotals(),
		idle:   idleTimeout,
		cancel: cancel,
		logger: logger,
	}

	// The consumer group reports fetch and authorization failures here when
	// Consumer.Return.Errors is set. Stop the run and remember the first one:
	// a revoked reader fails its next fetch instead of waiting out the idle
	// timeout.
	go func() {
		for err := range consumerGroup.Errors() {
			handler.recordError(err)
			cancel()
		}
	}()

	logger.Printf("reading %d heartbeat topics: %v", len(topics), topics)
	logger.Printf("consuming as group %s; stopping after %s without a message", group, idleTimeout)
	for runCtx.Err() == nil {
		if err := consumerGroup.Consume(runCtx, topics, handler); err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}
			handler.recordError(err)
			break
		}
	}

	// A failed run must not look like a completed bill: print the totals only
	// after a clean stop. The counts go to the log for diagnosis.
	if err := handler.firstError(); err != nil {
		accepted, repeats := handler.totals.Counts()
		logger.Printf("summed %d events, dropped %d repeats before the error", accepted, repeats)
		log.Fatalf("reader stopped: %v", err)
	}

	accepted, repeats := handler.totals.Counts()
	logger.Printf("summed %d events, dropped %d repeats", accepted, repeats)
	if err := handler.totals.Print(os.Stdout); err != nil {
		log.Fatalf("printing totals: %v", err)
	}
}

// initialOffset maps READER_START_OFFSET to the offset a reader with no
// committed position starts from.
func initialOffset(value string) (int64, error) {
	switch strings.ToLower(value) {
	case "oldest":
		return sarama.OffsetOldest, nil
	case "newest":
		return sarama.OffsetNewest, nil
	default:
		return 0, fmt.Errorf("invalid READER_START_OFFSET %q: use oldest or newest", value)
	}
}

// consumer sums each claimed message and stops the run after every claim has
// been quiet for idle. Several claims run at once, one per partition over all
// heartbeat topics, so the idle timer is shared and reset by any message, and
// the totals and first error are mutex-guarded.
type consumer struct {
	totals *Totals
	idle   time.Duration
	cancel context.CancelFunc
	logger *log.Logger

	timerMu sync.Mutex
	timer   *time.Timer

	errMu sync.Mutex
	err   error
}

func (c *consumer) Setup(sarama.ConsumerGroupSession) error {
	c.timerMu.Lock()
	c.timer = time.AfterFunc(c.idle, c.expire)
	c.timerMu.Unlock()
	return nil
}

func (c *consumer) Cleanup(sarama.ConsumerGroupSession) error {
	c.timerMu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timerMu.Unlock()
	return nil
}

func (c *consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			// Mark only after a successful sum. An event this reader cannot
			// decode or does not know stays uncommitted, so a restart sees it
			// again instead of losing it, and the run stops with an error.
			if err := c.sum(message); err != nil {
				c.recordError(err)
				return err
			}
			session.MarkMessage(message, "")
			c.touch()
		case <-session.Context().Done():
			return nil
		}
	}
}

func (c *consumer) touch() {
	c.timerMu.Lock()
	if c.timer != nil {
		c.timer.Reset(c.idle)
	}
	c.timerMu.Unlock()
}

func (c *consumer) expire() {
	c.logger.Printf("no message for %s; printing the billable totals", c.idle)
	c.cancel()
}

func (c *consumer) sum(message *sarama.ConsumerMessage) error {
	var event cloudevents.Event
	if err := event.UnmarshalJSON(message.Value); err != nil {
		return fmt.Errorf("decoding message at offset %d: %w", message.Offset, err)
	}
	if _, err := c.totals.Add(event); err != nil {
		return err
	}
	return nil
}

func (c *consumer) recordError(err error) {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		c.err = err
		c.logger.Printf("consumer error: %v", err)
	}
}

func (c *consumer) firstError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}
