/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package echo

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/schema"
)

// DefaultMaxEvents is the default ring-buffer capacity for the event store.
const DefaultMaxEvents = 1000

// storedEvent holds the full CloudEvent plus Kafka metadata.
type storedEvent struct {
	// Raw is the full CloudEvent serialized as JSON. Returned to callers
	// so E2E tests can assert on extensions (osacresourceid, osactenant, …)
	// and the data payload (billing_dimensions, transition_time, …).
	Raw json.RawMessage `json:"-"`

	// Indexed fields for server-side filtering.
	id         string
	eventType  string
	resourceID string
	receivedAt time.Time
}

// eventStore is a bounded, thread-safe ring buffer of received events.
// It supports filtered queries for E2E test assertions.
type eventStore struct {
	mu     sync.RWMutex
	events []storedEvent
	max    int
}

func newEventStore(max int) *eventStore {
	if max <= 0 {
		max = DefaultMaxEvents
	}
	return &eventStore{
		events: make([]storedEvent, 0, max),
		max:    max,
	}
}

// clear removes all events from the store.
func (s *eventStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:0]
}

// getByID returns the enriched JSON for the event with the given CloudEvent ID.
func (s *eventStore) getByID(id string) json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.events {
		if s.events[i].id == id {
			return s.events[i].Raw
		}
	}
	return nil
}

// add records a metering event in the ring buffer, storing the full
// CloudEvent JSON as-is. Kafka metadata (topic, partition, offset) is
// kept in indexed fields for filtering but not merged into the JSON
// to avoid overwriting CloudEvent attributes or introducing invalid
// extension names.
func (s *eventStore) add(event adapters.MeteringEvent) {
	ce := event.CloudEvent

	raw, err := json.Marshal(ce)
	if err != nil {
		log.Printf("failed to marshal CloudEvent id=%s: %v", ce.ID(), err)
		return
	}

	var resourceID string
	if v, ok := ce.Extensions()[schema.ExtResourceID]; ok {
		resourceID = fmt.Sprintf("%v", v)
	}

	entry := storedEvent{
		Raw:        raw,
		id:         ce.ID(),
		eventType:  ce.Type(),
		resourceID: resourceID,
		receivedAt: time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) >= s.max {
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = entry
	} else {
		s.events = append(s.events, entry)
	}
}

// query returns events matching the given filters.
func (s *eventStore) query(eventType, resourceID string, since time.Time, limit int) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []json.RawMessage
	for _, e := range s.events {
		if eventType != "" && e.eventType != eventType {
			continue
		}
		if resourceID != "" && e.resourceID != resourceID {
			continue
		}
		if !since.IsZero() && e.receivedAt.Before(since) {
			continue
		}
		result = append(result, e.Raw)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

// count returns the number of events matching the given filters.
func (s *eventStore) count(eventType, resourceID string, since time.Time) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := 0
	for _, e := range s.events {
		if eventType != "" && e.eventType != eventType {
			continue
		}
		if resourceID != "" && e.resourceID != resourceID {
			continue
		}
		if !since.IsZero() && e.receivedAt.Before(since) {
			continue
		}
		n++
	}
	return n
}

// handleEvents serves GET /events with optional query parameters:
//   - type:        filter by CloudEvent type
//   - resource_id: filter by resource ID (osacresourceid extension)
//   - since:       RFC3339 timestamp, only events received after this time
//   - limit:       max number of results
func (s *eventStore) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	eventType := q.Get("type")
	resourceID := q.Get("resource_id")

	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid since parameter: %v", err), http.StatusBadRequest)
			return
		}
		since = t
	}

	var limit int
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			http.Error(w, fmt.Sprintf("invalid limit parameter: %v", err), http.StatusBadRequest)
			return
		}
		limit = n
	}

	events := s.query(eventType, resourceID, since, limit)
	if events == nil {
		events = []json.RawMessage{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events) //nolint:errcheck
}

// handleEventByID serves GET /events/{id} — returns a single event by CloudEvent ID.
func (s *eventStore) handleEventByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing event id", http.StatusBadRequest)
		return
	}

	raw := s.getByID(id)
	if raw == nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(raw) //nolint:errcheck
}

// handleDeleteEvents serves DELETE /events — clears all stored events.
func (s *eventStore) handleDeleteEvents(w http.ResponseWriter, _ *http.Request) {
	s.clear()
	w.WriteHeader(http.StatusNoContent)
}

// handleCount serves GET /events/count with the same filters as /events.
func (s *eventStore) handleCount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	eventType := q.Get("type")
	resourceID := q.Get("resource_id")

	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid since parameter: %v", err), http.StatusBadRequest)
			return
		}
		since = t
	}

	n := s.count(eventType, resourceID, since)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"count": n}) //nolint:errcheck
}
