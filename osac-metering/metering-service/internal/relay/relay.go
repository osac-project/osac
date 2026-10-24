/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package relay sends the heartbeats a round wrote to the tenant's heartbeat topic. A database
// transaction and a Kafka write can't commit together, so the round only writes the outbox and
// this loop does the sending.
package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/metrics"
	"github.com/osac-project/osac-metering/internal/topics"
)

// BatchSize is how many outbox rows the relay sends per transaction.
const BatchSize = 200

// IdleWait is how long the relay waits when the outbox is empty.
const IdleWait = 200 * time.Millisecond

// Relay drains the outbox to Kafka.
type Relay struct {
	store    *database.Store
	producer sarama.SyncProducer
	logger   logr.Logger
}

// New creates a relay on an open producer.
func New(store *database.Store, producer sarama.SyncProducer, logger logr.Logger) (*Relay, error) {
	return &Relay{store: store, producer: producer, logger: logger}, nil
}

// RunTenant drains the tenant's outbox until the context ends. It sends outbox rows in order,
// waits for Kafka to confirm, then deletes them and advances the tenant's heartbeat offset. A
// crash between the send and the delete means the same rows are sent again with the same IDs;
// the billing provider drops the copies.
func (r *Relay) RunTenant(ctx context.Context, tenant string) error {
	if err := r.store.EnsureProgress(ctx, tenant); err != nil {
		return err
	}
	topic := topics.Heartbeat(tenant)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		sent, err := r.drainOnce(ctx, tenant, topic)
		if err != nil && ctx.Err() == nil {
			// A transient error must not stop the relay: the outbox keeps the
			// heartbeats and the next drain sends them again.
			r.logger.Error(err, "relay drain failed", "tenant", tenant)
		}
		if depth, depthErr := r.store.OutboxDepth(ctx, tenant); depthErr == nil {
			metrics.OutboxDepth(tenant, depth)
		} else if ctx.Err() == nil {
			r.logger.Error(depthErr, "reading the outbox depth", "tenant", tenant)
		}
		if sent == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(IdleWait):
			}
		}
	}
}

// drainOnce sends one batch of outbox rows and commits their removal. It begins the
// transaction and takes the tenant's relay lock before reading the outbox, so overlapping
// pods take turns over the same rows instead of both reading and sending them.
func (r *Relay) drainOnce(ctx context.Context, tenant, topic string) (int, error) {
	tx, err := r.store.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := database.TakeTenantLock(ctx, tx, database.LockRelay, tenant); err != nil {
		return 0, err
	}
	rows, err := database.NextOutbox(ctx, tx, tenant, BatchSize)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	progress, err := database.GetProgress(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}

	firstOffset := int64(-1)
	lastOffset := int64(0)
	for _, row := range rows {
		// SendMessage returns the partition first and the offset second.
		_, offset, err := r.producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Key:   sarama.StringEncoder(row.ID),
			Value: sarama.ByteEncoder(row.Payload),
		})
		if err != nil {
			return 0, fmt.Errorf("sending heartbeat %s: %w", row.ID, err)
		}
		if firstOffset < 0 {
			firstOffset = offset
		}
		if offset+1 > lastOffset {
			lastOffset = offset + 1
		}
	}

	seqs := make([]int64, 0, len(rows))
	for _, row := range rows {
		seqs = append(seqs, row.Seq)
	}
	if err := database.DeleteOutbox(ctx, tx, tenant, seqs); err != nil {
		return 0, err
	}
	// The offset moves only when this batch continues the stored one. A batch appended
	// above unread records, for example the backlog a database restore leaves below the
	// topic end, must not move the offset past them: catch-up replays those as
	// corrections and advances the offset itself.
	if firstOffset == progress.HeartbeatOffset && lastOffset > progress.HeartbeatOffset {
		if err := database.AdvanceHeartbeatOffset(ctx, tx, tenant, lastOffset); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	r.logger.Info("sent heartbeats", "tenant", tenant, "count", len(rows))
	return len(rows), nil
}
