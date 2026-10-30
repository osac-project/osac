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
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

const testToken = "super-secret-token"

type fakeServer struct {
	*httptest.Server
	hits    atomic.Int32
	methods chan string
}

// newFakeServer starts a server that counts hits and records request methods.
func newFakeServer(h http.HandlerFunc) *fakeServer {
	f := &fakeServer{methods: make(chan string, 1000)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.methods <- r.Method
		h(w, r)
	}))
	return f
}

// expectOnlyGET closes the fake and fails the spec if the adapter ever sent anything but a GET.
func (f *fakeServer) expectOnlyGET() {
	f.Close()
	close(f.methods)
	for m := range f.methods {
		ExpectWithOffset(1, m).To(Equal(http.MethodGet), "only GET may ever be sent")
	}
}

// testClient builds a client against url with millisecond backoff.
func testClient(url string) *client {
	c := newClient(billing.Config{BaseURL: url, APIVersion: "v1", Credential: testToken}, logger)
	c.backoffBase = time.Millisecond
	c.backoffMax = 2 * time.Millisecond
	return c
}

// reasonOf asserts err is a *billing.Error and returns its Reason.
func reasonOf(err error) billing.Reason {
	var e *billing.Error
	ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue(), "error was %v", err)
	return e.Reason
}

var _ = Describe("M360 transport", func() {
	var fake *fakeServer

	AfterEach(func() {
		fake.expectOnlyGET()
	})

	It("sends bearer token and addresses the external API of the configured version", func() {
		var auth, path string
		fake = newFakeServer(func(w http.ResponseWriter, r *http.Request) {
			auth, path = r.Header.Get("Authorization"), r.URL.Path
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
		var out struct{ OK bool }
		Expect(testClient(fake.URL).get(context.Background(), "op", "/accounts", nil, &out)).To(Succeed())
		Expect(auth).To(Equal("Bearer " + testToken))
		Expect(path).To(Equal("/api/v1/external/accounts"))
		Expect(out.OK).To(BeTrue())
	})

	DescribeTable("classifies non-2xx responses",
		func(status int, reason billing.Reason, attempts int32) {
			fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
			err := testClient(fake.URL).get(context.Background(), "op", "/x", nil, nil)
			Expect(reasonOf(err)).To(Equal(reason))
			Expect(fake.hits.Load()).To(Equal(attempts))
		},
		Entry("401 not retried", 401, billing.ReasonUnauthorized, int32(1)),
		Entry("403 not retried", 403, billing.ReasonUnauthorized, int32(1)),
		Entry("409 not retried", 409, billing.ReasonConflict, int32(1)),
		Entry("400 not retried", 400, billing.ReasonMalformed, int32(1)),
		Entry("404 not retried", 404, billing.ReasonMalformed, int32(1)),
		Entry("500 retried to 4 attempts", 500, billing.ReasonUnavailable, int32(4)),
		Entry("503 retried to 4 attempts", 503, billing.ReasonUnavailable, int32(4)),
		Entry("429 retried to 4 attempts", 429, billing.ReasonUnavailable, int32(4)),
		Entry("408 retried to 4 attempts", 408, billing.ReasonTimeout, int32(4)),
	)

	It("recovers when a retry succeeds", func() {
		var n atomic.Int32
		fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) {
			if n.Add(1) < 3 {
				w.WriteHeader(502)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		})
		Expect(testClient(fake.URL).get(context.Background(), "op", "/x", nil, &struct{}{})).To(Succeed())
		Expect(fake.hits.Load()).To(Equal(int32(3)))
	})

	It("times out a slow attempt and retries it", func() {
		fake = newFakeServer(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		c := testClient(fake.URL)
		c.attemptTimeout = 20 * time.Millisecond
		err := c.get(context.Background(), "op", "/x", nil, nil)
		Expect(reasonOf(err)).To(Equal(billing.ReasonTimeout))
		Expect(fake.hits.Load()).To(Equal(int32(4)))
	})

	It("reports network failure as UNAVAILABLE", func() {
		fake = newFakeServer(func(http.ResponseWriter, *http.Request) {})
		c := testClient(fake.URL)
		fake.CloseClientConnections()
		fake.Close()
		Expect(reasonOf(c.get(context.Background(), "op", "/x", nil, nil))).To(Equal(billing.ReasonUnavailable))
	})

	It("stops retrying once the caller's context is done", func() {
		fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(testClient(fake.URL).get(ctx, "op", "/x", nil, nil)).To(HaveOccurred())
		Expect(fake.hits.Load()).To(BeNumerically("<=", 1))
	})

	DescribeTable("treats bad 2xx bodies as MALFORMED, never as empty data",
		func(body string) {
			fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			var out envelope[pageData[account]]
			err := testClient(fake.URL).get(context.Background(), "op", "/x", nil, &out)
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
			Expect(fake.hits.Load()).To(Equal(int32(1)))
		},
		Entry("not json", "<html>"),
		Entry("empty body", ""),
		Entry("wrong shape", `{"success":true,"errors":[],"data":{"content":"nope"}}`),
		Entry("error envelope", `{"error":{"code":"X","message":"boom"}}`),
		Entry("success false", `{"success":false,"errors":[],"data":{"content":[]}}`),
		Entry("success missing", `{"errors":[],"data":{"content":[]}}`),
		Entry("errors reported", `{"success":true,"errors":[{"code":"X"}],"data":{"content":[]}}`),
		Entry("data null", `{"success":true,"errors":[],"data":null}`),
		Entry("data missing", `{"success":true,"errors":[]}`),
		Entry("old Items shape", `{"Items":[{"Id":"a1"}],"NextPage":"2"}`),
	)

	It("does not follow redirects, so the token never leaves for another path", func() {
		var elsewhere atomic.Int32
		fake = newFakeServer(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				elsewhere.Add(1)
				_, _ = w.Write([]byte(`{"success":true,"errors":[],"data":{"content":[]}}`))
				return
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		})
		var out envelope[pageData[account]]
		err := testClient(fake.URL).get(context.Background(), "op", "/accounts", nil, &out)
		var e *billing.Error
		Expect(errors.As(err, &e)).To(BeTrue())
		Expect(e.Reason).To(Equal(billing.ReasonMalformed))
		Expect(e.Retryable).To(BeFalse())
		Expect(fake.hits.Load()).To(Equal(int32(1)))
		Expect(elsewhere.Load()).To(BeZero())
	})

	Describe("pricing breaker", func() {
		It("opens after 5 consecutive exhausted failures, then probes and recovers", func() {
			var fail atomic.Bool
			fail.Store(true)
			fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) {
				if fail.Load() {
					w.WriteHeader(500)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			})
			now := time.Now()
			c := testClient(fake.URL)
			c.breaker.now = func() time.Time { return now }
			for range 5 {
				Expect(reasonOf(c.get(context.Background(), "op", "/x", nil, nil))).To(Equal(billing.ReasonUnavailable))
			}
			before := fake.hits.Load()
			err := c.get(context.Background(), "op", "/x", nil, nil)
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
			Expect(fake.hits.Load()).To(Equal(before), "open breaker must not call the provider")

			now = now.Add(31 * time.Second)
			fail.Store(false)
			Expect(c.get(context.Background(), "op", "/x", nil, &struct{}{})).To(Succeed())
			Expect(c.get(context.Background(), "op", "/x", nil, &struct{}{})).To(Succeed())
		})

		It("is neither tripped by nor blocking account discovery", func() {
			var fail atomic.Bool
			fail.Store(true)
			fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) {
				if fail.Load() {
					w.WriteHeader(500)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			})
			c := testClient(fake.URL)
			for range 6 {
				Expect(reasonOf(c.getDiscovery(context.Background(), "op", "/accounts", nil, nil))).To(Equal(billing.ReasonUnavailable))
			}
			fail.Store(false)
			Expect(c.get(context.Background(), "op", "/services", nil, &struct{}{})).To(Succeed(), "pricing breaker must still be closed")

			fail.Store(true)
			for range 5 {
				Expect(c.get(context.Background(), "op", "/services", nil, nil)).To(HaveOccurred())
			}
			fail.Store(false)
			Expect(reasonOf(c.get(context.Background(), "op", "/services", nil, nil))).To(Equal(billing.ReasonUnavailable))
			Expect(c.getDiscovery(context.Background(), "op", "/accounts", nil, &struct{}{})).To(Succeed(), "open pricing breaker must not block accounts")
		})

		It("is not tripped by non-retryable client errors", func() {
			fake = newFakeServer(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
			c := testClient(fake.URL)
			for range 10 {
				Expect(reasonOf(c.get(context.Background(), "op", "/x", nil, nil))).To(Equal(billing.ReasonUnauthorized))
			}
			Expect(fake.hits.Load()).To(Equal(int32(10)))
		})
	})
})

var _ = Describe("M360 redaction", func() {
	It("keeps bodies, tokens and ids out of errors and logs", func() {
		const leak = "acct-native-9999"
		fake := newFakeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"code":"E","message":"` + leak + ` ` + testToken + `"}}`))
		})
		defer fake.Close()
		var logs strings.Builder
		c := testClient(fake.URL)
		c.logger = newCaptureLogger(&logs)
		err := c.get(context.Background(), "GetAccount", "/accounts/"+leak, nil, nil)
		Expect(err).To(HaveOccurred())
		for _, s := range []string{err.Error(), logs.String()} {
			Expect(s).ToNot(ContainSubstring(leak))
			Expect(s).ToNot(ContainSubstring(testToken))
			Expect(s).ToNot(ContainSubstring("boom"))
		}
		Expect(logs.String()).To(ContainSubstring("GetAccount"))
	})
})

// newCaptureLogger returns a debug-level logger writing to w.
func newCaptureLogger(w *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
