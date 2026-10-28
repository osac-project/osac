/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

// processMessage handles a single Kafka message. Returns true to continue
// processing, false to stop the partition claim (e.g., DLQ send failure).
func (r *Runner) processMessage(
	ctx context.Context,
	msg *sarama.ConsumerMessage,
	provider string,
) bool {
	var ce cloudevents.Event
	if err := json.Unmarshal(msg.Value, &ce); err != nil {
		r.logger.Error(err, "failed to deserialize CloudEvent",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
		)
		r.metrics.eventsFailed.WithLabelValues(provider, "deserialization").Inc()
		if err := r.sendToDLQ(msg, "deserialization", 1, provider); err != nil {
			return false
		}
		r.trackOffset(msg)
		return true
	}

	eventID := ce.ID()

	// The CloudEvent ID is the dedup key: an event without one can never be
	// deduplicated correctly. Refuse it instead of letting the first empty-ID
	// event poison the dedup cache and silently suppress every later one.
	if eventID == "" {
		r.metrics.eventsFailed.WithLabelValues(provider, "non_retryable").Inc()
		r.logger.Error(nil, "event has an empty CloudEvent ID, dead-lettering",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
		)
		if err := r.sendToDLQ(msg, "empty event id", 1, provider); err != nil {
			return false
		}
		r.trackOffset(msg)
		return true
	}

	if r.dedup.contains(eventID) {
		r.metrics.duplicatesSuppressed.WithLabelValues(provider).Inc()
		r.trackOffset(msg)
		return true
	}

	r.checkOutOfOrder(ce, provider)

	event := adapters.MeteringEvent{
		CloudEvent: ce,
		Topic:      msg.Topic,
		Partition:  msg.Partition,
		Offset:     msg.Offset,
	}

	result := submitWithRetry(ctx, r.adapter, event, r.cfg.MaxRetries, r.logger, r.retries)

	if result.TotalDuration > 0 && result.Attempts > 1 {
		r.metrics.retryDuration.WithLabelValues(provider).Observe(result.TotalDuration.Seconds())
	}

	if result.Err != nil {
		if result.NonRetryable {
			r.metrics.eventsFailed.WithLabelValues(provider, "non_retryable").Inc()
			r.logger.Error(result.Err, "non-retryable error processing event",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
			)
			if err := r.sendToDLQ(msg, result.Err.Error(), result.Attempts, provider); err != nil {
				return false
			}
		} else if result.Exhausted {
			r.metrics.eventsFailed.WithLabelValues(provider, "retries_exhausted").Inc()
			r.logger.Error(result.Err, "retries exhausted processing event",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
			)
			if err := r.sendToDLQ(msg, result.Err.Error(), result.Attempts, provider); err != nil {
				return false
			}
		} else {
			// The session ended mid-backoff: the message was neither delivered nor
			// dead-lettered. Stop the claim so no later message advances the offset
			// past this one; the next session redelivers it.
			r.metrics.eventsFailed.WithLabelValues(provider, "retries_interrupted").Inc()
			r.logger.Error(result.Err, "submit interrupted, stopping the claim; the message is redelivered",
				"event_id", eventID, "topic", msg.Topic,
				"partition", msg.Partition, "offset", msg.Offset,
			)
			return false
		}
		r.trackOffset(msg)
		return true
	}

	r.dedup.add(eventID)
	r.metrics.eventsSubmitted.WithLabelValues(provider, msg.Topic).Inc()
	r.trackOffset(msg)
	return true
}

// sendToDLQ attempts to send a failed message to the dead letter queue.
// Returns nil when the DLQ is not configured (event permanently dropped)
// or when the send succeeds. Returns an error when the DLQ send fails —
// callers must NOT commit the offset so the message is redelivered.
func (r *Runner) sendToDLQ(msg *sarama.ConsumerMessage, reason string, attempts int, provider string) error {
	if r.dlq == nil {
		r.logger.Error(nil, "event permanently dropped, no DLQ configured",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
		)
		r.metrics.eventsDropped.WithLabelValues(provider).Inc()
		return nil
	}
	if err := r.dlq.Send(msg, reason, attempts); err != nil {
		r.logger.Error(err, "failed to send event to DLQ, event will be redelivered",
			"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
		)
		r.metrics.dlqSendErrors.WithLabelValues(provider).Inc()
		return err
	}
	r.metrics.dlqEventsTotal.WithLabelValues(provider).Inc()
	r.metrics.dlqSize.WithLabelValues(provider).Add(float64(len(msg.Value)))
	return nil
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
	resourceID, ok := ce.Extensions()[schema.ExtResourceID]
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
