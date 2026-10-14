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
	"testing"
)

func TestRetryProviderReadRetriesUntilSuccess(t *testing.T) {
	calls := 0
	got, err := RetryProviderRead(context.Background(), func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.Join(ErrProviderReadRetryable, errors.New("temporary provider error"))
		}
		return "value", nil
	})
	if err != nil {
		t.Fatalf("RetryProviderRead() error = %v", err)
	}
	if got != "value" || calls != 3 {
		t.Fatalf("RetryProviderRead() = (%q, %d calls), want (value, 3 calls)", got, calls)
	}
}

func TestRetryProviderReadReturnsLastErrorAfterConfiguredRetries(t *testing.T) {
	wantErr := errors.Join(ErrProviderReadRetryable, errors.New("provider unavailable"))
	calls := 0
	_, err := RetryProviderRead(context.Background(), func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RetryProviderRead() error = %v, want %v", err, wantErr)
	}
	if want := ProviderReadRetryCount + 1; calls != want {
		t.Fatalf("RetryProviderRead() made %d calls, want %d", calls, want)
	}
}

func TestRetryProviderReadDoesNotRetryPermanentErrors(t *testing.T) {
	wantErr := errors.New("invalid provider response")
	calls := 0
	_, err := RetryProviderRead(context.Background(), func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RetryProviderRead() error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("RetryProviderRead() made %d calls for a permanent error, want 1", calls)
	}
}

func TestRetryProviderReadStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := RetryProviderRead(ctx, func(context.Context) (struct{}, error) {
		calls++
		cancel()
		return struct{}{}, errors.Join(ErrProviderReadRetryable, errors.New("request failed"))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RetryProviderRead() error = %v, want context canceled", err)
	}
	if calls != 1 {
		t.Fatalf("RetryProviderRead() made %d calls after cancellation, want 1", calls)
	}
}
