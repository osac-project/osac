/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package drain

import (
	"context"
	"log/slog"
	"time"

	"github.com/osac-project/osac/osac-notifications/internal/dlq"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	"github.com/osac-project/osac/osac-notifications/internal/intent"
)

const defaultBatch = 100

// Drain finishes pending notification_intent rows after Kafka commit-after-intent.
type Drain struct {
	logger     *slog.Logger
	intents    intent.Intents
	dispatcher *fanout.Dispatcher
	dlq        dlq.Publisher
	batch      int
	interval   time.Duration
}

// New builds a pending-intent drain loop.
func New(logger *slog.Logger, intents intent.Intents, dispatcher *fanout.Dispatcher,
	deadLetter dlq.Publisher, interval time.Duration) *Drain {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if deadLetter == nil {
		deadLetter = dlq.Nop{}
	}
	return &Drain{
		logger:     logger,
		intents:    intents,
		dispatcher: dispatcher,
		dlq:        deadLetter,
		batch:      defaultBatch,
		interval:   interval,
	}
}

// Run repeatedly drains pending intents until ctx is cancelled.
func (d *Drain) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		if err := d.Once(ctx); err != nil && ctx.Err() == nil {
			d.logger.ErrorContext(ctx, "Intent drain pass failed", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once fans out one batch of pending intents.
func (d *Drain) Once(ctx context.Context) error {
	if d == nil || d.intents == nil || d.dispatcher == nil {
		return nil
	}
	pending, err := d.intents.ListPending(ctx, d.batch)
	if err != nil {
		return err
	}
	for _, rec := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		accepted, err := rec.ToAccepted()
		if err != nil {
			d.logger.ErrorContext(ctx, "Corrupt pending intent, marked failed",
				slog.String("tenant", rec.Tenant),
				slog.String("source", rec.Source),
				slog.String("id", rec.ID),
				slog.Any("error", err),
			)
			if failErr := d.fail(ctx, rec, err.Error()); failErr != nil {
				d.logger.ErrorContext(ctx, "Failed to DLQ/mark bad payload intent",
					slog.Any("error", failErr),
				)
			}
			continue
		}
		if err := d.dispatcher.FanOut(ctx, accepted); err != nil {
			if fanout.Permanent(err) {
				if failErr := d.fail(ctx, rec, err.Error()); failErr != nil {
					d.logger.ErrorContext(ctx, "Failed to DLQ/mark permanent fan-out failure",
						slog.String("tenant", rec.Tenant),
						slog.String("source", rec.Source),
						slog.String("id", rec.ID),
						slog.Any("error", failErr),
					)
				}
				continue
			}
			d.logger.ErrorContext(ctx, "Drain fan-out failed; will retry",
				slog.String("tenant", rec.Tenant),
				slog.String("source", rec.Source),
				slog.String("id", rec.ID),
				slog.Any("error", err),
			)
			continue
		}
		if err := d.intents.MarkStatus(ctx, rec.Tenant, rec.Source, rec.ID, intent.StatusCompleted); err != nil {
			d.logger.ErrorContext(ctx, "Drain mark-done failed; will retry",
				slog.String("tenant", rec.Tenant),
				slog.String("source", rec.Source),
				slog.String("id", rec.ID),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

func (d *Drain) fail(ctx context.Context, rec intent.Record, reason string) error {
	if err := d.dlq.Publish(ctx, rec.Tenant, rec.Payload, reason); err != nil {
		return err
	}
	return d.intents.MarkStatus(ctx, rec.Tenant, rec.Source, rec.ID, intent.StatusFailed)
}
