/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package wait provides a generic polling utility for CLI commands that need to wait for resource
// state transitions (e.g., ExternalIP reaching ALLOCATED, ComputeInstance reaching RUNNING).
package wait

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FetchFunc retrieves the current state of a resource.
type FetchFunc[T any] func(ctx context.Context) (T, error)

// StateFunc extracts the state string from a resource.
type StateFunc[T any] func(T) string

// ProgressFunc is called after each fetch with the current resource value.
// Use it to print a progress line to the user, for example:
//
//	"Waiting for ExternalIP 'my-ip' to reach ALLOCATED... (current: PENDING)"
type ProgressFunc[T any] func(T)

// Options configures a [Poll] call.
type Options[T any] struct {
	// Fetch retrieves the current resource. Required.
	Fetch FetchFunc[T]

	// StateOf extracts the state string from the resource. Required.
	StateOf StateFunc[T]

	// TargetState is the state that signals success. Required.
	TargetState string

	// FailureStates are states that signal a non-recoverable failure.
	// Poll returns [ErrTerminalState] immediately upon entering one of these.
	FailureStates []string

	// Timeout caps the total wait time. Defaults to 5 minutes when zero.
	Timeout time.Duration

	// Interval is the initial delay between polls. Defaults to 2 seconds when zero.
	Interval time.Duration

	// MaxInterval caps the delay after exponential backoff. Defaults to 10 seconds when zero.
	MaxInterval time.Duration

	// OnProgress is called after every fetch (including the first).
	// Optional — pass nil to suppress progress output.
	OnProgress ProgressFunc[T]
}

// ErrTerminalState is returned when the resource enters a failure state listed in
// [Options.FailureStates].
type ErrTerminalState struct {
	// State is the failure state that was observed.
	State string
}

func (e *ErrTerminalState) Error() string {
	return fmt.Sprintf("reached terminal failure state %q", e.State)
}

// ErrTimeout is returned when [Options.Timeout] (or the caller's context deadline) expires
// before the resource reaches [Options.TargetState].
type ErrTimeout struct {
	// After is the timeout duration that was configured.
	After time.Duration
	// Current is the state the resource was in when the timeout fired.
	Current string
}

func (e *ErrTimeout) Error() string {
	return fmt.Sprintf(
		"timed out waiting for state transition: exceeded configured timeout of %s (current: %s)",
		e.After.Round(time.Second), e.Current,
	)
}

// Poll polls a resource until it reaches [Options.TargetState], a [Options.FailureStates] entry,
// or the context / [Options.Timeout] expires. It returns the final resource value on success.
//
// Backoff: the inter-poll delay starts at [Options.Interval] and doubles after each poll that does
// not reach the target state, capped at [Options.MaxInterval].
func Poll[T any](ctx context.Context, opts Options[T]) (T, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	maxInterval := opts.MaxInterval
	if maxInterval <= 0 {
		maxInterval = 10 * time.Second
	}
	if interval > maxInterval {
		interval = maxInterval
	}

	failureSet := make(map[string]bool, len(opts.FailureStates))
	for _, s := range opts.FailureStates {
		failureSet[s] = true
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last string
	for {
		resource, err := opts.Fetch(ctx)
		if err != nil {
			var zero T
			// A deadline error caused by the timeout context is a
			// timeout, whatever the fetch layer returned. A gRPC
			// call reports the deadline as a status error, which
			// does not wrap context.DeadlineExceeded.
			if deadlineExceeded(err) && ctx.Err() == context.DeadlineExceeded {
				return zero, &ErrTimeout{After: timeout, Current: last}
			}
			return zero, err
		}

		state := opts.StateOf(resource)
		last = state

		if opts.OnProgress != nil {
			opts.OnProgress(resource)
		}

		if state == opts.TargetState {
			return resource, nil
		}

		if failureSet[state] {
			var zero T
			return zero, &ErrTerminalState{State: state}
		}

		select {
		case <-ctx.Done():
			var zero T
			if ctx.Err() == context.Canceled {
				return zero, ctx.Err()
			}
			return zero, &ErrTimeout{After: timeout, Current: state}
		case <-time.After(interval):
			interval *= 2
			if interval > maxInterval {
				interval = maxInterval
			}
		}
	}
}

// deadlineExceeded reports whether err represents an expired deadline, either
// as a bare context error or as the gRPC status error a deadline-expired call
// returns.
func deadlineExceeded(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return status.Code(err) == codes.DeadlineExceeded
}
