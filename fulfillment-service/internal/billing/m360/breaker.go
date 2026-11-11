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
	"slices"
	"sync"
	"time"
)

// breaker opens for cooldown once threshold calls have failed within window, then lets a single probe through whose
// verdict closes or reopens it. Each call reports whether it was the probe, so a call admitted before the breaker
// opened cannot close it.
// It is provider-agnostic: promote it to a shared package when a second billing provider needs one.
type breaker struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	cooldown  time.Duration
	now       func() time.Time
	failures  []time.Time // inside the window, oldest first, never threshold long
	openUntil time.Time
	probing   bool
}

// newBreaker opens for cooldown after threshold failures within window.
func newBreaker(threshold int, window, cooldown time.Duration) *breaker {
	return &breaker{threshold: threshold, window: window, cooldown: cooldown, now: time.Now}
}

// allow reports whether a call may proceed and whether it is the half-open probe.
// A nil breaker is a pass-through for calls that must not be guarded.
func (b *breaker) allow() (ok, probe bool) {
	if b == nil {
		return true, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true, false
	}
	if b.now().Before(b.openUntil) || b.probing {
		return false, false
	}
	b.probing = true
	return true, true
}

// success records a healthy verdict: it forgets the failures and closes the breaker, unless the breaker is open and
// the call was not its probe.
func (b *breaker) success(probe bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openUntil.IsZero() && !probe {
		return
	}
	b.failures, b.openUntil, b.probing = nil, time.Time{}, false
}

// failure records a failed call. A failed probe reopens the breaker whatever the window holds; a closed breaker opens
// once the failures inside the window reach the threshold; a call admitted before the breaker opened adds nothing.
func (b *breaker) failure(probe bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	switch {
	case probe:
		b.probing, b.openUntil = false, now.Add(b.cooldown)
	case b.openUntil.IsZero():
		cutoff := now.Add(-b.window)
		b.failures = append(slices.DeleteFunc(b.failures, func(t time.Time) bool { return !t.After(cutoff) }), now)
		if len(b.failures) >= b.threshold {
			b.failures, b.openUntil = nil, now.Add(b.cooldown)
		}
	}
}

// abort releases a half-open probe that ended without a verdict, such as a caller cancellation.
func (b *breaker) abort(probe bool) {
	if b == nil || !probe {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}
