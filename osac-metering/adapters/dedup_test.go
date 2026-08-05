/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("dedupCache", func() {
	var cache *dedupCache

	BeforeEach(func() {
		cache = newDedupCache(100 * time.Millisecond)
	})

	Describe("contains and add", func() {
		It("returns false for an unknown ID", func() {
			Expect(cache.contains("unknown")).To(BeFalse())
		})

		It("returns true after adding an ID", func() {
			cache.add("event-1")
			Expect(cache.contains("event-1")).To(BeTrue())
		})

		It("suppresses duplicate IDs", func() {
			cache.add("event-1")
			Expect(cache.contains("event-1")).To(BeTrue())
			Expect(cache.contains("event-1")).To(BeTrue())
		})

		It("tracks multiple IDs independently", func() {
			cache.add("event-1")
			cache.add("event-2")
			Expect(cache.contains("event-1")).To(BeTrue())
			Expect(cache.contains("event-2")).To(BeTrue())
			Expect(cache.contains("event-3")).To(BeFalse())
		})
	})

	Describe("TTL expiry", func() {
		It("returns false for expired entries", func() {
			cache.add("event-1")
			Expect(cache.contains("event-1")).To(BeTrue())

			time.Sleep(150 * time.Millisecond)
			Expect(cache.contains("event-1")).To(BeFalse())
		})
	})

	Describe("evictExpired", func() {
		It("removes expired entries from the map", func() {
			cache.add("event-1")
			Expect(cache.len()).To(Equal(1))

			time.Sleep(150 * time.Millisecond)
			cache.evictExpired()
			Expect(cache.len()).To(Equal(0))
		})

		It("preserves non-expired entries", func() {
			cache.add("event-1")
			cache.evictExpired()
			Expect(cache.len()).To(Equal(1))
		})
	})

	Describe("concurrent safety", func() {
		It("handles concurrent add and contains without panics", func() {
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(2)
				id := "event-" + string(rune('0'+i%10))
				go func() {
					defer wg.Done()
					cache.add(id)
				}()
				go func() {
					defer wg.Done()
					_ = cache.contains(id)
				}()
			}
			wg.Wait()
		})
	})
})
