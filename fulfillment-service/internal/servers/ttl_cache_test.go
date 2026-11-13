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
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sync/semaphore"
)

// fillTracker records the order in which fills start and finish and the peak number that ran at once, so a spec can
// assert the ordering and mutual exclusion of two fills without waiting on a clock.
type fillTracker struct {
	mu      sync.Mutex
	events  []string
	running int
	peak    int
}

func (t *fillTracker) start(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, name+":start")
	t.running++
	if t.running > t.peak {
		t.peak = t.running
	}
}

func (t *fillTracker) finish(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, name+":finish")
	t.running--
}

// snapshot returns the recorded event order and the peak number of fills that ran at once.
func (t *fillTracker) snapshot() (events []string, peak int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.events...), t.peak
}

var _ = Describe("Billing TTL cache", func() {
	// newTestCache returns a string-keyed cache with a one-minute TTL and a fill budget below it, whose clock is the
	// addressable variable now, so a spec can advance time.
	newTestCache := func(maxEntries int) (cache *ttlCache[string, int], now *time.Time) {
		cache = newTTLCache[string, int](time.Minute, 30*time.Second, maxEntries, func(key string) string { return key })
		current := time.Unix(0, 0)
		cache.now = func() time.Time { return current }
		return cache, &current
	}

	It("returns the loaded value and reuses it for the TTL", func() {
		cache, _ := newTestCache(8)
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			return 7, nil
		}
		for range 2 {
			value, err := cache.get(context.Background(), "key", load)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal(7))
		}
		Expect(loads).To(Equal(1))
	})

	It("reloads once the TTL has elapsed", func() {
		cache, now := newTestCache(8)
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			return 7, nil
		}
		_, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		*now = now.Add(time.Minute)
		_, err = cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(loads).To(Equal(2))
	})

	It("caches a zero value instead of re-reading it", func() {
		cache, _ := newTestCache(8)
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			return 0, nil
		}
		for range 2 {
			value, err := cache.get(context.Background(), "key", load)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal(0))
		}
		Expect(loads).To(Equal(1))
	})

	It("does not cache a failed load", func() {
		cache, _ := newTestCache(8)
		failure := errors.New("load unavailable")
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			if loads == 1 {
				return 0, failure
			}
			return 7, nil
		}
		_, err := cache.get(context.Background(), "key", load)
		Expect(err).To(MatchError(failure))
		value, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads).To(Equal(2))
	})

	It("collapses concurrent loads of one key into a single call", func() {
		cache, _ := newTestCache(8)
		release := make(chan struct{})
		var loads atomic.Int32
		load := func(context.Context) (int, error) {
			loads.Add(1)
			<-release
			return 7, nil
		}
		const callers = 8
		type outcome struct {
			value int
			err   error
		}
		outcomes := make(chan outcome, callers)
		for range callers {
			go func() {
				value, err := cache.get(context.Background(), "key", load)
				outcomes <- outcome{value: value, err: err}
			}()
		}
		Eventually(loads.Load).Should(Equal(int32(1)))
		close(release)
		for range callers {
			select {
			case result := <-outcomes:
				Expect(result.err).ToNot(HaveOccurred())
				Expect(result.value).To(Equal(7))
			case <-time.After(5 * time.Second):
				Fail("a concurrent get did not return")
			}
		}
		Expect(loads.Load()).To(Equal(int32(1)))
	})

	It("serves different keys independently", func() {
		cache, _ := newTestCache(8)
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			return loads, nil
		}
		_, err := cache.get(context.Background(), "one", load)
		Expect(err).ToNot(HaveOccurred())
		_, err = cache.get(context.Background(), "two", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(loads).To(Equal(2))
	})

	It("prunes an expired entry at capacity before storing the new one", func() {
		cache, now := newTestCache(1)
		_, err := cache.get(context.Background(), "key", func(context.Context) (int, error) { return 7, nil })
		Expect(err).ToNot(HaveOccurred())
		*now = now.Add(time.Minute)
		value, err := cache.get(context.Background(), "key", func(context.Context) (int, error) { return 9, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(9))
	})

	It("bypasses caching when at capacity with only fresh entries", func() {
		cache, _ := newTestCache(1)
		cache.entries["occupied"] = ttlEntry[int]{value: 1, expires: time.Unix(0, 0).Add(time.Minute)}
		value, err := cache.get(context.Background(), "key", func(context.Context) (int, error) { return 7, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		_, cached := cache.entries["key"]
		Expect(cached).To(BeFalse(), "a full cache must not store a new entry")
	})

	// newCountingKeyString builds a keyString that counts its calls: get evaluates it after its cancellation check,
	// immediately before the caller joins the flight, and a pending fill keeps the flight joinable, so a counted
	// second call proves the second caller committed to the in-flight fill.
	newCountingKeyString := func(calls *atomic.Int32) func(string) string {
		return func(key string) string {
			calls.Add(1)
			return key
		}
	}

	It("keeps filling when the leading caller cancels", func() {
		var keyStringCalls atomic.Int32
		cache := newTTLCache[string, int](time.Minute, 30*time.Second, 8, newCountingKeyString(&keyStringCalls))
		started := make(chan struct{})
		mayFinish := make(chan struct{})
		var loads atomic.Int32
		var fillCtxErr error
		load := func(ctx context.Context) (int, error) {
			loads.Add(1)
			close(started)
			<-mayFinish
			fillCtxErr = ctx.Err() // read after the leader cancelled: the fill context must still be live
			return 7, nil
		}
		type outcome struct {
			value int
			err   error
		}
		leaderCtx, cancelLeader := context.WithCancel(context.Background())
		defer cancelLeader()
		leaderDone := make(chan error, 1)
		go func() {
			_, err := cache.get(leaderCtx, "key", load)
			leaderDone <- err
		}()
		<-started
		cancelLeader()
		select {
		case err := <-leaderDone:
			Expect(err).To(MatchError(context.Canceled))
		case <-time.After(5 * time.Second):
			Fail("the cancelled leader did not return")
		}
		followerDone := make(chan outcome, 1)
		go func() {
			value, err := cache.get(context.Background(), "key", load)
			followerDone <- outcome{value: value, err: err}
		}()
		Eventually(keyStringCalls.Load).Should(Equal(int32(2)))
		close(mayFinish)
		select {
		case result := <-followerDone:
			Expect(result.err).ToNot(HaveOccurred())
			Expect(result.value).To(Equal(7))
		case <-time.After(5 * time.Second):
			Fail("the follower did not receive the fill's value")
		}
		Expect(fillCtxErr).ToNot(HaveOccurred(), "the leader's cancellation must not reach the fill context")
		value, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads.Load()).To(Equal(int32(1)))
	})

	It("keeps filling when a following caller cancels", func() {
		var keyStringCalls atomic.Int32
		cache := newTTLCache[string, int](time.Minute, 30*time.Second, 8, newCountingKeyString(&keyStringCalls))
		release := make(chan struct{})
		var loads atomic.Int32
		load := func(context.Context) (int, error) {
			loads.Add(1)
			<-release
			return 7, nil
		}
		type outcome struct {
			value int
			err   error
		}
		leaderDone := make(chan outcome, 1)
		go func() {
			value, err := cache.get(context.Background(), "key", load)
			leaderDone <- outcome{value: value, err: err}
		}()
		Eventually(loads.Load).Should(Equal(int32(1)))
		followerCtx, cancelFollower := context.WithCancel(context.Background())
		defer cancelFollower()
		followerDone := make(chan error, 1)
		go func() {
			_, err := cache.get(followerCtx, "key", load)
			followerDone <- err
		}()
		Eventually(keyStringCalls.Load).Should(Equal(int32(2)))
		cancelFollower()
		select {
		case err := <-followerDone:
			Expect(err).To(MatchError(context.Canceled))
		case <-time.After(5 * time.Second):
			Fail("the cancelled follower did not return")
		}
		close(release)
		select {
		case result := <-leaderDone:
			Expect(result.err).ToNot(HaveOccurred())
			Expect(result.value).To(Equal(7))
		case <-time.After(5 * time.Second):
			Fail("the leader did not receive the fill's value")
		}
		Expect(loads.Load()).To(Equal(int32(1)))
	})

	It("completes and caches a fill whose every caller cancelled", func() {
		cache, _ := newTestCache(8)
		release := make(chan struct{})
		var loads atomic.Int32
		load := func(context.Context) (int, error) {
			loads.Add(1)
			<-release
			return 7, nil
		}
		callerCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		callerDone := make(chan error, 1)
		go func() {
			_, err := cache.get(callerCtx, "key", load)
			callerDone <- err
		}()
		Eventually(loads.Load).Should(Equal(int32(1)))
		cancel()
		select {
		case err := <-callerDone:
			Expect(err).To(MatchError(context.Canceled))
		case <-time.After(5 * time.Second):
			Fail("the cancelled caller did not return")
		}
		close(release)
		Eventually(func() bool {
			_, cached := cache.snapshot("key")
			return cached
		}).Should(BeTrue(), "the detached fill should still complete and publish its value")
		value, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads.Load()).To(Equal(int32(1)))
	})

	It("fails an over-budget fill with its deadline and caches nothing", func() {
		cache := newTTLCache[string, int](time.Minute, 50*time.Millisecond, 8, func(key string) string { return key })
		var loads atomic.Int32
		load := func(ctx context.Context) (int, error) {
			loads.Add(1)
			<-ctx.Done() // hold the fill until its own budget expires
			return 0, ctx.Err()
		}
		_, err := cache.get(context.Background(), "key", load)
		Expect(err).To(MatchError(context.DeadlineExceeded))
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeFalse(), "an over-budget fill must not be cached")
		value, err := cache.get(context.Background(), "key", func(context.Context) (int, error) {
			loads.Add(1)
			return 7, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads.Load()).To(Equal(int32(2)))
	})

	It("clamps a fill budget larger than half the TTL", func() {
		cache := newTTLCache[string, int](time.Minute, time.Hour, 8, func(key string) string { return key })
		Expect(cache.fillBudget).To(Equal(30 * time.Second))
		var remaining time.Duration
		value, err := cache.get(context.Background(), "key", func(ctx context.Context) (int, error) {
			deadline, ok := ctx.Deadline()
			Expect(ok).To(BeTrue())
			remaining = time.Until(deadline)
			return 7, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(remaining).To(BeNumerically("<=", 30*time.Second))
		Expect(remaining).To(BeNumerically(">", 20*time.Second),
			"the fill must carry the clamped budget, not the caller's hour")
	})

	It("rejects a successful load that returns after the fill budget expired", func() {
		cache := newTTLCache[string, int](time.Minute, 50*time.Millisecond, 8, func(key string) string { return key })
		var loads atomic.Int32
		// A load that ignores its context still finishes successfully after the fill deadline. Returning success
		// here is deterministic: the load only proceeds once the fill context is already done.
		lateLoad := func(ctx context.Context) (int, error) {
			loads.Add(1)
			<-ctx.Done()
			return 7, nil
		}
		value, err := cache.get(context.Background(), "key", lateLoad)
		Expect(value).To(BeZero(), "a late success must not be served as a value")
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "got: %v", err)
		Expect(err.Error()).To(ContainSubstring("budget"))
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeFalse(), "a load that returns after the fill budget must not be cached")
		// The rejected flight must not stay wedged: the next get runs a fresh fill and caches its value.
		value, err = cache.get(context.Background(), "key", func(context.Context) (int, error) {
			loads.Add(1)
			return 9, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(9))
		Expect(loads.Load()).To(Equal(int32(2)), "the next get must retry the load, not reuse the late success")
		value, cached = cache.snapshot("key")
		Expect(cached).To(BeTrue(), "the retried fill must cache its value")
		Expect(value).To(Equal(9))
	})

	It("rejects a successful load whose publication waits past the fill deadline", func() {
		cache := newTTLCache[string, int](time.Minute, 250*time.Millisecond, 8, func(key string) string { return key })
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		callerCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		DeferCleanup(cancel)
		var loads atomic.Int32
		var loadCtxErr error
		value, err := cache.get(callerCtx, "key", func(ctx context.Context) (int, error) {
			loads.Add(1)
			// Return successfully within budget, but hold the publication mutex until the fill deadline expires.
			cache.mu.Lock()
			go func() {
				select {
				case <-ctx.Done():
				case <-release: // release the mutex even if the spec fails before the fill finishes
				}
				cache.mu.Unlock()
			}()
			loadCtxErr = ctx.Err()
			return 7, nil
		})
		Expect(loadCtxErr).ToNot(HaveOccurred(), "the load must succeed before its fill deadline")
		Expect(value).To(BeZero(), "a fill that expires waiting to publish must not serve its value")
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "got: %v", err)
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeFalse(), "a fill that expires waiting to publish must not be cached")
		value, err = cache.get(callerCtx, "key", func(context.Context) (int, error) {
			loads.Add(1)
			return 9, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(9))
		Expect(loads.Load()).To(Equal(int32(2)), "the next get must retry instead of reusing the expired fill")
		value, cached = cache.snapshot("key")
		Expect(cached).To(BeTrue(), "the retried fill must cache its value")
		Expect(value).To(Equal(9))
	})

	It("stamps an entry's TTL at store time, not at fill start", func() {
		cache := newTTLCache[string, int](time.Minute, time.Hour, 8, func(key string) string { return key })
		current := time.Unix(0, 0)
		cache.now = func() time.Time { return current }
		loads := 0
		load := func(context.Context) (int, error) {
			loads++
			// Only the cache clock moves here, by more than the clamped 30s budget but less than the TTL: the fill's
			// real deadline stays live, so the load completes within budget and the store stamps a fresh full TTL.
			current = current.Add(45 * time.Second)
			return 7, nil
		}
		value, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeTrue(), "a fill past the clamp must not store an already expired entry")
		value, err = cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads).To(Equal(1), "the entry must serve for the full TTL measured from the store")
		current = current.Add(time.Minute)
		_, err = cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(loads).To(Equal(2))
	})

	It("keeps a fill budget below the clamp unchanged", func() {
		cache := newTTLCache[string, int](time.Minute, 10*time.Second, 8, func(key string) string { return key })
		Expect(cache.fillBudget).To(Equal(10 * time.Second))
	})

	It("keeps a positive budget for a TTL too short to halve", func() {
		cache := newTTLCache[string, int](time.Nanosecond, time.Hour, 8, func(key string) string { return key })
		Expect(cache.fillBudget).To(Equal(time.Nanosecond))
	})

	It("replaces a zero requested budget with the positive limit", func() {
		cache := newTTLCache[string, int](time.Minute, 0, 8, func(key string) string { return key })
		Expect(cache.fillBudget).To(Equal(30 * time.Second))
	})

	It("keeps the requested budget when the TTL is not positive", func() {
		cache := newTTLCache[string, int](0, 10*time.Second, 8, func(key string) string { return key })
		Expect(cache.fillBudget).To(Equal(10*time.Second),
			"a non-positive TTL expires every entry on store, so the requested budget is unchanged")
	})

	It("returns a fill panic as an error and caches nothing", func() {
		cache, _ := newTestCache(8)
		var loads atomic.Int32
		load := func(context.Context) (int, error) {
			if loads.Add(1) == 1 {
				panic("boom")
			}
			return 7, nil
		}
		_, err := cache.get(context.Background(), "key", load)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"))
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeFalse(), "a panicked fill must not be cached")
		value, err := cache.get(context.Background(), "key", load)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads.Load()).To(Equal(int32(2)))
	})

	// newSingleSlotCache returns a cache with exactly one fill slot and a short fill budget, so a leaked slot fails a
	// spec with an ordinary budget error instead of waiting out the default budget.
	newSingleSlotCache := func() *ttlCache[string, int] {
		cache := newTTLCache[string, int](time.Minute, 100*time.Millisecond, 8, func(key string) string { return key })
		cache.fillLimiter = semaphore.NewWeighted(1)
		return cache
	}

	It("shares one fill limiter across caches by default", func() {
		cache, _ := newTestCache(8)
		Expect(cache.fillLimiter).To(BeIdenticalTo(ttlCacheFillLimiter),
			"every cache must draw its fill slots from the one process-wide limiter")
		// The admin guide documents this number, so a change to it must be deliberate.
		Expect(ttlCacheFillConcurrency).To(Equal(4))
	})

	It("holds a second key's fill until the running fill releases its only slot", func() {
		var keyStringCalls atomic.Int32
		cache := newTTLCache[string, int](time.Minute, 30*time.Second, 8, newCountingKeyString(&keyStringCalls))
		cache.fillLimiter = semaphore.NewWeighted(1)
		track := &fillTracker{}
		releaseFirst := make(chan struct{})
		firstStarted := make(chan struct{})
		firstDone := make(chan error, 1)
		go func() {
			_, err := cache.get(context.Background(), "one", func(context.Context) (int, error) {
				track.start("one")
				defer track.finish("one")
				close(firstStarted)
				<-releaseFirst
				return 1, nil
			})
			firstDone <- err
		}()
		<-firstStarted
		secondDone := make(chan error, 1)
		go func() {
			_, err := cache.get(context.Background(), "two", func(context.Context) (int, error) {
				track.start("two")
				defer track.finish("two")
				return 2, nil
			})
			secondDone <- err
		}()
		// A counted second keyString call proves the second get committed to its own flight; with one slot its fill
		// can only start once the first fill releases the slot below.
		Eventually(keyStringCalls.Load).Should(Equal(int32(2)))
		close(releaseFirst)
		for _, done := range []chan error{firstDone, secondDone} {
			select {
			case err := <-done:
				Expect(err).ToNot(HaveOccurred())
			case <-time.After(5 * time.Second):
				Fail("a get did not return")
			}
		}
		events, peak := track.snapshot()
		Expect(events).To(Equal([]string{"one:start", "one:finish", "two:start", "two:finish"}),
			"a different key's fill must not run while the running fill holds the only slot")
		Expect(peak).To(Equal(1), "fills must not overlap when the limiter has one slot")
		for key, want := range map[string]int{"one": 1, "two": 2} {
			value, err := cache.get(context.Background(), key, func(context.Context) (int, error) {
				return 0, errors.New("a cached key must not load again")
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal(want), "both fills must have cached their values")
		}
	})

	It("releases the fill slot when a fill fails", func() {
		cache := newSingleSlotCache()
		failure := errors.New("load unavailable")
		_, err := cache.get(context.Background(), "fails", func(context.Context) (int, error) {
			return 0, failure
		})
		Expect(err).To(MatchError(failure))
		// The only slot must be free again: a leaked slot makes this fill miss its 100ms budget and fail.
		value, err := cache.get(context.Background(), "succeeds", func(context.Context) (int, error) { return 7, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
	})

	It("releases the fill slot when a fill panics", func() {
		cache := newSingleSlotCache()
		_, err := cache.get(context.Background(), "panics", func(context.Context) (int, error) {
			panic("boom")
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"))
		// The only slot must be free again: a leaked slot makes this fill miss its 100ms budget and fail.
		value, err := cache.get(context.Background(), "succeeds", func(context.Context) (int, error) { return 7, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
	})

	It("fails a fill that cannot get a slot within its budget and caches nothing", func() {
		cache := newSingleSlotCache()
		// Hold the only slot, as a fill that is still running would.
		Expect(cache.fillLimiter.Acquire(context.Background(), 1)).To(Succeed())
		var loads atomic.Int32
		_, err := cache.get(context.Background(), "key", func(context.Context) (int, error) {
			loads.Add(1)
			return 7, nil
		})
		Expect(err).To(HaveOccurred(), "a fill that never gets a slot must fail its caller")
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(),
			"the wait is bounded by the fill budget, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("fill slot"))
		Expect(loads.Load()).To(BeZero(), "the load must not run without a slot")
		_, cached := cache.snapshot("key")
		Expect(cached).To(BeFalse(), "a fill that never ran must cache nothing")
		cache.fillLimiter.Release(1)
		// The failed flight must not stay wedged: once a slot is free the next get runs a fresh fill and caches it.
		value, err := cache.get(context.Background(), "key", func(context.Context) (int, error) {
			loads.Add(1)
			return 7, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(7))
		Expect(loads.Load()).To(Equal(int32(1)))
		value, cached = cache.snapshot("key")
		Expect(cached).To(BeTrue(), "the successful fill must cache its value")
		Expect(value).To(Equal(7))
	})
})
