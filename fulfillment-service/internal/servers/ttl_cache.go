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
	"fmt"
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
//
// A caller's wait and the fill it triggers have separate lifetimes. A caller that cancels, or whose deadline passes,
// stops waiting and gets its own context error, while the fill keeps running detached from every caller's
// cancellation for at most the fill budget, so the callers still waiting — and later gets — still receive the value.
// newTTLCache clamps the fill budget to at most half the TTL, so a fill that completes within its budget is never
// born expired. A panicking fill is contained and reported to every waiter as an ordinary failed load instead of
// crashing the process.
type ttlCache[K comparable, V any] struct {
	ttl time.Duration
	// fillBudget bounds how long one fill may run once it has been detached from every caller: the fill context
	// carries this deadline instead of any caller's. newTTLCache clamps it to at most half the TTL, so a fill that
	// completes within its budget still leaves at least half the TTL for the entry to be served.
	fillBudget time.Duration
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

// newTTLCache builds a cache with the default clock. fillBudget bounds a fill that has been detached from every
// caller; it is clamped to at most half of ttl, so a fill that completes within its budget is not already expired
// when stored and the entry still has at least half its TTL left to be served.
func newTTLCache[K comparable, V any](ttl, fillBudget time.Duration, maxEntries int,
	keyString func(K) string) *ttlCache[K, V] {
	return &ttlCache[K, V]{
		ttl:        ttl,
		fillBudget: clampFillBudget(ttl, fillBudget),
		maxEntries: maxEntries,
		keyString:  keyString,
		now:        time.Now,
		entries:    map[K]ttlEntry[V]{},
	}
}

// clampFillBudget enforces the invariant that a fill budget stays below the cache TTL, so a fill can never consume
// the lifetime of the entry it stores. The bound is half the TTL, which leaves the other half for serving the entry.
// A TTL shorter than two nanoseconds has no representable half, so the TTL itself, the closest positive bound,
// applies. A non-positive requested budget yields that positive bound, so a fill always has a positive budget when
// the TTL is positive. A non-positive TTL makes every entry expired as soon as it is stored, so the requested budget
// is kept unchanged there.
func clampFillBudget(ttl, fillBudget time.Duration) time.Duration {
	limit := ttl / 2
	if ttl > 0 && limit <= 0 {
		limit = ttl
	}
	if limit <= 0 {
		return fillBudget
	}
	if fillBudget <= 0 {
		return limit
	}
	return min(fillBudget, limit)
}

// get returns the cached value for key, loading it through load on a miss. Every concurrent caller of one key shares
// a single load, and only a successful load is cached. A fresh entry is served from the snapshot even when ctx is
// already done. On a miss with ctx already done, get returns ctx's own error without starting a fill. Otherwise get
// waits for the shared fill until either the fill completes or ctx is done: a caller that gives up returns its own
// context error promptly, while the fill keeps running for the remaining waiters and later gets. The fill runs under
// a context derived from the leading caller's — its values (transaction manager, auth metadata) are preserved, but
// its cancellation and deadline are dropped and replaced by the fill budget, so no caller's departure can cancel or
// truncate the shared fill. A fill that panics is converted to an error and, like any failure, caches nothing.
func (c *ttlCache[K, V]) get(ctx context.Context, key K,
	load func(context.Context) (V, error)) (V, error) {
	if value, ok := c.snapshot(key); ok {
		return value, nil
	}
	if err := ctx.Err(); err != nil {
		var zero V
		return zero, err
	}
	flight := c.group.DoChan(c.keyString(key), func() (result any, err error) {
		// Contain panics: singleflight deliberately re-panics in a goroutine when other callers are waiting on the
		// flight, which would crash the process, so a panicking fill must become a normal failed load here.
		defer func() {
			if p := recover(); p != nil {
				result = nil
				err = fmt.Errorf("ttl cache fill panicked: %v", p)
			}
		}()
		// The leader of this flight may have refreshed the entry between the caller's miss and joining it.
		if value, ok := c.snapshot(key); ok {
			return value, nil
		}
		fillCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.fillBudget)
		defer cancel()
		value, loadErr := load(fillCtx)
		if loadErr != nil {
			return nil, loadErr
		}
		c.mu.Lock()
		c.store(key, value)
		c.mu.Unlock()
		return value, nil
	})
	select {
	case result := <-flight:
		if result.Err != nil {
			var zero V
			return zero, result.Err
		}
		return result.Val.(V), nil
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
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
