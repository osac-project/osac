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
	// newTestCache returns a string-keyed cache with a one-minute TTL whose clock is the addressable variable now,
	// so a spec can advance time.
	newTestCache := func(maxEntries int) (cache *ttlCache[string, int], now *time.Time) {
		cache = newTTLCache[string, int](time.Minute, maxEntries, func(key string) string { return key })
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
})
