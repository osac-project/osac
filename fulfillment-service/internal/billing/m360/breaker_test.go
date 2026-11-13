/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pricing breaker", func() {
	var (
		b   *breaker
		now time.Time
	)

	BeforeEach(func() {
		now = time.Now()
		b = newBreaker(5, 30*time.Second, 20*time.Second)
		b.now = func() time.Time { return now }
	})

	// fail records one failed call, spacing it after the previous one.
	fail := func(spacing time.Duration) {
		now = now.Add(spacing)
		ok, probe := b.allow()
		ExpectWithOffset(1, ok).To(BeTrue(), "the breaker must admit the call")
		b.failure(probe)
	}
	admits := func() bool {
		ok, _ := b.allow()
		return ok
	}

	It("opens after 5 failures inside the window", func() {
		for range 5 {
			fail(7 * time.Second)
		}
		Expect(admits()).To(BeFalse())
	})

	It("stays closed when the failures are spread beyond the window", func() {
		for range 10 {
			fail(8 * time.Second)
		}
		Expect(admits()).To(BeTrue())
	})

	It("forgets the failures before a success", func() {
		for range 4 {
			fail(0)
		}
		_, probe := b.allow()
		b.success(probe)
		for range 4 {
			fail(0)
		}
		Expect(admits()).To(BeTrue())
	})

	It("lets a single probe through after the open time and reopens when it fails", func() {
		for range 5 {
			fail(0)
		}
		// Past the window too, so the window holds no failure when the probe fails.
		now = now.Add(31 * time.Second)
		ok, probe := b.allow()
		Expect(ok).To(BeTrue())
		Expect(probe).To(BeTrue())
		Expect(admits()).To(BeFalse(), "only one probe at a time")

		b.failure(probe)
		Expect(admits()).To(BeFalse(), "a failed probe reopens the breaker")
		now = now.Add(19 * time.Second)
		Expect(admits()).To(BeFalse())
		now = now.Add(time.Second)
		Expect(admits()).To(BeTrue())
	})

	It("admits the next call as the new probe after the client's probe is cancelled", func() {
		for range 5 {
			fail(0)
		}
		now = now.Add(20 * time.Second)
		fake := newFakeServer(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
		defer fake.expectOnlyGET()
		c := testClient(fake.URL)
		c.breaker = b
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(c.get(ctx, "op", "/x", nil, nil)).To(HaveOccurred())

		Expect(c.get(context.Background(), "op", "/x", nil, &struct{}{})).To(Succeed(),
			"a cancelled probe must not leave the breaker open forever")
		Expect(admits()).To(BeTrue(), "the new probe's success closes the breaker")
	})

	It("closes on the probe's success, not on a call admitted before it opened", func() {
		ok, early := b.allow()
		Expect(ok).To(BeTrue())
		for range 5 {
			fail(0)
		}
		b.success(early)
		Expect(admits()).To(BeFalse(), "a call that started before the trip must not close the breaker")

		now = now.Add(20 * time.Second)
		_, probe := b.allow()
		b.success(probe)
		Expect(admits()).To(BeTrue())
		Expect(admits()).To(BeTrue())
	})
})
