/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package wait_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/wait"
)

// fakeResource is a minimal stand-in for any resource type.
type fakeResource struct {
	state string
}

// stateOf extracts the state field.
func stateOf(r fakeResource) string { return r.state }

// seqFetch returns a FetchFunc that emits states[0], states[1], ... in order.
// It returns an error if called more times than there are states.
func seqFetch(states []string) wait.FetchFunc[fakeResource] {
	i := 0
	return func(_ context.Context) (fakeResource, error) {
		if i >= len(states) {
			return fakeResource{}, fmt.Errorf("seqFetch: called %d times, only %d states defined", i+1, len(states))
		}
		r := fakeResource{state: states[i]}
		i++
		return r, nil
	}
}

// fastOpts returns Options with millisecond timing for test speed.
func fastOpts(fetch wait.FetchFunc[fakeResource], target string) wait.Options[fakeResource] {
	return wait.Options[fakeResource]{
		Fetch:       fetch,
		StateOf:     stateOf,
		TargetState: target,
		Timeout:     5 * time.Second,
		Interval:    time.Millisecond,
		MaxInterval: time.Millisecond,
	}
}

var _ = Describe("Poll", func() {

	Describe("success paths", func() {
		It("returns immediately when first fetch already shows the target state", func() {
			opts := fastOpts(seqFetch([]string{"ALLOCATED"}), "ALLOCATED")
			result, err := wait.Poll(context.Background(), opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.state).To(Equal("ALLOCATED"))
		})

		It("returns the resource after several polls when the target state is eventually reached", func() {
			opts := fastOpts(seqFetch([]string{"PENDING", "PENDING", "ALLOCATED"}), "ALLOCATED")
			result, err := wait.Poll(context.Background(), opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.state).To(Equal("ALLOCATED"))
		})
	})

	Describe("failure paths", func() {
		It("returns ErrTerminalState immediately when a failure state is detected", func() {
			opts := fastOpts(seqFetch([]string{"PENDING", "FAILED"}), "ALLOCATED")
			opts.FailureStates = []string{"FAILED", "ERROR"}
			_, err := wait.Poll(context.Background(), opts)
			Expect(err).To(HaveOccurred())
			var termErr *wait.ErrTerminalState
			Expect(errors.As(err, &termErr)).To(BeTrue(), "expected ErrTerminalState, got %T: %v", err, err)
			Expect(termErr.State).To(Equal("FAILED"))
			Expect(termErr.Error()).To(ContainSubstring("FAILED"))
		})

		It("returns ErrTerminalState for every listed failure state", func() {
			for _, fs := range []string{"FAILED", "ERROR"} {
				opts := fastOpts(seqFetch([]string{fs}), "ALLOCATED")
				opts.FailureStates = []string{"FAILED", "ERROR"}
				_, err := wait.Poll(context.Background(), opts)
				var termErr *wait.ErrTerminalState
				Expect(errors.As(err, &termErr)).To(BeTrue(), "expected ErrTerminalState for state %q", fs)
				Expect(termErr.State).To(Equal(fs))
			}
		})

		It("returns ErrTimeout when the timeout fires before the target state is reached", func() {
			steady := func(_ context.Context) (fakeResource, error) {
				return fakeResource{state: "PENDING"}, nil
			}
			opts := wait.Options[fakeResource]{
				Fetch:       steady,
				StateOf:     stateOf,
				TargetState: "ALLOCATED",
				Timeout:     30 * time.Millisecond,
				Interval:    5 * time.Millisecond,
				MaxInterval: 5 * time.Millisecond,
			}
			_, err := wait.Poll(context.Background(), opts)
			Expect(err).To(HaveOccurred())
			var toErr *wait.ErrTimeout
			Expect(errors.As(err, &toErr)).To(BeTrue(), "expected ErrTimeout, got %T: %v", err, err)
			Expect(toErr.Current).To(Equal("PENDING"))
			Expect(toErr.Error()).To(ContainSubstring("PENDING"))
		})

		It("propagates fetch errors directly without wrapping", func() {
			sentinel := errors.New("connection refused")
			errFetch := func(_ context.Context) (fakeResource, error) {
				return fakeResource{}, sentinel
			}
			_, err := wait.Poll(context.Background(), fastOpts(errFetch, "ALLOCATED"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, sentinel)).To(BeTrue())
		})

		It("stops polling when the caller cancels the context", func() {
			ctx, cancel := context.WithCancel(context.Background())
			calls := 0
			cancelFetch := func(_ context.Context) (fakeResource, error) {
				calls++
				if calls >= 2 {
					cancel()
				}
				return fakeResource{state: "PENDING"}, nil
			}
			opts := wait.Options[fakeResource]{
				Fetch:       cancelFetch,
				StateOf:     stateOf,
				TargetState: "ALLOCATED",
				Timeout:     10 * time.Second,
				Interval:    time.Millisecond,
				MaxInterval: time.Millisecond,
			}
			_, err := wait.Poll(ctx, opts)
			Expect(err).To(HaveOccurred())
			Expect(calls).To(BeNumerically(">=", 2))
		})
	})

	Describe("OnProgress callback", func() {
		It("is called for every fetch including the first", func() {
			opts := fastOpts(seqFetch([]string{"PENDING", "ALLOCATING", "ALLOCATED"}), "ALLOCATED")
			var seen []string
			opts.OnProgress = func(r fakeResource) { seen = append(seen, r.state) }
			_, err := wait.Poll(context.Background(), opts)
			Expect(err).NotTo(HaveOccurred())
			Expect(seen).To(Equal([]string{"PENDING", "ALLOCATING", "ALLOCATED"}))
		})

		It("is called on the failure state before the error is returned", func() {
			opts := fastOpts(seqFetch([]string{"PENDING", "FAILED"}), "ALLOCATED")
			opts.FailureStates = []string{"FAILED"}
			var seen []string
			opts.OnProgress = func(r fakeResource) { seen = append(seen, r.state) }
			_, _ = wait.Poll(context.Background(), opts)
			Expect(seen).To(ContainElement("FAILED"))
		})
	})

	Describe("defaults", func() {
		It("does not panic when Timeout is zero (context deadline overrides)", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			steady := func(_ context.Context) (fakeResource, error) {
				return fakeResource{state: "PENDING"}, nil
			}
			_, err := wait.Poll(ctx, wait.Options[fakeResource]{
				Fetch:       steady,
				StateOf:     stateOf,
				TargetState: "ALLOCATED",
				// Timeout: 0 → defaults to 5m, but ctx cancels in 20ms
				Interval:    time.Millisecond,
				MaxInterval: time.Millisecond,
			})
			Expect(err).To(HaveOccurred())
		})
	})
})
