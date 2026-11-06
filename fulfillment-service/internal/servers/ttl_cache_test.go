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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
})
