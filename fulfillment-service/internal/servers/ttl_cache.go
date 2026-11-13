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

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

// ttlCacheFillConcurrency bounds how many detached fills run at once across every ttlCache in the process. Fills are
// the expensive part of a miss — a provider read or a catalog sweep — and each runs detached from its callers, so
// bounding storage alone would still let a caller that cycles request-derived keys start a new detached fill for
// every key, each consuming its whole fill budget. Four slots: one provider rate-card List fills at most the two
// mapped services, one at a time, so a single List never needs more than one slot and four slots leave headroom for
// two concurrent administrator requests without widening the burst this bound exists to prevent. Fills never nest —
// no load calls another cache's get — so a fill holding a slot never waits on a second slot.
const ttlCacheFillConcurrency = 4

// ttlCacheFillLimiter is the process-wide bound on concurrent detached fills. Every cache newTTLCache builds shares
// it, so the bound holds across servers rather than per cache; a test may give one cache its own limiter.
var ttlCacheFillLimiter = semaphore.NewWeighted(ttlCacheFillConcurrency)

// ttlCache keeps one successful load per key for a TTL, so repeated reads do not re-run the load on every request.
// Concurrent gets of the same key collapse into one load (singleflight). A failed load is neither cached nor served
// from a stale entry: the next get re-loads, exactly like an uncached read. At capacity, expired entries are pruned
// first, and a cache still full of fresh entries bypasses caching rather than evicting a fresh entry. The zero value
// of V is a cacheable value: an entry's presence in the map, not its content, marks it cached. Cached values are
// shared with every caller served from an entry, so callers must treat them as read-only.
//
// A caller's wait and the fill it triggers have separate lifetimes. A caller that cancels, or whose deadline passes,
// stops waiting and gets its own context error, while the fill keeps running detached from every caller's
// cancellation with the fill budget as its deadline, so the callers still waiting — and later gets — still receive
// the value. That budget bounds the fill, not just the load: a load that returns successfully after the budget has
// expired is rejected like any other failed load, so a value the deadline already abandoned is never published and
// the next get retries. The check cannot interrupt a load that ignores its context; such a load still runs until it
// returns, and its result is discarded. An entry's lifetime starts when its value is stored, not when the fill began,
// so clamping the budget below the TTL bounds detached work rather than an entry's age. A panicking fill is contained
// and reported to every waiter as an ordinary failed load instead of crashing the process.
//
// Detached fills are capped process-wide, not just per key: every cache built by newTTLCache draws from one shared
// limiter, so at most ttlCacheFillConcurrency fills run at once however many distinct keys callers ask for, and
// repeats of a key that the full cache refused to store cannot pile up provider reads either. A fill that cannot
// take a slot within its own budget is an ordinary failed load: it caches nothing, the caller that was waiting on it
// gets that error, and the flight ends instead of staying wedged. A fill holds its slot only while it runs, released
// when it returns, error or panic included.
type ttlCache[K comparable, V any] struct {
	ttl time.Duration
	// fillBudget bounds how long one fill may run once it has been detached from every caller: the fill context
	// carries this deadline instead of any caller's. newTTLCache clamps it to at most half the TTL, which bounds how
	// long a key's detached work may take relative to the freshness that key produces.
	fillBudget time.Duration
	// maxEntries bounds the cache; keys that can come from request parameters would otherwise grow it without limit.
	maxEntries int
	// fillLimiter bounds how many detached fills run at once. newTTLCache assigns the shared process-wide limiter;
	// a test may replace this field with one sized for the behavior it exercises.
	fillLimiter *semaphore.Weighted
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
// caller; it is clamped to at most half of ttl, so a key's detached work stays shorter than the freshness it
// produces. The clamp bounds the fill, not the entry: expiry is stamped when the value is stored, so an entry keeps
// its full TTL however long its fill took. The returned cache draws its fill slots from the shared process-wide
// limiter, so its fills share one bound with every other cache built here.
func newTTLCache[K comparable, V any](ttl, fillBudget time.Duration, maxEntries int,
	keyString func(K) string) *ttlCache[K, V] {
	return &ttlCache[K, V]{
		ttl:         ttl,
		fillBudget:  clampFillBudget(ttl, fillBudget),
		maxEntries:  maxEntries,
		fillLimiter: ttlCacheFillLimiter,
		keyString:   keyString,
		now:         time.Now,
		entries:     map[K]ttlEntry[V]{},
	}
}

// clampFillBudget bounds a fill budget to at most half the cache TTL, so the detached work one fill may take stays
// shorter than the freshness that fill produces. It bounds the fill, not the entry: the store stamps expiry when the
// value is written, so a stored entry keeps its full TTL however long its fill took. A TTL shorter than two
// nanoseconds has no representable half, so the TTL itself, the closest positive bound, applies. A non-positive
// requested budget yields that positive bound, so a fill always has a positive budget when the TTL is positive. A
// non-positive TTL makes every entry expired as soon as it is stored, so the requested budget is kept unchanged
// there.
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
// truncate the shared fill. A fill that panics is converted to an error and, like any failure, caches nothing. A
// load that returns successfully after the fill budget has expired is a failed fill too: its value is discarded
// rather than stored, so the budget bounds the fill even when the load ignores its context, and the next get retries.
// Before running, the fill takes one slot from the cache's limiter on that same fill context, so a saturated limiter
// delays or fails a fill rather than starting it anyway: failing to get a slot within the budget ends the fill with
// the wait's error, and the slot goes back when the fill returns.
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
		// Wait for a fill slot on the fill context, so the wait is bounded by the same budget as the load and no
		// caller can shorten it. Failing here is an ordinary failed load: nothing is cached, and singleflight drops
		// the flight once this function returns, so the next get starts a fresh attempt.
		if err := c.fillLimiter.Acquire(fillCtx, 1); err != nil {
			return nil, fmt.Errorf("ttl cache fill waiting for a fill slot: %w", err)
		}
		// Registered after the recover above, so it runs first on a panic and a panicking load cannot leak a slot.
		defer c.fillLimiter.Release(1)
		value, loadErr := load(fillCtx)
		if loadErr != nil {
			return nil, loadErr
		}
		c.mu.Lock()
		// The budget bounds the fill, not only the load, including the wait for the publication mutex. Check under
		// that mutex so a successful load cannot publish after its deadline expires while waiting to store. Such a
		// fill is an ordinary failed load: nothing is stored, every waiter gets the deadline error, singleflight
		// drops the flight, and the next get starts a fresh fill.
		if err := fillCtx.Err(); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("ttl cache fill finished after its budget: %w", err)
		}
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
