package adapters

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("orderTracker", func() {
	var tracker *orderTracker

	BeforeEach(func() {
		tracker = newOrderTracker(100 * time.Millisecond)
	})

	Describe("check", func() {
		It("returns false for a new resource ID", func() {
			t := time.Now()
			Expect(tracker.check("res-1", t)).To(BeFalse())
		})

		It("returns false when events arrive in order", func() {
			t1 := time.Now()
			t2 := t1.Add(10 * time.Second)

			Expect(tracker.check("res-1", t1)).To(BeFalse())
			Expect(tracker.check("res-1", t2)).To(BeFalse())
		})

		It("returns true when an event arrives out of order", func() {
			t1 := time.Now()
			t2 := t1.Add(10 * time.Second)

			Expect(tracker.check("res-1", t2)).To(BeFalse())
			Expect(tracker.check("res-1", t1)).To(BeTrue())
		})

		It("tracks resources independently", func() {
			t1 := time.Now()
			t2 := t1.Add(10 * time.Second)

			Expect(tracker.check("res-1", t2)).To(BeFalse())
			Expect(tracker.check("res-2", t1)).To(BeFalse())
		})

		It("updates last-seen time on newer events", func() {
			t1 := time.Now()
			t2 := t1.Add(10 * time.Second)
			t3 := t1.Add(5 * time.Second)

			Expect(tracker.check("res-1", t1)).To(BeFalse())
			Expect(tracker.check("res-1", t2)).To(BeFalse())
			// t3 is between t1 and t2 — still out of order relative to t2
			Expect(tracker.check("res-1", t3)).To(BeTrue())
		})
	})

	Describe("TTL eviction", func() {
		It("evicts expired entries", func() {
			tracker.check("res-1", time.Now())
			Expect(tracker.len()).To(Equal(1))

			time.Sleep(150 * time.Millisecond)
			tracker.evictExpired()
			Expect(tracker.len()).To(Equal(0))
		})

		It("preserves non-expired entries", func() {
			tracker.check("res-1", time.Now())
			tracker.evictExpired()
			Expect(tracker.len()).To(Equal(1))
		})

		It("uses wall-clock time for eviction, not event time", func() {
			// A backdated transition_time should not cause premature eviction;
			// eviction is based on when the tracker last saw the resource.
			tracker.check("res-1", time.Now().Add(-time.Hour))
			tracker.evictExpired()
			Expect(tracker.len()).To(Equal(1))
		})
	})

	Describe("concurrent safety", func() {
		It("handles concurrent checks without panics", func() {
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					tracker.check("res-1", time.Now().Add(time.Duration(i)*time.Second))
				}(i)
			}
			wg.Wait()
		})
	})
})
