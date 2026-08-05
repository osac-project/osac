/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// NotifierBuilder contains the data and logic needed to build a notifier.
type NotifierBuilder struct {
	logger          *slog.Logger
	channel         string
	pool            *pgxpool.Pool
	cleanupInterval time.Duration
}

// Notifier knows how to send notifications using PostgreSQL's NOTIFY command, using protocol buffers messages as
// payload.
type Notifier struct {
	logger          *slog.Logger
	channel         string
	pool            *pgxpool.Pool
	cleanupInterval time.Duration
}

// NewNotifier uses the information stored in the builder to create a new notifier.
func NewNotifier() *NotifierBuilder {
	return &NotifierBuilder{
		cleanupInterval: 1 * time.Minute,
	}
}

// SetLogger sets the logger for the notifier. This is mandatory.
func (b *NotifierBuilder) SetLogger(logger *slog.Logger) *NotifierBuilder {
	b.logger = logger
	return b
}

// SetChannel sets the channel name. This is mandatory.
func (b *NotifierBuilder) SetChannel(value string) *NotifierBuilder {
	b.channel = value
	return b
}

// SetPool sets the database connection pool that will be used for the asynchronous cleanup of old notifications.
// This is mandatory.
func (b *NotifierBuilder) SetPool(value *pgxpool.Pool) *NotifierBuilder {
	b.pool = value
	return b
}

// SetCleanupInterval sets the interval at which old notifications will be deleted. This is optional and the default
// is one minute.
func (b *NotifierBuilder) SetCleanupInterval(value time.Duration) *NotifierBuilder {
	b.cleanupInterval = value
	return b
}

// Build constructs a notifier instance using the configured parameters.
func (b *NotifierBuilder) Build() (result *Notifier, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.channel == "" {
		err = errors.New("channel is mandatory")
		return
	}
	if b.pool == nil {
		err = errors.New("database connection pool is mandatory")
		return
	}
	if b.cleanupInterval <= 0 {
		err = fmt.Errorf("cleanup interval should be positive, but it is %s", b.cleanupInterval)
		return
	}

	// Create and populate the object:
	logger := b.logger.With(
		slog.String("channel", b.channel),
	)
	result = &Notifier{
		logger:          logger,
		channel:         b.channel,
		pool:            b.pool,
		cleanupInterval: b.cleanupInterval,
	}
	return
}

// Notify sends a notification with the given payload using the configured channel. The payload is placed into an
// Any object, then marshalled using protocol buffers.
//
// Note that this method expects to find a transaction in the context.
func (n *Notifier) Notify(ctx context.Context, payload proto.Message) (err error) {
	// Get the transaction fron the context:
	tx, err := TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)

	// Encode the payload:
	wrapper, err := anypb.New(payload)
	if err != nil {
		return err
	}
	data, err := proto.Marshal(wrapper)
	if err != nil {
		n.logger.ErrorContext(
			ctx,
			"Failed to serialize payload",
			slog.Any("payload", payload),
			slog.Any("error", err),
		)
		return err
	}

	// Generate an identifier:
	id := uuid.New()

	// Save the payload to the database:
	_, err = tx.Exec(ctx, "insert into notifications (id, payload) values ($1, $2)", id, data)
	if err != nil {
		return err
	}

	// Send the notification:
	_, err = tx.Exec(ctx, "select pg_notify($1, $2)", n.channel, id)
	if err != nil {
		return err
	}
	if n.logger.Enabled(ctx, slog.LevelDebug) {
		n.logger.DebugContext(
			ctx,
			"Sent notification",
			slog.String("id", id),
			slog.Any("payload", payload),
		)
	}

	return nil
}

// Start begins the asynchronous cleanup of old notifications. It starts a goroutine that periodically deletes old
// notifications from the database. The goroutine will stop when the given context is cancelled.
func (n *Notifier) Start(ctx context.Context) error {
	go n.cleanupLoop(ctx)
	return nil
}

// cleanupLoop runs the cleanup operation periodically until the context is cancelled.
func (n *Notifier) cleanupLoop(ctx context.Context) {
	n.logger.InfoContext(
		ctx,
		"Starting notification cleanup loop",
		slog.Duration("interval", n.cleanupInterval),
	)
	ticker := time.NewTicker(n.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			n.logger.InfoContext(ctx, "Stopping notification cleanup loop")
			return
		case <-ticker.C:
			n.cleanup(ctx)
		}
	}
}

// cleanup deletes old notifications from the database.
func (n *Notifier) cleanup(ctx context.Context) {
	tag, err := n.pool.Exec(ctx, "delete from notifications where creation_timestamp < now() - interval '1 minute'")
	if err != nil {
		n.logger.ErrorContext(
			ctx,
			"Failed to delete old notifications",
			slog.Any("error", err),
		)
		return
	}
	count := tag.RowsAffected()
	if count > 0 {
		n.logger.DebugContext(
			ctx,
			"Deleted old notifications",
			slog.Int64("count", count),
		)
	}
}
