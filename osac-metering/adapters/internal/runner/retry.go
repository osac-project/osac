/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/adapters"
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

// retryKey identifies one Kafka record by its position. A redelivered record
// keeps its key, so its retry progress carries across consume sessions.
type retryKey struct {
	Topic     string
	Partition int32
	Offset    int64
}

// retryEntry is the retry progress of one record.
type retryEntry struct {
	// Attempts is the number of Submit attempts already spent on the record
	// across all sessions.
	Attempts int
	// NextEligible is when the next attempt would have been due had the
	// session survived its backoff.
	NextEligible time.Time
}

// retryCache keeps retry progress across consume sessions. Sessions restart
// every SessionInterval to discover new topics, and ending a session cancels
// the in-flight claim, so the attempt count cannot live in claim-local state:
// a persistently retryable record would restart at zero on every session and
// never reach the dead-letter queue. The cache is in-memory only: if the pod
// restarts, counting starts over, which only delays the dead-letter decision
// and never loses or duplicates a charge.
type retryCache struct {
	mu      sync.Mutex
	entries map[retryKey]retryEntry
	ttl     time.Duration
}

func newRetryCache(ttl time.Duration) *retryCache {
	return &retryCache{
		entries: make(map[retryKey]retryEntry),
		ttl:     ttl,
	}
}

// resume returns the recorded progress for the record, or the zero entry when
// none is kept.
func (c *retryCache) resume(key retryKey) retryEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return retryEntry{}
	}
	if time.Since(entry.NextEligible) > c.ttl {
		// The record stopped being redelivered a full TTL after its schedule
		// ended; its progress is stale.
		delete(c.entries, key)
		return retryEntry{}
	}
	return entry
}

// record saves the progress of a record whose backoff was interrupted.
func (c *retryCache) record(key retryKey, entry retryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry
}

// remove drops a record that is finished: submitted, or dead-lettered.
func (c *retryCache) remove(key retryKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// evictExpired drops entries whose record has not come back for a full TTL
// past its next-eligible time.
func (c *retryCache) evictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.Sub(entry.NextEligible) > c.ttl {
			delete(c.entries, key)
		}
	}
}

// startEviction runs evictExpired every 30 seconds until ctx is cancelled.
func (c *retryCache) startEviction(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

// len returns the number of entries in the cache.
func (c *retryCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// submitWithRetry calls adapter.Submit with exponential backoff on retryable
// errors. Progress is kept in retries, keyed by the record's position, so a
// record whose backoff spans several consume sessions still exhausts its
// budget and reaches the dead-letter queue instead of blocking its partition.
func submitWithRetry(
	ctx context.Context,
	adapter adapters.ProviderAdapter,
	event adapters.MeteringEvent,
	maxRetries int,
	logger logr.Logger,
	retries *retryCache,
) retryResult {
	start := time.Now()

	if maxRetries < 1 {
		maxRetries = 1
	}

	key := retryKey{Topic: event.Topic, Partition: event.Partition, Offset: event.Offset}
	prior := retries.resume(key)

	for attempt := prior.Attempts; attempt < maxRetries; attempt++ {
		err := adapter.Submit(ctx, event)
		if err == nil {
			retries.remove(key)
			return retryResult{
				Attempts:      attempt + 1,
				TotalDuration: time.Since(start),
			}
		}

		var nonRetryable *adapters.NonRetryableError
		if errors.As(err, &nonRetryable) {
			retries.remove(key)
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
			// The session may end during this backoff and cancel the claim.
			// Record the progress before sleeping, so the next session
			// continues the schedule from this attempt count instead of
			// restarting the budget at zero.
			retries.record(key, retryEntry{
				Attempts:     attempt + 1,
				NextEligible: time.Now().Add(backoff),
			})
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
			retries.remove(key)
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
