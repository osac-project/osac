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
	"sync"
	"time"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

type memoryKey struct {
	tenant, source, id string
}

type memoryRow struct {
	record    Record
	status    string
	createdAt time.Time
}

// TestStore is an in-memory Intents implementation for unit tests.
type TestStore struct {
	mu   sync.Mutex
	rows map[memoryKey]memoryRow
}

// NewTestStore creates an empty test intent store.
func NewTestStore() *TestStore {
	return &TestStore{rows: map[memoryKey]memoryRow{}}
}

// Accept implements Intents.
func (m *TestStore) Accept(_ context.Context, accepted *cevent.Accepted) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memoryKey{accepted.Tenant, accepted.Source, accepted.ID}
	if existing, ok := m.rows[key]; ok {
		if existing.status == StatusCompleted || existing.status == StatusFailed {
			return Completed, nil
		}
		return Pending, nil
	}
	m.rows[key] = memoryRow{
		record: Record{
			Tenant:   accepted.Tenant,
			Source:   accepted.Source,
			ID:       accepted.ID,
			Kind:     accepted.Kind,
			Type:     accepted.Type,
			Severity: accepted.Severity,
			Topic:    accepted.Topic,
			Payload:  append([]byte(nil), accepted.Payload...),
		},
		status:    StatusPending,
		createdAt: time.Now(),
	}
	return Created, nil
}

// MarkStatus implements Intents.
func (m *TestStore) MarkStatus(_ context.Context, tenant, source, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memoryKey{tenant, source, id}
	row, ok := m.rows[key]
	if !ok || row.status != StatusPending {
		return nil
	}
	row.status = status
	m.rows[key] = row
	return nil
}

// ListPending implements Intents.
func (m *TestStore) ListPending(_ context.Context, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	type keyed struct {
		key memoryKey
		row memoryRow
	}
	pending := make([]keyed, 0)
	for key, row := range m.rows {
		if row.status == StatusPending {
			pending = append(pending, keyed{key: key, row: row})
		}
	}
	// Insertion order is enough for tests; sort by createdAt for stability.
	for i := 0; i < len(pending); i++ {
		for j := i + 1; j < len(pending); j++ {
			if pending[j].row.createdAt.Before(pending[i].row.createdAt) {
				pending[i], pending[j] = pending[j], pending[i]
			}
		}
	}
	if len(pending) > limit {
		pending = pending[:limit]
	}
	out := make([]Record, 0, len(pending))
	for _, item := range pending {
		out = append(out, item.row.record)
	}
	return out, nil
}

// StatusOf returns the status for tests.
func (m *TestStore) StatusOf(tenant, source, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[memoryKey{tenant, source, id}]
	if !ok {
		return ""
	}
	return row.status
}
