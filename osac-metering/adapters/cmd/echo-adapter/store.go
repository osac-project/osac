/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/osac-project/osac-metering/adapters"
)

const defaultMaxEvents = 1000

// storedEvent holds the metadata for a received metering event.
type storedEvent struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
	Time       string    `json:"time,omitempty"`
	ResourceID string    `json:"resource_id,omitempty"`
	Topic      string    `json:"topic"`
	Partition  int32     `json:"partition"`
	Offset     int64     `json:"offset"`
	ReceivedAt time.Time `json:"received_at"`
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
		max = defaultMaxEvents
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

// getByID returns the event with the given CloudEvent ID, or nil if not found.
func (s *eventStore) getByID(id string) *storedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.events {
		if s.events[i].ID == id {
			e := s.events[i]
			return &e
		}
	}
	return nil
}

// add records a metering event in the ring buffer.
func (s *eventStore) add(event adapters.MeteringEvent) {
	ce := event.CloudEvent

	var resourceID string
	if v, ok := ce.Extensions()["osacresourceid"]; ok {
		resourceID = fmt.Sprintf("%v", v)
	}

	entry := storedEvent{
		ID:         ce.ID(),
		Type:       ce.Type(),
		Source:     ce.Source(),
		Time:       ce.Time().Format(time.RFC3339),
		ResourceID: resourceID,
		Topic:      event.Topic,
		Partition:  event.Partition,
		Offset:     event.Offset,
		ReceivedAt: time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) >= s.max {
		// Shift left by 1, dropping the oldest entry.
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = entry
	} else {
		s.events = append(s.events, entry)
	}
}

// query returns events matching the given filters.
func (s *eventStore) query(eventType, resourceID string, since time.Time, limit int) []storedEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []storedEvent
	for _, e := range s.events {
		if eventType != "" && e.Type != eventType {
			continue
		}
		if resourceID != "" && e.ResourceID != resourceID {
			continue
		}
		if !since.IsZero() && e.ReceivedAt.Before(since) {
			continue
		}
		result = append(result, e)
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
		if eventType != "" && e.Type != eventType {
			continue
		}
		if resourceID != "" && e.ResourceID != resourceID {
			continue
		}
		if !since.IsZero() && e.ReceivedAt.Before(since) {
			continue
		}
		n++
	}
	return n
}

// handleEvents serves GET /events with optional query parameters:
//   - type:        filter by CloudEvent type
//   - resource_id: filter by resource ID
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
		events = []storedEvent{}
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

	event := s.getByID(id)
	if event == nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event) //nolint:errcheck
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
