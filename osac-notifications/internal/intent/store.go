/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
    "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package intent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

const (
	// StatusPending means inbox fan-out has not completed.
	StatusPending = "pending"
	// StatusCompleted means inbox fan-out completed successfully.
	StatusCompleted = "completed"
	// StatusFailed means fan-out permanently failed.
	StatusFailed = "failed"
)

// Querier is the subset of pgx pool/tx used by the intent store.
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Result of an intent Accept attempt.
type Result int

const (
	// Created means a new pending intent row was inserted; fan-out is needed.
	Created Result = iota
	// Pending means the intent already existed and is still pending; fan-out is needed.
	Pending
	// Completed means the intent already existed and fan-out previously finished; skip fan-out.
	Completed
)

// Record is a durable intent row used by the drain loop.
type Record struct {
	Tenant   string
	Source   string
	ID       string
	Kind     string
	Type     string
	Severity string
	Topic    string
	Payload  []byte
}

// ToAccepted rebuilds an Accepted event from stored columns + payload for fan-out.
// It does not re-run grant validation (that already passed when the intent was written).
func (r Record) ToAccepted() (*cevent.Accepted, error) {
	event, err := cevent.Parse(r.Payload)
	if err != nil {
		return nil, err
	}
	var data cevent.Data
	if len(event.Data()) > 0 {
		if err := json.Unmarshal(event.Data(), &data); err != nil {
			return nil, fmt.Errorf("unmarshal data: %w", err)
		}
	}
	return &cevent.Accepted{
		Event:    event,
		Tenant:   r.Tenant,
		Source:   r.Source,
		ID:       r.ID,
		Kind:     r.Kind,
		Type:     r.Type,
		Severity: r.Severity,
		Topic:    r.Topic,
		Data:     data,
		Payload:  append(json.RawMessage(nil), r.Payload...),
	}, nil
}

// Intents is the outbox API used by ingest and drain.
type Intents interface {
	Accept(ctx context.Context, accepted *cevent.Accepted) (Result, error)
	MarkStatus(ctx context.Context, tenant, source, id, status string) error
	ListPending(ctx context.Context, limit int) ([]Record, error)
}

// Store writes durable accept intents.
type Store struct {
	db Querier
}

// NewStore creates an intent store.
func NewStore(db Querier) *Store {
	return &Store{db: db}
}

// Accept inserts a pending intent. On conflict, returns Pending or Completed from the existing row.
func (s *Store) Accept(ctx context.Context, accepted *cevent.Accepted) (Result, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("intent store is not configured")
	}
	tag, err := s.db.Exec(ctx, `
		insert into notification_intent
			(tenant, source, id, kind, type, severity, topic, payload, status)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (tenant, source, id) do nothing
	`, accepted.Tenant, accepted.Source, accepted.ID, accepted.Kind, accepted.Type,
		accepted.Severity, accepted.Topic, accepted.Payload, StatusPending)
	if err != nil {
		return 0, fmt.Errorf("insert notification_intent: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return Created, nil
	}

	var status string
	err = s.db.QueryRow(ctx, `
		select status from notification_intent
		where tenant = $1 and source = $2 and id = $3
	`, accepted.Tenant, accepted.Source, accepted.ID).Scan(&status)
	if err != nil {
		return 0, fmt.Errorf("load notification_intent status: %w", err)
	}
	switch status {
	case StatusCompleted, StatusFailed:
		// Terminal states: do not fan out again.
		return Completed, nil
	case StatusPending:
		return Pending, nil
	default:
		return 0, fmt.Errorf("unknown notification_intent status %q", status)
	}
}

// MarkStatus updates a pending intent to completed or failed.
func (s *Store) MarkStatus(ctx context.Context, tenant, source, id, status string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("intent store is not configured")
	}
	_, err := s.db.Exec(ctx, `
		update notification_intent
		set status = $4
		where tenant = $1 and source = $2 and id = $3 and status = $5
	`, tenant, source, id, status, StatusPending)
	if err != nil {
		return fmt.Errorf("mark notification_intent %s: %w", status, err)
	}
	return nil
}

// ListPending returns the oldest pending intents up to limit.
func (s *Store) ListPending(ctx context.Context, limit int) ([]Record, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("intent store is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		select tenant, source, id, kind, type, severity, topic, payload
		from notification_intent
		where status = $1
		order by creation_timestamp
		limit $2
	`, StatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending notification_intent: %w", err)
	}
	defer rows.Close()

	out := make([]Record, 0, limit)
	for rows.Next() {
		var rec Record
		if err := rows.Scan(
			&rec.Tenant, &rec.Source, &rec.ID, &rec.Kind, &rec.Type, &rec.Severity, &rec.Topic, &rec.Payload,
		); err != nil {
			return nil, fmt.Errorf("scan notification_intent: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
