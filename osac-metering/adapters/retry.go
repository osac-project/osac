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
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 5 * time.Minute
	jitterFraction = 0.25
)

// retryResult holds the outcome of a retry-wrapped Submit call.
type retryResult struct {
	Attempts      int
	TotalDuration time.Duration
	NonRetryable  bool
	Exhausted     bool
	Err           error
}

// calculateBackoff returns the backoff duration for the given attempt (0-indexed).
// Sequence: 1s, 2s, 4s, 8s, 16s, 32s, 64s, 128s, 256s, 300s (capped).
// Jitter of ±25% is applied.
func calculateBackoff(attempt int) time.Duration {
	backoff := initialBackoff
	for i := 0; i < attempt; i++ {
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
			break
		}
	}
	jitter := float64(backoff) * jitterFraction * (2*rand.Float64() - 1) //nolint:gosec
	return backoff + time.Duration(jitter)
}

// sleepFunc is the function used to sleep between retries.
// Overridable in tests to avoid real sleeps.
var sleepFunc = func(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// submitWithRetry calls adapter.Submit with exponential backoff on retryable errors.
func submitWithRetry(
	ctx context.Context,
	adapter ProviderAdapter,
	event MeteringEvent,
	maxRetries int,
	logger logr.Logger,
) retryResult {
	start := time.Now()

	if maxRetries < 1 {
		maxRetries = 1
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := adapter.Submit(ctx, event)
		if err == nil {
			return retryResult{
				Attempts:      attempt + 1,
				TotalDuration: time.Since(start),
			}
		}

		var nonRetryable *NonRetryableError
		if errors.As(err, &nonRetryable) {
			logger.Error(err, "non-retryable error, skipping event",
				"event_id", event.CloudEvent.ID(),
				"attempt", attempt+1,
			)
			return retryResult{
				Attempts:      attempt + 1,
				TotalDuration: time.Since(start),
				NonRetryable:  true,
				Err:           err,
			}
		}

		if attempt < maxRetries-1 {
			backoff := calculateBackoff(attempt)
			logger.V(1).Info("retrying submit",
				"event_id", event.CloudEvent.ID(),
				"attempt", attempt+1,
				"backoff", backoff,
			)
			if err := sleepFunc(ctx, backoff); err != nil {
				return retryResult{
					Attempts:      attempt + 1,
					TotalDuration: time.Since(start),
					Err:           ctx.Err(),
				}
			}
		} else {
			logger.Error(err, "retries exhausted, skipping event",
				"event_id", event.CloudEvent.ID(),
				"attempts", maxRetries,
			)
			return retryResult{
				Attempts:      maxRetries,
				TotalDuration: time.Since(start),
				Exhausted:     true,
				Err:           err,
			}
		}
	}

	// Unreachable with maxRetries >= 1, but keep the contract: Exhausted implies a non-nil Err.
	return retryResult{
		Attempts:      maxRetries,
		TotalDuration: time.Since(start),
		Exhausted:     true,
		Err:           fmt.Errorf("submit not attempted: maxRetries %d", maxRetries),
	}
}
