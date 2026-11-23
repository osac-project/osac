/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/dlq"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	"github.com/osac-project/osac/osac-notifications/internal/grant"
	"github.com/osac-project/osac/osac-notifications/internal/intent"
)

// Outcome describes how a Kafka message was handled for offset commit.
type Outcome int

const (
	// Accepted means the intent is durable and the Kafka offset may be committed.
	Accepted Outcome = iota
	// Rejected means validation failed with no intent. Commit after DLQ write.
	Rejected
)

// Ingest validates CloudEvents, stores intents, and best-effort fans out.
type Ingest struct {
	logger  *slog.Logger
	grants  grant.Map
	intents intent.Intents
	inbox   *fanout.Inbox
	dlq     dlq.Publisher
}

// New builds an ingest pipeline.
func New(logger *slog.Logger, grants grant.Map, intents intent.Intents, inbox *fanout.Inbox,
	deadLetter dlq.Publisher) *Ingest {
	if logger == nil {
		logger = slog.Default()
	}
	if grants == nil {
		grants = grant.DefaultMap
	}
	if deadLetter == nil {
		deadLetter = dlq.Nop{}
	}
	return &Ingest{logger: logger, grants: grants, intents: intents, inbox: inbox, dlq: deadLetter}
}

// Handle validates, stores a durable intent, and best-effort fans out.
// Accepted allows Kafka commit while pending fan-out is drained asynchronously.
func (i *Ingest) Handle(ctx context.Context, topic string, body []byte) (Outcome, error) {
	accepted, err := cevent.Validate(topic, body, i.grants)
	if err != nil {
		i.logger.InfoContext(ctx, "Rejected notification CloudEvent",
			slog.String("topic", topic),
			slog.Any("error", err),
		)
		tenant, _ := cevent.TenantFromTopic(topic)
		if tenant == "" {
			tenant = "unknown"
		}
		if dlqErr := i.dlq.Publish(ctx, tenant, body, err.Error()); dlqErr != nil {
			return 0, fmt.Errorf("dlq validation reject: %w", dlqErr)
		}
		return Rejected, nil
	}

	result, err := i.intents.Accept(ctx, accepted)
	if err != nil {
		return 0, fmt.Errorf("store intent: %w", err)
	}
	if result == intent.Completed {
		i.logger.DebugContext(ctx, "Duplicate notification intent already terminal; skipping fan-out",
			slog.String("tenant", accepted.Tenant),
			slog.String("source", accepted.Source),
			slog.String("id", accepted.ID),
		)
		return Accepted, nil
	}

	if err := i.inbox.FanOut(ctx, accepted); err != nil {
		if fanout.Permanent(err) {
			return Accepted, i.failIntent(ctx, accepted, body, err)
		}
		i.logger.ErrorContext(ctx, "Eager inbox fan-out failed; leaving intent pending for drain",
			slog.String("tenant", accepted.Tenant),
			slog.String("source", accepted.Source),
			slog.String("id", accepted.ID),
			slog.Any("error", err),
		)
		return Accepted, nil
	}
	if err := i.intents.MarkStatus(ctx, accepted.Tenant, accepted.Source, accepted.ID, intent.StatusCompleted); err != nil {
		i.logger.ErrorContext(ctx, "Failed to mark intent done after fan-out; drain will retry",
			slog.String("tenant", accepted.Tenant),
			slog.String("source", accepted.Source),
			slog.String("id", accepted.ID),
			slog.Any("error", err),
		)
	}
	return Accepted, nil
}

func (i *Ingest) failIntent(ctx context.Context, accepted *cevent.Accepted, body []byte, cause error) error {
	i.logger.ErrorContext(ctx, "Permanent inbox fan-out failure; writing DLQ and marking failed",
		slog.String("tenant", accepted.Tenant),
		slog.String("source", accepted.Source),
		slog.String("id", accepted.ID),
		slog.Any("error", cause),
	)
	if err := i.dlq.Publish(ctx, accepted.Tenant, body, cause.Error()); err != nil {
		return fmt.Errorf("dlq permanent fan-out failure: %w", err)
	}
	if err := i.intents.MarkStatus(ctx, accepted.Tenant, accepted.Source, accepted.ID, intent.StatusFailed); err != nil {
		return fmt.Errorf("mark intent failed: %w", err)
	}
	return nil
}
