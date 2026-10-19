/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package billing

import (
	"context"
	"errors"
	"time"
)

// ProviderReadRetryCount is the number of retries after the initial provider
// read attempt.
const ProviderReadRetryCount = 3

// RetryProviderRead retries only failures marked ErrProviderReadRetryable, up
// to ProviderReadRetryCount times with a short exponential backoff. The
// caller's context bounds both provider calls and backoff waits.
func RetryProviderRead[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= ProviderReadRetryCount; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result, err := read(ctx)
		if err == nil {
			return result, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, ctxErr
		}
		lastErr = err
		if !errors.Is(err, ErrProviderReadRetryable) {
			return zero, err
		}
		if attempt == ProviderReadRetryCount {
			break
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}
