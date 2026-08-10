/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
)

const (
	defaultFlushInterval = 10 * time.Second
	defaultDedupTTL      = 10 * time.Minute
	defaultMaxRetries    = 10
	shutdownFlushTimeout = 30 * time.Second
	shutdownCloseTimeout = 10 * time.Second
	consumeErrorBackoff  = 5 * time.Second
)

// RunnerConfig configures the adapter Runner.
type RunnerConfig struct {
	Brokers       string
	ConsumerGroup string
	Topics        []string
	FlushInterval time.Duration // default 10s
	DedupTTL      time.Duration // default 10m
	MaxRetries    int           // default 10
	Kafka         KafkaConfig
}

type topicPartition struct {
	Topic     string
	Partition int32
}

// Runner manages the Kafka consumer lifecycle for a ProviderAdapter.
type Runner struct {
	adapter ProviderAdapter
	cfg     RunnerConfig
	logger  logr.Logger
	metrics *adapterMetrics
	dedup   *dedupCache
	order   *orderTracker

	mu      sync.Mutex
	offsets map[topicPartition]int64
	session sarama.ConsumerGroupSession
}

// NewRunner creates a Runner for the given adapter.
func NewRunner(adapter ProviderAdapter, cfg RunnerConfig, logger logr.Logger) *Runner {
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = defaultFlushInterval
	}
	if cfg.DedupTTL == 0 {
		cfg.DedupTTL = defaultDedupTTL
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}

	return &Runner{
		adapter: adapter,
		cfg:     cfg,
		logger:  logger.WithName("adapter-runner").WithValues("provider", adapter.Name()),
		metrics: newAdapterMetrics(),
		dedup:   newDedupCache(cfg.DedupTTL),
		order:   newOrderTracker(cfg.DedupTTL),
		offsets: make(map[topicPartition]int64),
	}
}

// MetricsHandler returns an HTTP handler for Prometheus metrics.
func (r *Runner) MetricsHandler() http.Handler {
	return r.metrics.handler()
}

// Run starts the Kafka consumer group and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	sc, err := newConsumerConfig(r.cfg.Kafka)
	if err != nil {
		return fmt.Errorf("creating consumer config: %w", err)
	}

	brokers := splitAndTrimBrokers(r.cfg.Brokers, ",")
	group, err := sarama.NewConsumerGroup(brokers, r.cfg.ConsumerGroup, sc)
	if err != nil {
		return fmt.Errorf("creating consumer group: %w", err)
	}
	defer func() { _ = group.Close() }()

	go r.dedup.startEviction(ctx)
	go r.order.startEviction(ctx)

	flushDone := make(chan struct{})
	go r.flushLoop(ctx, flushDone)

	r.logger.Info("starting consumer group", "topics", r.cfg.Topics)

	for {
		if err := group.Consume(ctx, r.cfg.Topics, r); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				r.logger.Info("consumer group closed, exiting")
				break
			}
			r.logger.Error(err, "consumer group error, retrying after backoff")
			select {
			case <-ctx.Done():
			case <-time.After(consumeErrorBackoff):
			}
		}
		if ctx.Err() != nil {
			break
		}
	}

	<-flushDone

	r.logger.Info("performing final flush on shutdown")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlushTimeout)
	defer cancel()
	if err := r.flush(shutdownCtx); err != nil {
		r.logger.Error(err, "final flush failed")
	}

	closeErr := make(chan error, 1)
	go func() { closeErr <- r.adapter.Close() }()
	select {
	case err := <-closeErr:
		if err != nil {
			r.logger.Error(err, "adapter close failed")
		}
	case <-time.After(shutdownCloseTimeout):
		r.logger.Error(nil, "adapter close timed out", "timeout", shutdownCloseTimeout)
	}

	return nil
}

func (r *Runner) flushLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(r.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.flush(ctx); err != nil {
				r.logger.Error(err, "flush failed")
			}
		}
	}
}

func (r *Runner) flush(ctx context.Context) error {
	start := time.Now()
	_, err := r.adapter.Flush(ctx)
	duration := time.Since(start)

	provider := r.adapter.Name()
	r.metrics.flushDuration.WithLabelValues(provider).Observe(duration.Seconds())

	if err != nil {
		r.metrics.flushErrors.WithLabelValues(provider).Inc()
		return fmt.Errorf("adapter flush: %w", err)
	}

	r.mu.Lock()
	session := r.session
	if session == nil {
		// No active session — retain offsets for the next session to commit.
		r.mu.Unlock()
		return nil
	}
	offsets := make(map[topicPartition]int64, len(r.offsets))
	for tp, o := range r.offsets {
		offsets[tp] = o
	}
	r.offsets = make(map[topicPartition]int64)
	r.mu.Unlock()

	for tp, offset := range offsets {
		session.MarkOffset(tp.Topic, tp.Partition, offset+1, "")
	}
	session.Commit()

	return nil
}

// --- sarama.ConsumerGroupHandler ---

// Setup is called when a new consumer group session starts.
func (r *Runner) Setup(session sarama.ConsumerGroupSession) error {
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()
	r.logger.Info("consumer group session started")
	return nil
}

// Cleanup is called when a consumer group session ends.
func (r *Runner) Cleanup(_ sarama.ConsumerGroupSession) error {
	r.mu.Lock()
	r.session = nil
	r.mu.Unlock()
	r.logger.Info("consumer group session ended")
	return nil
}

// ConsumeClaim processes messages from a partition claim.
func (r *Runner) ConsumeClaim(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	provider := r.adapter.Name()

	for msg := range claim.Messages() {
		r.processMessage(session.Context(), msg, provider)
	}
	return nil
}

func (r *Runner) processMessage(
	ctx context.Context,
	msg *sarama.ConsumerMessage,
	provider string,
) {
	var ce cloudevents.Event
	if err := json.Unmarshal(msg.Value, &ce); err != nil {
		r.logger.Error(err, "failed to deserialize CloudEvent",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
		)
		r.metrics.eventsFailed.WithLabelValues(provider, "deserialization").Inc()
		return
	}

	eventID := ce.ID()

	if r.dedup.contains(eventID) {
		r.metrics.duplicatesSuppressed.WithLabelValues(provider).Inc()
		r.trackOffset(msg)
		return
	}

	r.checkOutOfOrder(ce, provider)

	event := MeteringEvent{
		CloudEvent: ce,
		Topic:      msg.Topic,
		Partition:  msg.Partition,
		Offset:     msg.Offset,
	}

	result := submitWithRetry(ctx, r.adapter, event, r.cfg.MaxRetries, r.logger)

	if result.TotalDuration > 0 && result.Attempts > 1 {
		r.metrics.retryDuration.WithLabelValues(provider).Observe(result.TotalDuration.Seconds())
	}

	if result.Err != nil {
		if result.NonRetryable {
			r.metrics.eventsFailed.WithLabelValues(provider, "non_retryable").Inc()
			r.logger.Error(result.Err, "dropping non-retryable event",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
			)
		} else if result.Exhausted {
			r.metrics.eventsFailed.WithLabelValues(provider, "retries_exhausted").Inc()
			r.logger.Error(result.Err, "dropping event after retries exhausted",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
			)
		} else {
			// Context cancelled (e.g., rebalance interrupted retry backoff).
			// Do NOT track offset — the event will be redelivered to the new
			// partition owner after rebalance completes.
			r.logger.V(1).Info("submit interrupted, event will be redelivered",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
				"error", result.Err,
			)
			return
		}
		// Track the offset for non-retryable and exhausted errors so the
		// consumer does not redeliver the same poison message on every restart.
		r.trackOffset(msg)
		return
	}

	r.dedup.add(eventID)
	r.metrics.eventsSubmitted.WithLabelValues(provider, msg.Topic).Inc()
	r.trackOffset(msg)
}

func (r *Runner) trackOffset(msg *sarama.ConsumerMessage) {
	tp := topicPartition{Topic: msg.Topic, Partition: msg.Partition}
	r.mu.Lock()
	if current, ok := r.offsets[tp]; !ok || msg.Offset > current {
		r.offsets[tp] = msg.Offset
	}
	r.mu.Unlock()
}

func (r *Runner) checkOutOfOrder(ce cloudevents.Event, provider string) {
	resourceID, ok := ce.Extensions()["osacresourceid"]
	if !ok {
		return
	}

	var data map[string]interface{}
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		return
	}

	ttStr, ok := data["transition_time"].(string)
	if !ok {
		return
	}

	tt, err := time.Parse(time.RFC3339, ttStr)
	if err != nil {
		return
	}

	if r.order.check(fmt.Sprintf("%v", resourceID), tt) {
		r.metrics.outOfOrderEvents.WithLabelValues(provider).Inc()
	}
}
