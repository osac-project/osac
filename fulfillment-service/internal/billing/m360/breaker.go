/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package m360

import (
	"sync"
	"time"
)

// breaker opens after threshold consecutive failures and lets a single probe through once the cooldown elapses.
// It is provider-agnostic: promote it to a shared package when a second billing provider needs one.
type breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	failures  int
	openUntil time.Time
	probing   bool
}

// newBreaker opens after threshold consecutive failures for cooldown.
func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: threshold, cooldown: cooldown, now: time.Now}
}

// A nil breaker is a pass-through for calls that must not be guarded.
func (b *breaker) allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if b.now().Before(b.openUntil) || b.probing {
		return false
	}
	b.probing = true
	return true
}

// success records a healthy verdict and resets the failure count.
func (b *breaker) success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures, b.openUntil, b.probing = 0, time.Time{}, false
}

// failure records a failed call and opens the breaker once failures reach the
// threshold.
func (b *breaker) failure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
}

// abort releases a half-open probe that ended without a verdict, such as a caller cancellation.
func (b *breaker) abort() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}
