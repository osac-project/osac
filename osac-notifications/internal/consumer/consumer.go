/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/IBM/sarama"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/ingest"
)

const (
	// GroupID is the Kafka consumer group name from the design.
	GroupID = "osac-notifications"

	// DefaultSessionInterval bounds one consume session so new tenant topics are rediscovered.
	DefaultSessionInterval = time.Minute

	discoverBackoff = 5 * time.Second
)

// Handler processes Kafka messages through the notification ingest pipeline.
type Handler struct {
	logger *slog.Logger
	ingest *ingest.Ingest
}

// NewHandler builds a consumer group handler.
func NewHandler(logger *slog.Logger, pipeline *ingest.Ingest) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{logger: logger, ingest: pipeline}
}

// Setup implements sarama.ConsumerGroupHandler.
func (h *Handler) Setup(sarama.ConsumerGroupSession) error { return nil }

// Cleanup implements sarama.ConsumerGroupHandler.
func (h *Handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim processes messages for one partition claim.
func (h *Handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for message := range claim.Messages() {
		if message == nil {
			continue
		}
		if !cevent.TopicMatches(message.Topic) {
			h.logger.InfoContext(session.Context(), "Skipping non-ingest topic",
				slog.String("topic", message.Topic),
			)
			session.MarkMessage(message, "")
			continue
		}
		outcome, err := h.ingest.Handle(session.Context(), message.Topic, message.Value)
		if err != nil {
			// Intent store failed — do not commit; Kafka will redeliver.
			return fmt.Errorf("process topic=%s offset=%d: %w", message.Topic, message.Offset, err)
		}
		_ = outcome
		// Commit after durable intent (or validation reject); drain finishes pending fan-out.
		session.MarkMessage(message, "")
	}
	return nil
}

// TopicClient is the sarama client surface used for ingest topic discovery.
type TopicClient interface {
	RefreshMetadata(topics ...string) error
	Topics() ([]string, error)
}

// DiscoverIngestTopics refreshes metadata then returns ingest topics (excludes DLQ).
func DiscoverIngestTopics(client TopicClient) ([]string, error) {
	if err := client.RefreshMetadata(); err != nil {
		return nil, fmt.Errorf("refreshing kafka metadata: %w", err)
	}
	names, err := client.Topics()
	if err != nil {
		return nil, fmt.Errorf("listing kafka topics: %w", err)
	}
	return FilterTopics(names), nil
}

// RunSessions consumes until ctx is cancelled, rediscovering topics each session (metering pattern).
func RunSessions(
	ctx context.Context,
	logger *slog.Logger,
	group sarama.ConsumerGroup,
	handler sarama.ConsumerGroupHandler,
	discover func(context.Context) ([]string, error),
	sessionInterval time.Duration,
) error {
	if logger == nil {
		logger = slog.Default()
	}
	if sessionInterval <= 0 {
		sessionInterval = DefaultSessionInterval
	}

	var topics []string
	logger.InfoContext(ctx, "starting consumer group sessions",
		slog.Duration("session_interval", sessionInterval),
	)

	for {
		discovered, err := discover(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "discovering topics; retrying with previous set",
				slog.Any("error", err),
			)
		} else if !sameTopics(discovered, topics) {
			logger.InfoContext(ctx, "topic set changed", slog.Any("topics", discovered))
			topics = discovered
		}

		if len(topics) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(discoverBackoff):
				continue
			}
		}

		sessionCtx, cancelSession := context.WithTimeout(ctx, sessionInterval)
		err = group.Consume(sessionCtx, topics, handler)
		cancelSession()
		if err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				logger.InfoContext(ctx, "consumer group closed")
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.ErrorContext(ctx, "consumer group error: retrying after backoff",
				slog.Any("error", err),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(discoverBackoff):
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// FilterTopics returns ingest topics from a broker topic list.
func FilterTopics(all []string) []string {
	out := make([]string, 0, len(all))
	for _, topic := range all {
		if cevent.TopicMatches(topic) {
			out = append(out, topic)
		}
	}
	slices.Sort(out)
	return out
}

func sameTopics(a, b []string) bool {
	sortedA := slices.Clone(a)
	sortedB := slices.Clone(b)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return slices.Equal(sortedA, sortedB)
}
