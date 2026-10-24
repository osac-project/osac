/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package fold

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/metrics"
	"github.com/osac-project/osac-metering/internal/topics"
)

// BatchSize is how many events one fold transaction applies.
const BatchSize = 200

// PollWait is how long the fold waits for more events before it applies what it has.
const PollWait = 200 * time.Millisecond

// Fold reads a tenant's event topic and folds its events into history. It resumes from the
// tenant's stored event offset and never creates topics.
type Fold struct {
	store     *database.Store
	extractor *FactExtractor
	consumer  sarama.Consumer
	logger    logr.Logger
}

// New creates a fold on an open sarama consumer.
func New(store *database.Store, consumer sarama.Consumer, logger logr.Logger) (*Fold, error) {
	extractor, err := NewFactExtractor()
	if err != nil {
		return nil, err
	}
	return &Fold{store: store, extractor: extractor, consumer: consumer, logger: logger}, nil
}

// RunTenant folds the tenant's events until the context ends. Each transaction takes the
// tenant's fold lock, writes the facts its events carry, and advances the event offset. Events
// of types metering does not track only advance the offset.
func (f *Fold) RunTenant(ctx context.Context, tenant string) error {
	if err := f.store.EnsureProgress(ctx, tenant); err != nil {
		return err
	}
	topic := topics.Events(tenant)

	for {
		if ctx.Err() != nil {
			return nil
		}
		progress, err := f.store.GetTenantProgress(ctx, tenant)
		if err != nil {
			return err
		}
		partition, err := f.consumer.ConsumePartition(topic, 0, startOffset(progress.EventOffset))
		// skippedFrom is the stored offset an out-of-range consume fell back from:
		// retention deleted the segments below it, so those events are gone.
		skippedFrom := int64(-1)
		if err != nil {
			if errors.Is(err, sarama.ErrOffsetOutOfRange) {
				skippedFrom = progress.EventOffset
				partition, err = f.consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
			}
			if err != nil {
				return fmt.Errorf("consuming %s: %w", topic, err)
			}
		}
		err = f.drain(ctx, tenant, partition, skippedFrom)
		closeErr := partition.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("closing consumer of %s: %w", topic, closeErr)
		}
	}
}

// drain applies events until the context ends. A partition consumer only ends with its
// context, so the fold reopens it when the stored offset moves, for example after another pod
// folded ahead. skippedFrom, when not negative, is the stored offset that turned out to be
// below the oldest retained one; the first message then reports how much retention deleted.
func (f *Fold) drain(
	ctx context.Context, tenant string, partition sarama.PartitionConsumer, skippedFrom int64,
) error {
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-batchCtx.Done()
		_ = partition.Close()
	}()

	for {
		var messages []*sarama.ConsumerMessage
	collect:
		for len(messages) < BatchSize {
			select {
			case <-ctx.Done():
				if len(messages) > 0 {
					return f.apply(ctx, tenant, messages)
				}
				return nil
			case message, ok := <-partition.Messages():
				if !ok {
					// Kafka closed the partition's stream, for example after an
					// offset-out-of-range error. Return what was collected so
					// RunTenant reopens the consumer instead of spinning on a
					// closed channel.
					if len(messages) > 0 {
						return f.apply(ctx, tenant, messages)
					}
					return nil
				}
				messages = append(messages, message)
			case <-time.After(PollWait):
				break collect
			}
		}
		if len(messages) == 0 {
			continue
		}
		if skippedFrom >= 0 {
			f.logSkipped(tenant, skippedFrom, messages[0].Offset)
			skippedFrom = -1
		}
		if err := f.apply(ctx, tenant, messages); err != nil {
			return err
		}
		// Reopen when the stored offset moved past what this partition consumer holds,
		// because another pod folded ahead of us.
		progress, err := f.store.GetTenantProgress(ctx, tenant)
		if err != nil {
			return err
		}
		if progress.EventOffset > messages[len(messages)-1].Offset+1 {
			return nil
		}
	}
}

// apply folds one batch of events in one transaction.
func (f *Fold) apply(ctx context.Context, tenant string, messages []*sarama.ConsumerMessage) error {
	tx, err := f.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := database.TakeTenantLock(ctx, tx, database.LockFold, tenant); err != nil {
		return err
	}
	for _, message := range messages {
		event := &privatev1.Event{}
		if err := proto.Unmarshal(message.Value, event); err != nil {
			// A record that is not an event can never become one. Skip it and move
			// on, like the publisher does with changes it cannot convert.
			if err := f.skipEvent(ctx, tx, tenant, metrics.SkipUndecodable, err, message); err != nil {
				return err
			}
			continue
		}
		facts, err := f.extractor.Facts(tenant, event)
		if err != nil {
			if !errors.Is(err, ErrForeignTenant) {
				return err
			}
			// Misrouted data: billing it here would bill the wrong customer.
			if err := f.skipEvent(ctx, tx, tenant, metrics.SkipForeignTenant, err, message); err != nil {
				return err
			}
			continue
		}
		if len(facts) == 0 {
			if err := database.AdvanceEventOffset(ctx, tx, tenant, message.Offset+1); err != nil {
				return err
			}
			continue
		}
		if err := database.InsertFacts(ctx, tx, tenant, facts, message.Offset+1); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// skipEvent drops one record the fold must not bill: an undecodable record, or an event
// of another tenant. The skip stays loud in the log and the metrics, and the offset
// still advances, so the fold never stalls on the record.
func (f *Fold) skipEvent(
	ctx context.Context, tx database.Querier, tenant, reason string, err error,
	message *sarama.ConsumerMessage,
) error {
	f.logger.Error(err, "Skipping event",
		"tenant", tenant, "topic", message.Topic, "offset", message.Offset, "reason", reason)
	metrics.EventsSkipped(tenant, reason, 1)
	return database.AdvanceEventOffset(ctx, tx, tenant, message.Offset+1)
}

// startOffset maps a stored next offset to the offset a partition consumer starts at. A tenant
// the fold has never seen starts at offset 0.
func startOffset(stored int64) int64 {
	if stored <= 0 {
		return sarama.OffsetOldest
	}
	return stored
}

// logSkipped reports loudly that retention deleted the events between a stored offset and the
// oldest retained one. Those events are gone from history, so resources can under-bill; the
// operator must be able to find the gap.
func (f *Fold) logSkipped(tenant string, skippedFrom, oldest int64) {
	if oldest <= skippedFrom {
		return
	}
	f.logger.Error(nil,
		"Kafka retention deleted events before the stored offset; they are gone from history "+
			"and their usage cannot be billed",
		"tenant", tenant, "topic", topics.Events(tenant),
		"skippedFromOffset", skippedFrom, "oldestRetainedOffset", oldest,
		"skippedEvents", oldest-skippedFrom,
	)
	metrics.EventsSkipped(tenant, metrics.SkipRetention, oldest-skippedFrom)
}
