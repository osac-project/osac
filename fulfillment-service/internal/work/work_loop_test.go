/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package work

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Loop", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	It("Fails if name is missing", func() {
		loop, err := NewLoop().
			SetLogger(logger).
			SetInterval(1 * time.Hour).
			SetWorkFunc(func(ctx context.Context) error {
				return nil
			}).
			Build()
		Expect(err).To(HaveOccurred())
		Expect(loop).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("name is mandatory"))
	})

	It("Runs the function repeatedly", func() {
		// Create a channel to signal that the function has run:
		ran := make(chan struct{})

		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(10 * time.Millisecond).
			SetWorkFunc(func(ctx context.Context) error {
				ran <- struct{}{}
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Verify that the function runs multiple times:
		Eventually(ran).Should(Receive())
		Eventually(ran).Should(Receive())
		Eventually(ran).Should(Receive())
	})

	It("Stops when the context is cancelled", func() {
		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(1 * time.Hour).
			SetWorkFunc(func(ctx context.Context) error {
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
			close(done)
		}()

		// Cancel the context and verify that the loop stops:
		cancel()
		Eventually(done).Should(BeClosed())
	})

	It("Logs errors returned by the function", func() {
		// Create a channel to signal that the function has run:
		ran := make(chan struct{})

		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(10 * time.Millisecond).
			SetWorkFunc(func(ctx context.Context) error {
				defer func() {
					ran <- struct{}{}
				}()
				return errors.New("test error")
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Verify that the function runs (even if it errors):
		Eventually(ran).Should(Receive())
		Eventually(ran).Should(Receive())
	})

	It("Respects the sleep interval", func() {
		// Create a channel to record the time of each run:
		times := make(chan time.Time, 10)

		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(100 * time.Millisecond).
			SetWorkFunc(func(ctx context.Context) error {
				times <- time.Now()
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Get two execution times:
		var t1, t2 time.Time
		Eventually(times).Should(Receive(&t1))
		Eventually(times).Should(Receive(&t2))

		// Verify that the interval between runs is approximately correct:
		Expect(t2.Sub(t1)).To(BeNumerically(">=", 100*time.Millisecond))
	})

	It("Subtracts execution time from sleep interval", func() {
		// Create a channel to record the time of each run:
		times := make(chan time.Time, 10)

		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(100 * time.Millisecond).
			SetWorkFunc(func(ctx context.Context) error {
				times <- time.Now()
				time.Sleep(50 * time.Millisecond)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Get two execution times:
		var t1, t2 time.Time
		Eventually(times).Should(Receive(&t1))
		Eventually(times).Should(Receive(&t2))

		// Verify that the interval between start times is still approximately the interval,
		// meaning it slept interval - execution time.
		// t2 - t1 should be close to 100ms.
		// If it didn't subtract, it would be 100ms + 50ms = 150ms.
		diff := t2.Sub(t1)
		Expect(diff).To(BeNumerically(">=", 100*time.Millisecond))
		Expect(diff).To(BeNumerically("<", 150*time.Millisecond))
	})

	It("Does not sleep if execution takes longer than interval", func() {
		// Create a channel to record the time of each run:
		times := make(chan time.Time, 10)

		// Create the loop:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(50 * time.Millisecond).
			SetWorkFunc(func(ctx context.Context) error {
				times <- time.Now()
				time.Sleep(100 * time.Millisecond)
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Get two execution times:
		var t1, t2 time.Time
		Eventually(times).Should(Receive(&t1))
		Eventually(times).Should(Receive(&t2))

		// Verify that the interval between start times is roughly the execution time (no extra sleep).
		diff := t2.Sub(t1)
		Expect(diff).To(BeNumerically(">=", 100*time.Millisecond))
		// Allow some small overhead, but it shouldn't be 150ms.
		Expect(diff).To(BeNumerically("<", 120*time.Millisecond))
	})

	It("Wakes up immediately when kicked", func() {
		// Create a channel to signal that the function has run:
		ran := make(chan struct{})

		// Create the loop with a long interval:
		loop, err := NewLoop().
			SetLogger(logger).
			SetName("test").
			SetInterval(1 * time.Hour).
			SetWorkFunc(func(ctx context.Context) error {
				ran <- struct{}{}
				return nil
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the loop in the background:
		go func() {
			defer GinkgoRecover()
			err = loop.Run(ctx)
			Expect(err).To(MatchError(context.Canceled))
		}()

		// Wait for the first run:
		Eventually(ran).Should(Receive())

		// Kick the loop:
		loop.Kick()

		// Verify that it runs again immediately:
		Eventually(ran).Should(Receive())
	})
})
