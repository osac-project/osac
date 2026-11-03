/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ttlCache keeps one successful load per key for a TTL, so repeated reads do not re-run the load on every request.
// Concurrent gets of the same key collapse into one load (singleflight). A failed load is neither cached nor served
// from a stale entry: the next get re-loads, exactly like an uncached read. At capacity, expired entries are pruned
// first, and a cache still full of fresh entries bypasses caching rather than evicting a fresh entry. The zero value
// of V is a cacheable value: an entry's presence in the map, not its content, marks it cached. Cached values are
// shared with every caller served from an entry, so callers must treat them as read-only.
type ttlCache[K comparable, V any] struct {
	ttl time.Duration
	// maxEntries bounds the cache; keys that can come from request parameters would otherwise grow it without limit.
	maxEntries int
	// keyString encodes a key as an injective singleflight string, because singleflight keys by string.
	keyString func(K) string
	// now is time.Now in production and a controllable clock in tests.
	now     func() time.Time
	mu      sync.Mutex
	entries map[K]ttlEntry[V]
	group   singleflight.Group
}

// ttlEntry is one cached, successful load.
type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

// newTTLCache builds a cache with the default clock.
func newTTLCache[K comparable, V any](ttl time.Duration, maxEntries int,
	keyString func(K) string) *ttlCache[K, V] {
	return &ttlCache[K, V]{
		ttl:        ttl,
		maxEntries: maxEntries,
		keyString:  keyString,
		now:        time.Now,
		entries:    map[K]ttlEntry[V]{},
	}
}

// get returns the cached value for key, loading it through load on a miss. Every concurrent caller of one key shares
// a single load, and only a successful load is cached.
func (c *ttlCache[K, V]) get(ctx context.Context, key K,
	load func(context.Context) (V, error)) (V, error) {
	if value, ok := c.snapshot(key); ok {
		return value, nil
	}
	result, err, _ := c.group.Do(c.keyString(key), func() (any, error) {
		// The leader of this flight may have refreshed the entry between the caller's miss and joining it.
		if value, ok := c.snapshot(key); ok {
			return value, nil
		}
		value, err := load(ctx)
		if err != nil {
			var zero V
			return zero, err
		}
		c.mu.Lock()
		c.store(key, value)
		c.mu.Unlock()
		return value, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return result.(V), nil
}

// snapshot returns the entry for key while it is still fresh.
func (c *ttlCache[K, V]) snapshot(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !c.now().Before(entry.expires) {
		var zero V
		return zero, false
	}
	return entry.value, true
}

// store inserts the entry, pruning expired entries when the cache is at capacity instead of growing past the bound.
// The caller holds c.mu.
func (c *ttlCache[K, V]) store(key K, value V) {
	if len(c.entries) >= c.maxEntries {
		for key, existing := range c.entries {
			if !c.now().Before(existing.expires) {
				delete(c.entries, key)
			}
		}
		if len(c.entries) >= c.maxEntries {
			return
		}
	}
	c.entries[key] = ttlEntry[V]{value: value, expires: c.now().Add(c.ttl)}
}
