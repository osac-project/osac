/*
Copyright 2025.

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

package provisioning

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

// Action represents the outcome of shouldTriggerProvision.
type Action int

const (
	Skip    Action = iota // nothing to do
	Trigger               // trigger a new provision job
	Poll                  // poll an existing non-terminal job
	Requeue               // stale cache detected, requeue to refresh
	Backoff               // failed job with same config, retry after backoff
)

func (a Action) String() string {
	switch a {
	case Skip:
		return "Skip"
	case Trigger:
		return "Trigger"
	case Poll:
		return "Poll"
	case Requeue:
		return "Requeue"
	case Backoff:
		return "Backoff"
	default:
		return "Unknown"
	}
}

const (
	BackoffBaseDelay = 2 * time.Minute
	BackoffMaxDelay  = 30 * time.Minute
)

// HandleBackoff checks if the backoff period has elapsed since the last failed job.
// If elapsed, it calls triggerFn to retry. Otherwise, it returns a RequeueAfter with the remaining delay.
func HandleBackoff(ctx context.Context, provState *State, latestJob *v1alpha1.JobStatus, triggerFn func() (ctrl.Result, error)) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	backoff := ComputeBackoffFromJobs(*provState.Jobs, provState.DesiredConfigVersion)
	elapsed := time.Since(latestJob.Timestamp.Time)
	if elapsed >= backoff {
		log.Info("backoff elapsed, retrying provision", "jobID", latestJob.JobID, "backoff", backoff, "elapsed", elapsed)
		return triggerFn()
	}
	remaining := backoff - elapsed
	log.Info("provision failed, backing off", "jobID", latestJob.JobID, "backoff", backoff, "remaining", remaining)
	return ctrl.Result{RequeueAfter: remaining}, nil
}

// HasJobID returns true if the job is non-nil and has a non-empty JobID.
func HasJobID(job *v1alpha1.JobStatus) bool {
	return job != nil && job.JobID != ""
}

// computeBackoffFromFailedJobs determines the next backoff duration based on the gap
// between the last two failed jobs matching the filter.
// First failure uses BackoffBaseDelay. Subsequent failures double the previous gap.
func computeBackoffFromFailedJobs(jobs []v1alpha1.JobStatus, match func(*v1alpha1.JobStatus) bool) time.Duration {
	var last, prev *v1alpha1.JobStatus
	for i := len(jobs) - 1; i >= 0; i-- {
		j := &jobs[i]
		if j.State != v1alpha1.JobStateFailed || !match(j) {
			continue
		}
		if last == nil {
			last = j
		} else {
			prev = j
			break
		}
	}

	if last == nil || prev == nil {
		return BackoffBaseDelay
	}

	gap := last.Timestamp.Time.UTC().Sub(prev.Timestamp.Time.UTC())
	if gap <= 0 {
		return BackoffBaseDelay
	}
	nextDelay := gap * 2
	if nextDelay < BackoffBaseDelay {
		return BackoffBaseDelay
	}
	if nextDelay > BackoffMaxDelay {
		return BackoffMaxDelay
	}
	return nextDelay
}

// ComputeBackoffFromJobs determines the next backoff duration based on the gap
// between the last two failed provision jobs with the same ConfigVersion.
func ComputeBackoffFromJobs(jobs []v1alpha1.JobStatus, configVersion string) time.Duration {
	return computeBackoffFromFailedJobs(jobs, func(j *v1alpha1.JobStatus) bool {
		return j.Type == v1alpha1.JobTypeProvision && j.ConfigVersion == configVersion
	})
}

// ComputeDeprovisionBackoff determines the next backoff duration for deprovision retries.
func ComputeDeprovisionBackoff(jobs []v1alpha1.JobStatus) time.Duration {
	return computeBackoffFromFailedJobs(jobs, func(j *v1alpha1.JobStatus) bool {
		return j.Type == v1alpha1.JobTypeDeprovision
	})
}
