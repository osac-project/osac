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

// handleFailedWorkers cleans at most one recorded Failed incarnation. Retain
// the old ID until Agent removal and BMI Get NotFound; initialize retry exactly
// once at that transition. An ID-less retry checkpoint is not another failure.
func (r *Reconciler) handleFailedWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) error {
	log := ctrllog.FromContext(ctx)
	for i := range co.Status.Workers {
		w := &co.Status.Workers[i]
		if w.Phase != workerPhaseFailed || (w.BareMetalInstance.ID == "" && w.NextRetryTime != nil) {
			continue
		}
		gone, err := r.cleanupWorker(ctx, co, w)
		if err != nil {
			return fmt.Errorf("cleaning failed worker %s: %w", w.Name, err)
		}
		if !gone {
			return nil
		}
		log.Info("confirmed failed incarnation cleanup", "worker", w.Name, "bmiID", w.BareMetalInstance.ID)
		w.AttemptCount++
		category := ClassifyFailure(w.LastFailureReason)
		backoff := ComputeBackoff(category, w.AttemptCount)
		now := metav1.Now()
		retryTime := metav1.NewTime(now.Add(backoff))
		w.NextRetryTime = &retryTime
		w.BareMetalInstance.ID = ""
		// Only confirmed old-incarnation absence makes a new reservation safe to
		// cancel. Unknown Create outcomes never pass this cleanup checkpoint.
		w.BMICreateState = v1alpha1.WorkerBMICreateStateReserved
		w.ReadySince = nil
		// The attempt origin belongs to the attempt that just ended. A fresh one
		// is persisted before the replacement Create, so retry backoff is never
		// counted against the next registration timeout.
		w.AttemptStartedAt = nil
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonWorkerFailed, "HandleFailedWorker",
			"worker %s: failed (attempt %d, reason %s), next retry in %s",
			w.Name, w.AttemptCount, w.LastFailureReason, backoff)
		return nil
	}
	return nil
}

func hasFailedIncarnations(workers []v1alpha1.WorkerStatus) bool {
	for _, w := range workers {
		if w.Phase == workerPhaseFailed && w.BareMetalInstance.ID != "" {
			return true
		}
	}
	return false
}

// retryFailedWorker creates a replacement BMI for a failed worker whose retry is due.
// Returns a non-zero result if the fulfillment service is unavailable. If the worker is not
// eligible for retry, this is a no-op.
func (r *Reconciler) retryFailedWorker(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	nr *v1alpha1.NodeRequest, prev *v1alpha1.WorkerStatus,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string,
) (ctrl.Result, error) {
	if prev.Phase != workerPhaseFailed || prev.BareMetalInstance.ID != "" || prev.NextRetryTime == nil || !isRetryDue(*prev, time.Now()) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx)
	bmi, res, err := r.ensureBMI(ctx, co, tenant, *nr, prev.BareMetalInstance.Name, image, ignitionRaw, filter, fabricInterface)
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
func isRetryDue(w v1alpha1.WorkerStatus, now time.Time) bool {
	if w.NextRetryTime == nil {
		return true
	}
	return !now.Before(w.NextRetryTime.Time)
}

// workerRecheckDeadline contributes the earliest bounded recheck the worker set
// needs when no stage already produced a boundary. Agent and NodePool watches are
// optional, so provisioning, waiting, binding and cleanup states are rechecked
// explicitly. A retry deadline is one such wait among others rather than a global
// gate, and a stable no-op order returns a zero result and relies on watches. A
// Ready worker with retry history keeps a timer for the healthy-reset deadline.
func (r *Reconciler) workerRecheckDeadline(workers []v1alpha1.WorkerStatus, now time.Time) ctrl.Result {
	earliest := time.Duration(0)
	consider := func(delay time.Duration) {
		if delay < time.Second {
			delay = time.Second
		}
		if earliest == 0 || delay < earliest {
			earliest = delay
		}
	}
	for i := range workers {
		w := &workers[i]
		switch {
		case w.Phase == workerPhaseFailed && w.BareMetalInstance.ID != "":
			// Provider cleanup is still pending; recheck its completion.
			consider(teardownRequeueInterval)
		case w.Phase == workerPhaseFailed && w.NextRetryTime != nil:
			consider(time.Until(w.NextRetryTime.Time))
		case w.Phase == workerPhaseProvisioning, w.Phase == workerPhaseWaitingForAgent, w.Phase == workerPhaseBinding:
			consider(agentRequeueInterval)
		case w.Phase == workerPhaseUnbinding, w.Phase == workerPhaseDeleting:
			consider(teardownRequeueInterval)
		case w.Phase == workerPhaseReady && w.AttemptCount > 0 && w.ReadySince != nil:
			consider(minHealthyDuration - now.Sub(w.ReadySince.Time))
		}
	}
	if earliest == 0 {
		return ctrl.Result{}
	}
	return ctrl.Result{RequeueAfter: earliest}
}

// resetHealthyWorkers resets attemptCount for workers that have been Ready for at least
// MinHealthyDuration, clearing their failure history.
func resetHealthyWorkers(log logr.Logger, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, now time.Time) {
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
