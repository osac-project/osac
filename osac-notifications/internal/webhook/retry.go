/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import "time"

// Retry delays: 5s, 30s, 2m, 10m, 30m, 1h, then hourly.
var retryDelays = []time.Duration{
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

const sustainedFailureWindow = 5 * 24 * time.Hour

// NextAttemptDelay returns the delay before the next attempt after attemptCount
// completed attempts (1-based after a failure).
func NextAttemptDelay(attemptCount int) time.Duration {
	if attemptCount <= 0 {
		return retryDelays[0]
	}
	idx := attemptCount - 1
	if idx >= len(retryDelays) {
		return time.Hour
	}
	return retryDelays[idx]
}

// SustainedFailure reports whether firstFailure is old enough to auto-pause.
func SustainedFailure(firstFailure time.Time, now time.Time) bool {
	if firstFailure.IsZero() {
		return false
	}
	return now.Sub(firstFailure) >= sustainedFailureWindow
}
