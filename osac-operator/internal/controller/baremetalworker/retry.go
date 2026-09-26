/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"fmt"
	"strings"
	"time"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// FailureCategory classifies a worker failure for backoff schedule selection.
type FailureCategory int

const (
	FailureCategoryTransient    FailureCategory = iota
	FailureCategoryResource                     // no hosts available, resource exhausted
	FailureCategoryAgentTimeout                 // agent did not register within the timeout
)

const minHealthyDuration = 1 * time.Hour

type backoffSchedule struct {
	steps []time.Duration
	cap   time.Duration
}

var backoffSchedules = map[FailureCategory]backoffSchedule{
	FailureCategoryTransient: {
		steps: []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second},
		cap:   5 * time.Minute,
	},
	FailureCategoryResource: {
		steps: []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute},
		cap:   30 * time.Minute,
	},
	FailureCategoryAgentTimeout: {
		steps: []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute},
		cap:   30 * time.Minute,
	},
}

// ClassifyFailure maps a LastFailureReason string to a FailureCategory.
func ClassifyFailure(reason string) FailureCategory {
	switch reason {
	case eventReasonAgentRegistrationTimeout:
		return FailureCategoryAgentTimeout
	case "NoHostAvailable", "ResourceExhausted":
		return FailureCategoryResource
	default:
		return FailureCategoryTransient
	}
}

// ComputeBackoff returns the backoff duration for the given failure category and attempt count.
// Attempt 0 is treated as attempt 1 (first retry).
func ComputeBackoff(category FailureCategory, attemptCount int32) time.Duration {
	sched := backoffSchedules[category]
	idx := int(attemptCount) - 1
	if idx < 0 {
		idx = 0
	}
	if idx < len(sched.steps) {
		return sched.steps[idx]
	}
	return sched.cap
}

// FormatWorkersFailed builds tenant-safe retry details from failed workers.
func FormatWorkersFailed(workers []v1alpha1.WorkerStatus) string {
	var parts []string
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseFailed {
			continue
		}
		retry := "pending"
		if w.NextRetryTime != nil {
			retry = w.NextRetryTime.UTC().Format(time.RFC3339)
		}
		parts = append(parts, fmt.Sprintf("retry %d: attempt %d, next retry %s", len(parts)+1, w.AttemptCount, retry))
	}
	return strings.Join(parts, "; ")
}
