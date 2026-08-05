// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adapters

import (
	"context"
	"sync"
	"time"
)

type orderEntry struct {
	transitionTime time.Time // latest event transition_time (used for ordering)
	updatedAt      time.Time // wall-clock time of last update (used for eviction)
}

type orderTracker struct {
	mu       sync.Mutex
	lastSeen map[string]orderEntry // resource_id → latest transition time + wall-clock update time
	ttl      time.Duration
}

func newOrderTracker(ttl time.Duration) *orderTracker {
	return &orderTracker{
		lastSeen: make(map[string]orderEntry),
		ttl:      ttl,
	}
}

// check returns true if the event's transitionTime is out of order
// (earlier than the last seen time for this resourceID).
// Updates the tracker with the new time if it is the latest.
func (t *orderTracker) check(resourceID string, transitionTime time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.lastSeen[resourceID]
	if ok && transitionTime.Before(entry.transitionTime) {
		// Out of order — still refresh the wall-clock time so the entry
		// is not evicted while the resource is actively producing events.
		entry.updatedAt = time.Now()
		t.lastSeen[resourceID] = entry
		return true
	}
	t.lastSeen[resourceID] = orderEntry{transitionTime: transitionTime, updatedAt: time.Now()}
	return false
}

// evictExpired removes entries whose wall-clock update time exceeds the TTL.
func (t *orderTracker) evictExpired() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, entry := range t.lastSeen {
		if now.Sub(entry.updatedAt) > t.ttl {
			delete(t.lastSeen, id)
		}
	}
}

// startEviction runs evictExpired every 30 seconds until ctx is cancelled.
func (t *orderTracker) startEviction(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.evictExpired()
		}
	}
}

// len returns the number of tracked resources.
func (t *orderTracker) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.lastSeen)
}
