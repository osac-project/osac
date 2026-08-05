// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adapters

import (
	"context"
	"errors"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Override sleepFunc to skip real sleeps in all tests.
// This is the only BeforeSuite in the package.
var _ = BeforeSuite(func() {
	sleepFunc = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
})

var _ = Describe("calculateBackoff", func() {
	It("returns 1s base for attempt 0", func() {
		b := calculateBackoff(0)
		// ±25% jitter: 750ms to 1250ms
		Expect(b).To(BeNumerically(">=", 750*time.Millisecond))
		Expect(b).To(BeNumerically("<=", 1250*time.Millisecond))
	})

	It("doubles on each subsequent attempt", func() {
		// attempt 1 → 2s base, attempt 2 → 4s base
		b1 := calculateBackoff(1)
		Expect(b1).To(BeNumerically(">=", 1500*time.Millisecond))
		Expect(b1).To(BeNumerically("<=", 2500*time.Millisecond))

		b2 := calculateBackoff(2)
		Expect(b2).To(BeNumerically(">=", 3*time.Second))
		Expect(b2).To(BeNumerically("<=", 5*time.Second))
	})

	It("caps at 5 minutes", func() {
		// attempt 9 would be 512s without cap; capped at 300s (5m)
		b := calculateBackoff(9)
		Expect(b).To(BeNumerically("<=", 5*time.Minute+75*time.Second)) // 5m + 25% jitter
		Expect(b).To(BeNumerically(">=", 5*time.Minute-75*time.Second)) // 5m - 25% jitter
	})

	It("stays capped for very high attempts", func() {
		b := calculateBackoff(20)
		Expect(b).To(BeNumerically("<=", 5*time.Minute+75*time.Second))
	})
})

var _ = Describe("submitWithRetry", func() {
	var (
		adapter *retryTestAdapter
		event   MeteringEvent
		logger  logr.Logger
	)

	BeforeEach(func() {
		adapter = &retryTestAdapter{name: "test"}
		ce := cloudevents.NewEvent()
		ce.SetID("retry-test")
		ce.SetType("osac.test.v1")
		ce.SetSource("test")
		event = MeteringEvent{CloudEvent: ce, Topic: "t", Partition: 0, Offset: 0}
		logger = logr.Discard()
	})

	It("returns success on first attempt when Submit succeeds", func() {
		result := submitWithRetry(context.Background(), adapter, event, 3, logger)
		Expect(result.Err).NotTo(HaveOccurred())
		Expect(result.Attempts).To(Equal(1))
		Expect(adapter.calls).To(Equal(1))
	})

	It("skips immediately on NonRetryableError", func() {
		adapter.errs = []error{
			&NonRetryableError{Err: errors.New("bad schema")},
		}
		result := submitWithRetry(context.Background(), adapter, event, 3, logger)
		Expect(result.Err).To(HaveOccurred())
		Expect(result.NonRetryable).To(BeTrue())
		Expect(result.Attempts).To(Equal(1))
		Expect(adapter.calls).To(Equal(1))
	})

	It("retries on retryable errors and succeeds", func() {
		adapter.errs = []error{
			errors.New("transient-1"),
			errors.New("transient-2"),
			nil, // third attempt succeeds
		}
		result := submitWithRetry(context.Background(), adapter, event, 5, logger)
		Expect(result.Err).NotTo(HaveOccurred())
		Expect(result.Attempts).To(Equal(3))
		Expect(adapter.calls).To(Equal(3))
	})

	It("returns exhausted after max retries", func() {
		adapter.errs = []error{
			errors.New("fail-1"),
			errors.New("fail-2"),
			errors.New("fail-3"),
		}
		result := submitWithRetry(context.Background(), adapter, event, 3, logger)
		Expect(result.Err).To(HaveOccurred())
		Expect(result.Exhausted).To(BeTrue())
		Expect(result.Attempts).To(Equal(3))
	})

	It("clamps maxRetries to 1 and still calls Submit", func() {
		result := submitWithRetry(context.Background(), adapter, event, 0, logger)
		Expect(result.Err).NotTo(HaveOccurred())
		Expect(result.Attempts).To(Equal(1))
		Expect(adapter.calls).To(Equal(1))
	})

	It("stops retrying when context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		adapter.errs = []error{errors.New("fail")}
		adapter.onSubmit = func() { cancel() }

		result := submitWithRetry(ctx, adapter, event, 10, logger)
		Expect(result.Err).To(HaveOccurred())
		Expect(result.Attempts).To(Equal(1))
	})
})

// retryTestAdapter is a mock ProviderAdapter for retry tests.
// It returns errors from the errs slice in order; once exhausted, returns nil.
type retryTestAdapter struct {
	name     string
	errs     []error
	calls    int
	onSubmit func()
}

func (a *retryTestAdapter) Name() string { return a.name }
func (a *retryTestAdapter) Submit(_ context.Context, _ MeteringEvent) error {
	a.calls++
	if a.onSubmit != nil {
		a.onSubmit()
	}
	if a.calls-1 < len(a.errs) {
		return a.errs[a.calls-1]
	}
	return nil
}
func (a *retryTestAdapter) Flush(_ context.Context) (SubmitResult, error) { return SubmitResult{}, nil }
func (a *retryTestAdapter) HealthCheck(_ context.Context) error           { return nil }
func (a *retryTestAdapter) Close() error                                  { return nil }
