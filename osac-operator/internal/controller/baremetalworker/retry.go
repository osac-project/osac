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
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	eventReasonWorkerRetry  = "WorkerRetry"
	eventReasonWorkerFailed = "WorkerFailed"
)

// FailureCategory classifies a worker failure for backoff schedule selection.
type FailureCategory int

const (
	FailureCategoryTransient    FailureCategory = iota
	FailureCategoryResource                     // no hosts available, resource exhausted
	FailureCategoryAgentTimeout                 // agent did not register within the timeout
)

const (
	minHealthyDuration = 1 * time.Hour
)

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

// handleFailedWorkers processes at most one Failed worker: deletes its BMI via the
// fulfillment API, increments attemptCount, computes failure-appropriate backoff, and
// sets NextRetryTime. The failed BMI's ID is cleared so reconcileNodeSets
// creates a replacement when the retry is due.
func (r *Reconciler) handleFailedWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) error {
	log := ctrllog.FromContext(ctx)
	for i := range co.Status.Workers {
		w := &co.Status.Workers[i]
		if w.Phase != workerPhaseFailed || w.BareMetalInstance.ID == "" {
			continue
		}
		if err := r.checkedDeleteBMI(ctx, co, *w); err != nil {
			return fmt.Errorf("deleting failed worker %s: %w", w.Name, err)
		}
		log.Info("deleted failed BMI", "worker", w.Name, "bmiID", w.BareMetalInstance.ID)
		w.AttemptCount++
		category := ClassifyFailure(w.LastFailureReason)
		backoff := ComputeBackoff(category, w.AttemptCount)
		now := metav1.Now()
		retryTime := metav1.NewTime(now.Add(backoff))
		w.NextRetryTime = &retryTime
		w.BareMetalInstance.ID = ""
		w.ReadySince = nil
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonWorkerFailed, "HandleFailedWorker",
			"worker %s: failed (attempt %d, reason %s), next retry in %s",
			w.Name, w.AttemptCount, w.LastFailureReason, backoff)
		return nil
	}
	return nil
}

// retryFailedWorker creates a replacement BMI for a failed worker whose retry is due.
// Returns a non-zero result if the fulfillment service is unavailable. If the worker is not
// eligible for retry, this is a no-op.
func (r *Reconciler) retryFailedWorker(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	nr *v1alpha1.NodeRequest, prev *v1alpha1.WorkerStatus,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string, observations ...*workerObservation,
) (ctrl.Result, error) {
	if prev.Phase != workerPhaseFailed || prev.BareMetalInstance.ID != "" || !isRetryDue(*prev) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx)
	bmi, res, err := r.ensureBMI(ctx, co, tenant, *nr, prev.BareMetalInstance.Name, image, ignitionRaw, filter, fabricInterface, observations...)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !res.IsZero() {
		return res, nil
	}
	log.Info("created replacement BMI for failed worker", "name", prev.Name, "attempt", prev.AttemptCount, "id", bmi.GetId())
	workerCreated(prev, bmi.GetMetadata().GetName(), bmi.GetId())
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerRetry, "RetryWorker",
		"worker %s: retry attempt %d", prev.Name, prev.AttemptCount)
	return ctrl.Result{}, nil
}

// isRetryDue returns true if a Failed worker's NextRetryTime has passed (or is nil).
func isRetryDue(w v1alpha1.WorkerStatus) bool {
	if w.NextRetryTime == nil {
		return true
	}
	return !time.Now().Before(w.NextRetryTime.Time)
}

// earliestRetryRequeue returns a RequeueAfter result for the earliest pending retry
// among Failed workers, or a zero result if no retries are pending.
func (r *Reconciler) earliestRetryRequeue(workers []v1alpha1.WorkerStatus) ctrl.Result {
	var earliest time.Time
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseFailed || w.NextRetryTime == nil {
			continue
		}
		if earliest.IsZero() || w.NextRetryTime.Time.Before(earliest) {
			earliest = w.NextRetryTime.Time
		}
	}
	if earliest.IsZero() {
		return ctrl.Result{}
	}
	delay := time.Until(earliest)
	if delay < time.Second {
		delay = time.Second
	}
	return ctrl.Result{RequeueAfter: delay}
}

// resetHealthyWorkers resets attemptCount for workers that have been Ready for at least
// MinHealthyDuration, clearing their failure history.
func resetHealthyWorkers(log logr.Logger, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus) {
	now := time.Now()
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseReady || w.AttemptCount == 0 || w.ReadySince == nil {
			continue
		}
		if now.Sub(w.ReadySince.Time) < minHealthyDuration {
			continue
		}
		log.Info("worker healthy for MinHealthyDuration, resetting attemptCount",
			"worker", w.Name, "previousAttempts", w.AttemptCount)
		w.AttemptCount = 0
		w.LastFailureReason = ""
		w.LastFailureMessage = ""
		w.LastFailureTime = nil
		w.NextRetryTime = nil
	}
}
