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
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	eventReasonWorkerRetry                    = "WorkerRetry"
	eventReasonWorkerFailed                   = "WorkerFailed"
	eventReasonRegistryProbePreparationFailed = "RegistryProbePreparationFailed"
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
	case eventReasonAgentRegistrationTimeout, eventReasonRegistryAccessTimeout:
		return FailureCategoryAgentTimeout
	case "NoHostAvailable", "ResourceExhausted":
		return FailureCategoryResource
	default:
		return FailureCategoryTransient
	}
}

// ComputeBackoff returns the backoff duration for the given failure category
// and resettable retry count. Retry 0 is treated as retry 1.
func ComputeBackoff(category FailureCategory, retryCount int32) time.Duration {
	sched := backoffSchedules[category]
	idx := int(retryCount) - 1
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
		if w.RegistryProbePrepared {
			if err := r.cleanupFailedWorkerRegistryProbe(ctx, co, *w); err != nil {
				log.Error(err, "failed worker registry probe cleanup is pending", "worker", w.Name)
				continue
			}
		}
		log.Info("confirmed failed incarnation cleanup", "worker", w.Name, "bmiID", w.BareMetalInstance.ID)
		// AttemptCount is the monotonic identity used by probe resources. RetryCount
		// is deliberately independent: a healthy interval may clear it without
		// changing the resource identity or making the next failure look old.
		w.AttemptCount++
		w.RetryCount++
		category := ClassifyFailure(w.LastFailureReason)
		backoff := ComputeBackoff(category, w.RetryCount)
		now := metav1.Now()
		retryTime := metav1.NewTime(now.Add(backoff))
		w.NextRetryTime = &retryTime
		w.BareMetalInstance.ID = ""
		w.Agent = nil
		w.RegistrationDeadline = nil
		w.RegistryProbePrepared = false
		w.RegistryProbeImage = ""
		w.RegistryAccessReady = nil
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

func workerRegistryProbeMayExist(worker v1alpha1.WorkerStatus, networkingEnabled bool) bool {
	return worker.RegistryProbePrepared || (networkingEnabled && worker.AttemptStartedAt != nil)
}

// cleanupRetiringWorkerRegistryProbes removes guest probe resources even when
// injection failed before RegistryProbePrepared could be persisted. The
// attempt clock is written before probe preparation, so it is durable evidence
// that preparation may have created resources.
func (r *Reconciler) cleanupRetiringWorkerRegistryProbes(
	ctx context.Context, order *v1alpha1.ClusterOrder,
) error {
	if !order.Spec.SupportsAutomaticCaaSEndpoints || !r.NetworkingProvisioningEnabled {
		return nil
	}
	for _, worker := range order.Status.Workers {
		if worker.Phase != workerPhaseUnbinding && worker.Phase != workerPhaseDeleting {
			continue
		}
		if !workerRegistryProbeMayExist(worker, r.NetworkingProvisioningEnabled) {
			continue
		}
		if err := r.cleanupFailedWorkerRegistryProbe(ctx, order, worker); err != nil {
			if apierrors.IsNotFound(err) {
				if recorded := workerByName(order.Status.Workers, worker.Name); recorded != nil {
					recorded.RegistryProbePrepared = false
					recorded.AttemptStartedAt = nil
				}
				continue
			}
			// Retiring probe cleanup is independently retried by the worker
			// recheck timer. Do not let one guest/API deletion failure block
			// creation, Agent binding, or convergence of other workers.
			ctrllog.FromContext(ctx).Error(err, "retiring worker registry probe cleanup is pending",
				"worker", worker.Name)
			continue
		}
		if recorded := workerByName(order.Status.Workers, worker.Name); recorded != nil {
			recorded.RegistryProbePrepared = false
			recorded.RegistryProbeImage = ""
			recorded.AttemptStartedAt = nil
		}
	}
	return nil
}

func (r *Reconciler) cleanupFailedWorkerRegistryProbe(
	ctx context.Context, order *v1alpha1.ClusterOrder, worker v1alpha1.WorkerStatus,
) error {
	names, err := workerRegistryProbeNamesFor(order, worker.Name, worker.AttemptCount)
	if err != nil {
		return err
	}
	resolver := r.registryProbeGuestResolver
	if resolver == nil {
		resolver = hostedClusterRegistryProbeResolver{management: r.Client, authoritative: r.apiReader}
	}
	guest, err := resolver.Resolve(ctx, order)
	if err != nil {
		if errors.Is(err, ErrRegistryProbeGuestGone) {
			// The guest control plane and its API have already disappeared, so
			// guest-side probe resources are gone with them. Do not block BMI,
			// Agent, or ClusterOrder teardown on an API that no longer exists.
			return nil
		}
		return fmt.Errorf("resolve guest API: %w", err)
	}
	var errs []error
	if err := guest.client.CoreV1().ConfigMaps(names.namespace).Delete(ctx, names.configMap, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete worker registry probe ConfigMap: %w", err))
	}
	if err := cleanupWorkerRegistryProbeCredentials(ctx, guest.client, order, names); err != nil {
		errs = append(errs, err)
	}
	if err := cleanupWorkerRegistryProbeForWorker(ctx, guest.client, order, worker.Name); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	ctx context.Context, co *v1alpha1.ClusterOrder, cluster *privatev1.Cluster,
	nr *v1alpha1.NodeRequest, prev *v1alpha1.WorkerStatus,
	image *privatev1.DiskImageReference, ignitionRaw, filter string,
) (ctrl.Result, error) {
	if prev.Phase != workerPhaseFailed || prev.BareMetalInstance.ID != "" || prev.NextRetryTime == nil || !isRetryDue(*prev, time.Now()) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx)
	bmi, res, err := r.ensureBMI(ctx, co, cluster, *nr, prev.BareMetalInstance.Name, image, ignitionRaw, filter)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !res.IsZero() {
		return res, nil
	}
	log.Info("created replacement BMI for failed worker", jsonNameField, prev.Name, "attempt", prev.AttemptCount, "id", bmi.GetId())
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
		case w.Phase == workerPhaseFailed && w.NextRetryTime == nil:
			// A failed ID-less worker may still be waiting for probe/resource
			// cleanup before its retry checkpoint can be armed.
			consider(teardownRequeueInterval)
		case w.Phase == workerPhaseFailed && w.NextRetryTime != nil:
			consider(time.Until(w.NextRetryTime.Time))
		case w.Phase == workerPhaseProvisioning, w.Phase == workerPhaseWaitingForAgent, w.Phase == workerPhaseBinding:
			consider(agentRequeueInterval)
		case w.Phase == workerPhaseUnbinding, w.Phase == workerPhaseDeleting:
			consider(teardownRequeueInterval)
		case w.Phase == workerPhaseReady && w.RetryCount > 0 && w.ReadySince != nil:
			consider(minHealthyDuration - now.Sub(w.ReadySince.Time))
		}
	}
	if earliest == 0 {
		return ctrl.Result{}
	}
	return ctrl.Result{RequeueAfter: earliest}
}

// resetHealthyWorkers clears retry history after workers have been Ready for at
// least MinHealthyDuration. AttemptCount is monotonic probe identity and never resets.
func resetHealthyWorkers(log logr.Logger, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus, now time.Time) {
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseReady || w.RetryCount == 0 || w.ReadySince == nil {
			continue
		}
		if now.Sub(w.ReadySince.Time) < minHealthyDuration {
			continue
		}
		log.Info("worker healthy for MinHealthyDuration, resetting retry history",
			"worker", w.Name, "previousRetries", w.RetryCount, "attempt", w.AttemptCount)
		w.RetryCount = 0
		w.LastFailureReason = ""
		w.LastFailureMessage = ""
		w.LastFailureTime = nil
		w.NextRetryTime = nil
	}
}
