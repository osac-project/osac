/*
Copyright 2026.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	// DefaultAgentRegistrationTimeout is the maximum time to wait for an agent
	// to register after a BareMetalInstance is created.
	DefaultAgentRegistrationTimeout = 30 * time.Minute

	// DefaultMaxWorkerRetries is the maximum number of provisioning attempts
	// per worker slot before the controller sets a terminal WorkersFailed condition.
	DefaultMaxWorkerRetries = 5

	// bootingWorkerRequeueInterval is the requeue interval used when a worker's
	// agent has registered but the BMI is not yet ready. This ensures the
	// controller polls the worker's progress rather than waiting indefinitely.
	bootingWorkerRequeueInterval = 1 * time.Minute
)

// BMIProvider abstracts BareMetalInstance lifecycle operations for testability.
type BMIProvider interface {
	// CreateBMI creates a new BareMetalInstance CR and returns its name and namespace.
	CreateBMI(ctx context.Context, clusterOrder *v1alpha1.ClusterOrder, workerIndex int) (name, namespace string, err error)

	// DeleteBMI deletes a BareMetalInstance CR by name and namespace.
	DeleteBMI(ctx context.Context, name, namespace string) error

	// GetBMIRegistrationTime returns the time when the agent registered for the given BMI,
	// or zero time if the agent has not yet registered.
	GetBMIRegistrationTime(ctx context.Context, name, namespace string) (time.Time, error)

	// IsBMIReady returns true if the BareMetalInstance has reached a ready state.
	IsBMIReady(ctx context.Context, name, namespace string) (bool, error)
}

// FulfillmentClient abstracts gRPC communication with the fulfillment service for testability.
type FulfillmentClient interface {
	// ReportWorkerStatus reports worker provisioning status to the fulfillment service.
	ReportWorkerStatus(ctx context.Context, clusterOrderName string, workers []v1alpha1.WorkerStatus) error
}

// BareMetalWorkerReconciler reconciles bare-metal worker provisioning failures
// for ClusterOrder resources, implementing timeout detection, escalating backoff,
// BMI replacement, and terminal failure handling.
type BareMetalWorkerReconciler struct {
	// BMIProvider handles BareMetalInstance lifecycle operations.
	BMIProvider BMIProvider

	// FulfillmentClient communicates worker status to the fulfillment gRPC service.
	FulfillmentClient FulfillmentClient

	// AgentRegistrationTimeout is the maximum time to wait for an agent to register.
	AgentRegistrationTimeout time.Duration

	// MaxRetries is the maximum number of provisioning attempts per worker slot.
	MaxRetries int

	// now returns the current time (injectable for testing).
	now func() time.Time
}

// NewBareMetalWorkerReconciler creates a reconciler with production defaults.
// Both bmiProvider and fulfillmentClient may be nil: when nil, ReconcileWorkers
// returns early without processing workers. This allows the reconciler to be
// wired at startup before concrete provider implementations are available,
// deferring activation until the bare-metal provisioning feature is fully
// integrated.
func NewBareMetalWorkerReconciler(
	bmiProvider BMIProvider,
	fulfillmentClient FulfillmentClient,
) *BareMetalWorkerReconciler {
	return &BareMetalWorkerReconciler{
		BMIProvider:              bmiProvider,
		FulfillmentClient:        fulfillmentClient,
		AgentRegistrationTimeout: DefaultAgentRegistrationTimeout,
		MaxRetries:               DefaultMaxWorkerRetries,
		now:                      time.Now,
	}
}

// IsTransientGRPCError returns true if the error is a transient gRPC error
// that should not count toward the maximum retry budget. Transient errors
// are: Unavailable, DeadlineExceeded, ResourceExhausted, and Aborted.
func IsTransientGRPCError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
		return true
	default:
		return false
	}
}

// IsNotFoundError returns true if the error indicates the resource was not found,
// covering both gRPC NotFound and k8s API NotFound responses.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check k8s API NotFound first (most common for k8s client operations).
	if apierrors.IsNotFound(err) {
		return true
	}
	// Check gRPC NotFound.
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return true
	}
	return false
}

// IsTransientError returns true if the error is transient (should not count
// toward the maximum retry budget) across both gRPC status codes and common
// Kubernetes client errors.
//
// Covered errors (transient):
//   - gRPC: Unavailable, DeadlineExceeded, ResourceExhausted, Aborted
//   - Kubernetes API server errors:
//   - Timeout (apierrors.IsTimeout)
//   - ServerTimeout (apierrors.IsServerTimeout)
//   - ServiceUnavailable (apierrors.IsServiceUnavailable)
//   - TooManyRequests / 429 (apierrors.IsTooManyRequests)
//   - InternalError / 500 (apierrors.IsInternalError)
//   - Network-level errors (net.Error), including:
//   - Connection timeouts
//   - Connection refused
//   - DNS resolution failures
//
// NOT transient (permanent — these count toward the retry budget):
//   - NotFound (resource does not exist; handled separately by IsNotFoundError)
//   - Conflict (optimistic concurrency violation; caller should re-read and retry)
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Check gRPC transient codes.
	if IsTransientGRPCError(err) {
		return true
	}
	// Check k8s API server errors (5xx) and throttling (429).
	if apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsTimeout(err) ||
		apierrors.IsInternalError(err) {
		return true
	}
	// Check network-level errors (connection refused, DNS failures, timeouts).
	var netErr net.Error
	return errors.As(err, &netErr)
}

// ComputeWorkerBackoff calculates the next backoff delay for a worker using
// the same escalating schedule as the existing provisioning helpers:
// base delay of 2 minutes, doubling on each attempt, capped at 30 minutes.
func ComputeWorkerBackoff(attemptCount int) time.Duration {
	if attemptCount <= 1 {
		return provisioning.BackoffBaseDelay
	}

	delay := provisioning.BackoffBaseDelay
	for i := 1; i < attemptCount; i++ {
		delay *= 2
		if delay > provisioning.BackoffMaxDelay {
			return provisioning.BackoffMaxDelay
		}
	}
	return delay
}

// workerReconcileResult captures the outcome of reconciling a single worker.
type workerReconcileResult struct {
	// requeueAfter is the minimum requeue delay requested for this worker.
	requeueAfter time.Duration
	// exhausted is true when the worker has reached the maximum retry count.
	exhausted bool
	// hadTransientError is true when a transient gRPC error was encountered.
	hadTransientError bool
}

// minDuration returns the smaller of two durations, treating zero as "not set".
func minDuration(current, candidate time.Duration) time.Duration {
	if candidate == 0 {
		return current
	}
	if current == 0 || candidate < current {
		return candidate
	}
	return current
}

// ReconcileWorkers is the main entry point for worker failure handling.
// It inspects each worker in the ClusterOrder status and takes appropriate action:
// - Detects agent registration timeouts
// - Triggers BMI replacement with escalating backoff
// - Sets FulfillmentServiceUnavailable on transient gRPC errors
// - Sets terminal WorkersFailed condition after max retries
func (r *BareMetalWorkerReconciler) ReconcileWorkers(
	ctx context.Context, instance *v1alpha1.ClusterOrder,
) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if len(instance.Status.Workers) == 0 {
		return ctrl.Result{}, nil
	}

	// Guard against nil providers. The reconciler may be wired with nil
	// BMIProvider and FulfillmentClient at startup when concrete
	// implementations are not yet available. Return early to avoid a
	// nil-pointer panic; the feature activates once real providers are
	// injected.
	if r.BMIProvider == nil {
		log.Info("BMIProvider is nil, skipping worker reconciliation (feature not yet active)")
		return ctrl.Result{}, nil
	}

	now := r.now()
	var nextRequeue time.Duration
	allTerminal := true
	hasFailures := false
	// hadTransientError tracks whether ANY transient gRPC error occurred
	// during the entire reconciliation loop. The FulfillmentServiceUnavailable
	// condition is only cleared after the loop completes with zero transient
	// errors, preventing a single successful worker from prematurely
	// clearing a condition that still applies to another worker.
	hadTransientError := false

	for i := range instance.Status.Workers {
		worker := &instance.Status.Workers[i]
		result, err := r.reconcileWorker(ctx, instance, worker, now)
		if err != nil {
			return ctrl.Result{}, err
		}
		if result.exhausted {
			hasFailures = true
		} else {
			allTerminal = false
		}
		if result.hadTransientError {
			hadTransientError = true
		}
		nextRequeue = minDuration(nextRequeue, result.requeueAfter)
	}

	// Report worker status to fulfillment service
	if r.FulfillmentClient != nil {
		if err := r.FulfillmentClient.ReportWorkerStatus(ctx, instance.Name, instance.Status.Workers); err != nil {
			if IsTransientError(err) {
				log.Info("transient gRPC error reporting worker status",
					"clusterOrder", instance.Name)
				hadTransientError = true
				r.setTransientGRPCCondition(instance, err)
				// Ensure the controller requeues to retry the report even when
				// all workers are already ready (nextRequeue would be zero).
				nextRequeue = minDuration(nextRequeue, provisioning.BackoffBaseDelay)
			} else {
				return ctrl.Result{}, fmt.Errorf("reporting worker status: %w", err)
			}
		}
	}

	// Set FulfillmentServiceUnavailable to False (rather than removing it)
	// when the entire reconciliation loop completes with zero transient
	// errors. Setting to False ensures patchStatusWithRetry persists the
	// cleared state via SetStatusCondition; removing would only modify
	// the in-memory object without propagating to the API server.
	if !hadTransientError {
		instance.SetStatusCondition(
			v1alpha1.ConditionFulfillmentServiceUnavailable,
			metav1.ConditionFalse,
			"No transient errors during reconciliation",
			v1alpha1.ReasonNoTransientErrors,
		)
	}

	// Set terminal condition if all workers have exhausted retries
	if hasFailures && allTerminal {
		log.Info("all workers have exhausted retries, setting terminal WorkersFailed condition",
			"clusterOrder", instance.Name)
		instance.SetStatusCondition(
			v1alpha1.ConditionWorkersFailed,
			metav1.ConditionTrue,
			"All worker provisioning attempts exhausted",
			v1alpha1.ReasonMaxRetriesExhausted,
		)
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseFailed
	}

	if nextRequeue > 0 {
		return ctrl.Result{RequeueAfter: nextRequeue}, nil
	}
	return ctrl.Result{}, nil
}

// reconcileWorker processes a single worker entry and returns the reconciliation
// outcome. Extracting per-worker logic keeps ReconcileWorkers below the
// cyclomatic-complexity threshold.
func (r *BareMetalWorkerReconciler) reconcileWorker(
	ctx context.Context, instance *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus, now time.Time,
) (workerReconcileResult, error) {
	log := ctrllog.FromContext(ctx)

	// Check if max retries exhausted
	if worker.AttemptCount >= r.MaxRetries {
		return workerReconcileResult{exhausted: true}, nil
	}

	// If BMIName is empty but we have prior attempts, the old BMI was
	// successfully deleted but the subsequent CreateBMI failed transiently.
	// Skip readiness/registration checks (there is no BMI to check) and go
	// directly to creation.
	if worker.BMIName == "" && worker.AttemptCount > 0 {
		log.Info("worker has empty BMIName after prior attempt, retrying BMI creation",
			"workerID", worker.WorkerID, "attemptCount", worker.AttemptCount)
		return r.createReplacementBMI(ctx, instance, worker)
	}

	// Check if worker is ready
	ready, err := r.BMIProvider.IsBMIReady(ctx, worker.BMIName, worker.BMINamespace)
	if err != nil {
		if IsTransientError(err) {
			log.Info("transient gRPC error checking BMI readiness, setting FulfillmentServiceUnavailable",
				"worker", worker.BMIName)
			r.setTransientGRPCCondition(instance, err)
			return workerReconcileResult{
				requeueAfter:      provisioning.BackoffBaseDelay,
				hadTransientError: true,
			}, nil
		}
		return workerReconcileResult{}, fmt.Errorf("checking BMI readiness for %s/%s: %w",
			worker.BMINamespace, worker.BMIName, err)
	}

	if ready {
		return workerReconcileResult{}, nil
	}

	// Check backoff window
	if worker.NextRetryTime != nil && now.Before(worker.NextRetryTime.Time) {
		remaining := worker.NextRetryTime.Time.Sub(now)
		return workerReconcileResult{requeueAfter: remaining}, nil
	}

	// Check agent registration timeout
	regTime, err := r.BMIProvider.GetBMIRegistrationTime(ctx, worker.BMIName, worker.BMINamespace)
	if err != nil {
		if IsTransientError(err) {
			log.Info("transient gRPC error checking agent registration, setting FulfillmentServiceUnavailable",
				"worker", worker.BMIName)
			r.setTransientGRPCCondition(instance, err)
			return workerReconcileResult{
				requeueAfter:      provisioning.BackoffBaseDelay,
				hadTransientError: true,
			}, nil
		}
		return workerReconcileResult{}, fmt.Errorf("checking agent registration for %s/%s: %w",
			worker.BMINamespace, worker.BMIName, err)
	}

	if regTime.IsZero() {
		return r.handleUnregisteredAgent(ctx, instance, worker, now)
	}

	// Agent is registered but BMI is not yet ready — poll periodically.
	// TODO(OSAC-5126): Add a readiness timeout here once BMIProvider is wired
	// with concrete implementations. If the BMI stays not-ready beyond a
	// threshold (e.g. 60 minutes after agent registration), trigger a
	// replacement via replaceBMI to avoid workers being stuck indefinitely.
	return workerReconcileResult{requeueAfter: bootingWorkerRequeueInterval}, nil
}

// handleUnregisteredAgent handles the case where a worker's agent has not yet
// registered, checking for timeout and triggering BMI replacement if needed.
func (r *BareMetalWorkerReconciler) handleUnregisteredAgent(
	ctx context.Context, instance *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus, now time.Time,
) (workerReconcileResult, error) {
	log := ctrllog.FromContext(ctx)

	bmiCreationTime, hasCreationTime := r.getBMICreationTime(worker)
	if hasCreationTime && now.Sub(bmiCreationTime) >= r.AgentRegistrationTimeout {
		log.Info("agent registration timeout, triggering BMI replacement",
			"worker", worker.BMIName, "timeout", r.AgentRegistrationTimeout)

		result, transientErr, err := r.replaceBMI(ctx, instance, worker, v1alpha1.ReasonAgentRegistrationTimeout,
			fmt.Sprintf("Agent failed to register within %s", r.AgentRegistrationTimeout))
		if err != nil {
			return workerReconcileResult{}, err
		}
		return workerReconcileResult{
			requeueAfter:      result.RequeueAfter,
			hadTransientError: transientErr,
		}, nil
	}

	// Still waiting for agent registration
	if hasCreationTime {
		remaining := r.AgentRegistrationTimeout - now.Sub(bmiCreationTime)
		if remaining > 0 {
			return workerReconcileResult{requeueAfter: remaining}, nil
		}
	}

	// No creation timestamp yet; record the current time as the attempt
	// start so that subsequent reconciliations can evaluate the agent
	// registration timeout instead of polling indefinitely.
	attemptStart := metav1.NewTime(now)
	worker.AttemptStartTime = &attemptStart
	log.Info("recorded attempt start time for worker without timing metadata",
		"worker", worker.BMIName)
	return workerReconcileResult{requeueAfter: bootingWorkerRequeueInterval}, nil
}

// setTransientGRPCCondition sets the FulfillmentServiceUnavailable condition
// with a sanitized error message.
func (r *BareMetalWorkerReconciler) setTransientGRPCCondition(instance *v1alpha1.ClusterOrder, err error) {
	instance.SetStatusCondition(
		v1alpha1.ConditionFulfillmentServiceUnavailable,
		metav1.ConditionTrue,
		sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
		v1alpha1.ReasonGRPCUnavailable,
	)
}

// replaceBMI deletes the current BMI and creates a replacement, updating the
// worker status with backoff timing and failure information. The returned
// hadTransientErr flag indicates whether a transient gRPC error occurred
// during the replacement, so the caller can avoid clearing the
// FulfillmentServiceUnavailable condition prematurely.
func (r *BareMetalWorkerReconciler) replaceBMI(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	worker *v1alpha1.WorkerStatus,
	reason, message string,
) (result ctrl.Result, hadTransientErr bool, err error) {
	log := ctrllog.FromContext(ctx)

	// Capture the old BMI name before any mutation so log entries and
	// condition messages correctly reference the replaced instance.
	oldBMIName := worker.BMIName

	// Compute new attempt count without mutating the worker yet, so that
	// a transient CreateBMI failure does not persist stale retry state.
	newAttemptCount := worker.AttemptCount + 1

	// Check if max retries would be exhausted after increment. On the
	// final attempt, do NOT delete the old BMI — leave it in place for
	// debugging. Set the terminal condition and return without replacement.
	if newAttemptCount >= r.MaxRetries {
		now := r.now()
		failTime := metav1.NewTime(now)
		worker.AttemptCount = newAttemptCount
		worker.LastFailureReason = reason
		worker.LastFailureTime = &failTime

		instance.SetStatusCondition(
			v1alpha1.ConditionWorkerProvisioningFailed,
			metav1.ConditionTrue,
			fmt.Sprintf("Worker %s: %s (attempt %d/%d)", oldBMIName, message, newAttemptCount, r.MaxRetries),
			reason,
		)
		log.Info("worker max retries exhausted, leaving BMI in place for debugging",
			"worker", oldBMIName,
			"attempts", newAttemptCount,
			"maxRetries", r.MaxRetries,
		)
		return ctrl.Result{}, false, nil
	}

	// Delete the failed BMI. NotFound means the BMI is already gone —
	// treat it as a successful deletion rather than a permanent error.
	if err := r.BMIProvider.DeleteBMI(ctx, worker.BMIName, worker.BMINamespace); err != nil {
		if IsNotFoundError(err) {
			log.Info("BMI already deleted (NotFound), proceeding with replacement",
				"worker", worker.BMIName)
		} else if IsTransientError(err) {
			log.Info("transient error deleting BMI", "worker", worker.BMIName)
			instance.SetStatusCondition(
				v1alpha1.ConditionFulfillmentServiceUnavailable,
				metav1.ConditionTrue,
				sanitizeFeedbackText(fmt.Sprintf("Transient error: %v", err)),
				v1alpha1.ReasonGRPCUnavailable,
			)
			return ctrl.Result{RequeueAfter: provisioning.BackoffBaseDelay}, true, nil
		} else {
			return ctrl.Result{}, false, fmt.Errorf("deleting BMI %s/%s: %w", worker.BMINamespace, worker.BMIName, err)
		}
	}

	// Clear BMIName immediately after successful deletion so that if the
	// subsequent CreateBMI fails, the status does not reference a
	// now-deleted BMI. This prevents the next reconcile from attempting
	// to replace an already-gone instance.
	worker.BMIName = ""

	// Create replacement BMI before mutating retry state, so that a
	// transient CreateBMI failure does not leave stale AttemptCount,
	// NextRetryTime, or LastFailureReason in the worker status.
	workerIndex := r.findWorkerIndex(instance, worker)
	newName, newNamespace, err := r.BMIProvider.CreateBMI(ctx, instance, workerIndex)
	if err != nil {
		if IsTransientError(err) {
			log.Info("transient gRPC error creating replacement BMI",
				"worker", oldBMIName)
			instance.SetStatusCondition(
				v1alpha1.ConditionFulfillmentServiceUnavailable,
				metav1.ConditionTrue,
				sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
				v1alpha1.ReasonGRPCUnavailable,
			)
			return ctrl.Result{RequeueAfter: provisioning.BackoffBaseDelay}, true, nil
		}
		return ctrl.Result{}, false, fmt.Errorf("creating replacement BMI: %w", err)
	}

	// Full delete+create succeeded — now commit the retry-state mutations.
	backoff := ComputeWorkerBackoff(newAttemptCount)
	now := r.now()
	nextRetry := metav1.NewTime(now.Add(backoff))
	failTime := metav1.NewTime(now)

	worker.AttemptCount = newAttemptCount
	worker.NextRetryTime = &nextRetry
	worker.LastFailureReason = reason
	worker.LastFailureTime = &failTime
	worker.BMIName = newName
	worker.BMINamespace = newNamespace
	worker.AttemptStartTime = nil // NextRetryTime is now the timeout anchor

	instance.SetStatusCondition(
		v1alpha1.ConditionWorkerProvisioningFailed,
		metav1.ConditionTrue,
		fmt.Sprintf("Replaced %s with %s (attempt %d/%d, next retry after %s): %s",
			oldBMIName, newName, newAttemptCount, r.MaxRetries, backoff, message),
		v1alpha1.ReasonBMIReplacementTriggered,
	)

	log.Info("BMI replacement triggered",
		"oldBMI", oldBMIName,
		"newBMI", newName,
		"attempt", newAttemptCount,
		"backoff", backoff,
	)

	return ctrl.Result{RequeueAfter: backoff}, false, nil
}

// createReplacementBMI creates a new BMI for a worker whose previous BMI was
// already deleted (BMIName is empty). This handles the case where DeleteBMI
// succeeded but CreateBMI failed transiently, leaving the worker with an empty
// BMIName. Unlike replaceBMI, it skips the deletion step entirely.
func (r *BareMetalWorkerReconciler) createReplacementBMI(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	worker *v1alpha1.WorkerStatus,
) (workerReconcileResult, error) {
	log := ctrllog.FromContext(ctx)

	workerIndex := r.findWorkerIndex(instance, worker)
	newName, newNamespace, err := r.BMIProvider.CreateBMI(ctx, instance, workerIndex)
	if err != nil {
		if IsTransientError(err) {
			log.Info("transient error creating BMI for worker with empty BMIName",
				"workerID", worker.WorkerID)
			instance.SetStatusCondition(
				v1alpha1.ConditionFulfillmentServiceUnavailable,
				metav1.ConditionTrue,
				sanitizeFeedbackText(fmt.Sprintf("Transient error: %v", err)),
				v1alpha1.ReasonGRPCUnavailable,
			)
			return workerReconcileResult{
				requeueAfter:      provisioning.BackoffBaseDelay,
				hadTransientError: true,
			}, nil
		}
		return workerReconcileResult{}, fmt.Errorf("creating BMI for worker %s: %w", worker.WorkerID, err)
	}

	// Creation succeeded — record the new BMI and set timing metadata.
	now := r.now()
	attemptStart := metav1.NewTime(now)
	worker.BMIName = newName
	worker.BMINamespace = newNamespace
	worker.AttemptStartTime = &attemptStart

	log.Info("created BMI for worker with empty BMIName",
		"workerID", worker.WorkerID,
		"newBMI", newName,
		"attempt", worker.AttemptCount,
	)

	return workerReconcileResult{requeueAfter: bootingWorkerRequeueInterval}, nil
}

// getBMICreationTime returns the effective creation time of a worker's BMI
// and a boolean indicating whether a creation timestamp is available.
// When the worker has a NextRetryTime (set during a replacement), that value
// is used as the effective creation time. Otherwise, LastFailureTime is used.
// As a final fallback, AttemptStartTime (recorded when the worker is first
// observed without any other timestamp) is used. If none of the three
// timestamps are set, the second return value is false so the caller can
// record an AttemptStartTime and requeue.
func (r *BareMetalWorkerReconciler) getBMICreationTime(worker *v1alpha1.WorkerStatus) (time.Time, bool) {
	if worker.NextRetryTime != nil {
		return worker.NextRetryTime.Time, true
	}
	if worker.LastFailureTime != nil {
		return worker.LastFailureTime.Time, true
	}
	if worker.AttemptStartTime != nil {
		return worker.AttemptStartTime.Time, true
	}
	// No creation timestamp available — the BMI was just created and has no
	// prior failure history. Return zero time with false to signal that the
	// caller should record an AttemptStartTime.
	return time.Time{}, false
}

// findWorkerIndex returns the index of the worker in the instance's Workers slice,
// matching by stable WorkerID rather than BMIName (which changes on replacement).
func (r *BareMetalWorkerReconciler) findWorkerIndex(instance *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) int {
	for i := range instance.Status.Workers {
		if instance.Status.Workers[i].WorkerID == worker.WorkerID {
			return i
		}
	}
	return 0
}
