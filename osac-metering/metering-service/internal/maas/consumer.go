/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package maas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/schema"
)

// permanentError wraps errors that will never succeed on retry (malformed
// messages, missing required fields). These get their offset committed so
// they don't block the partition forever.
type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

var (
	inferenceEventsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "osac_metering_inference_events_processed_total",
		Help: "Total inference events consumed from the raw topic and published",
	})

	inferenceIngestErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_inference_ingest_errors_total",
		Help: "Total inference ingest errors by type",
	}, []string{"error_type"})
)

// rawInferenceData matches the CloudEvent data payload from the IPP plugin.
type rawInferenceData struct {
	User                string  `json:"user"`
	Group               string  `json:"group"`
	Subscription        string  `json:"subscription"`
	OrganizationID      string  `json:"organization_id"`
	CostCenter          string  `json:"cost_center"`
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	CachedInputTokens   int     `json:"cached_input_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	ReasoningTokens     int     `json:"reasoning_tokens"`
	DurationMs          float64 `json:"duration_ms"`
	UserAgent           string  `json:"user_agent"`
}

// rawCloudEvent is the minimal CloudEvent envelope fields we need.
type rawCloudEvent struct {
	ID   string           `json:"id"`
	Time string           `json:"time"`
	Data rawInferenceData `json:"data"`
}

// Consumer reads raw inference events from Kafka, resolves tenant
// attribution, and publishes enriched events to the canonical topic.
type Consumer struct {
	consumerGroup sarama.ConsumerGroup
	publisher     kafkapub.EventPublisher
	tenantCache   *TenantCache
	logger        logr.Logger
}

func NewConsumer(
	consumerGroup sarama.ConsumerGroup,
	publisher kafkapub.EventPublisher,
	tenantCache *TenantCache,
	logger logr.Logger,
) *Consumer {
	return &Consumer{
		consumerGroup: consumerGroup,
		publisher:     publisher,
		tenantCache:   tenantCache,
		logger:        logger.WithName("maas-consumer"),
	}
}

func (c *Consumer) Run(ctx context.Context) error {
	handler := &consumerHandler{
		publisher:   c.publisher,
		tenantCache: c.tenantCache,
		logger:      c.logger,
	}
	topics := []string{kafkapub.TopicInferenceRaw}

	for {
		if err := c.consumerGroup.Consume(ctx, topics, handler); err != nil {
			c.logger.Error(err, "consumer group session error, retrying in 5s")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

type consumerHandler struct {
	publisher   kafkapub.EventPublisher
	tenantCache *TenantCache
	logger      logr.Logger
}

const commitInterval = 5 * time.Second

func (h *consumerHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *consumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ticker := time.NewTicker(commitInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				session.Commit()
				return nil
			}
			err := h.processMessage(session.Context(), msg)
			if err == nil {
				session.MarkMessage(msg, "")
				continue
			}

			var permanent permanentError
			if errors.As(err, &permanent) {
				h.logger.Error(err, "permanently failed inference event, skipping",
					"offset", msg.Offset,
					"partition", msg.Partition,
				)
				session.MarkMessage(msg, "")
				continue
			}

			h.logger.Error(err, "transient failure processing inference event, will redeliver",
				"offset", msg.Offset,
				"partition", msg.Partition,
			)
			session.Commit()
			return err

		case <-ticker.C:
			session.Commit()
		}
	}
}

func (h *consumerHandler) processMessage(ctx context.Context, msg *sarama.ConsumerMessage) error {
	var raw rawCloudEvent
	if err := json.Unmarshal(msg.Value, &raw); err != nil {
		inferenceIngestErrors.WithLabelValues("parse_error").Inc()
		return permanentError{fmt.Errorf("parsing CloudEvent: %w", err)}
	}

	if raw.Data.OrganizationID == "" {
		inferenceIngestErrors.WithLabelValues("missing_organization_id").Inc()
		return permanentError{fmt.Errorf("inference event %s has no organization_id", raw.ID)}
	}

	if raw.Data.Model == "" {
		inferenceIngestErrors.WithLabelValues("missing_model").Inc()
		return permanentError{fmt.Errorf("inference event %s has no model", raw.ID)}
	}

	eventTime, err := time.Parse(time.RFC3339, raw.Time)
	if err != nil {
		inferenceIngestErrors.WithLabelValues("invalid_time").Inc()
		return permanentError{fmt.Errorf("inference event %s has invalid time %q: %w", raw.ID, raw.Time, err)}
	}

	tenantName, err := h.tenantCache.Resolve(ctx, raw.Data.OrganizationID)
	if err != nil {
		inferenceIngestErrors.WithLabelValues("tenant_resolution_failed").Inc()
		return fmt.Errorf("resolving tenant for organization %q: %w", raw.Data.OrganizationID, err)
	}

	enrichedEvent := buildInferenceEvent(raw.ID, raw.Data, tenantName, eventTime)

	if err := h.publisher.Publish(ctx, enrichedEvent); err != nil {
		inferenceIngestErrors.WithLabelValues("publish_error").Inc()
		return fmt.Errorf("publishing enriched inference event: %w", err)
	}

	inferenceEventsProcessed.Inc()
	return nil
}

func buildInferenceEvent(rawEventID string, raw rawInferenceData, tenantID string, eventTime time.Time) cloudevents.Event {
	ce := cloudevents.NewEvent()

	ce.SetID(rawEventID)
	ce.SetSource("osac-metering")
	ce.SetType(events.EventInferenceUsage)
	ce.SetTime(eventTime)

	events.SetOSACExtensions(&ce, rawEventID, events.ResourceTypeMaaSInference, tenantID, "")

	data := schema.LifecycleData{
		ResourceID:   rawEventID,
		ResourceType: events.ResourceTypeMaaSInference,
		TenantID:     tenantID,
		BillingDimensions: map[string]any{
			"organization_id":       raw.OrganizationID,
			"cost_center":           raw.CostCenter,
			"subscription":          raw.Subscription,
			"provider":              raw.Provider,
			"model":                 raw.Model,
			"prompt_tokens":         raw.PromptTokens,
			"completion_tokens":     raw.CompletionTokens,
			"total_tokens":          raw.TotalTokens,
			"cached_input_tokens":   raw.CachedInputTokens,
			"cache_creation_tokens": raw.CacheCreationTokens,
			"reasoning_tokens":      raw.ReasoningTokens,
			"duration_ms":           raw.DurationMs,
		},
		SchemaVersion: schema.SchemaVersion,
	}

	_ = ce.SetData(cloudevents.ApplicationJSON, data)
	return ce
}
