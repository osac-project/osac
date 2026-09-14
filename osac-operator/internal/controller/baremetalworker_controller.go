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
	"fmt"
	"time"

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
	BMIProvider       BMIProvider
	FulfillmentClient FulfillmentClient

	// AgentRegistrationTimeout is the maximum time to wait for an agent to register.
	AgentRegistrationTimeout time.Duration

	// MaxRetries is the maximum number of provisioning attempts per worker slot.
	MaxRetries int

	// now returns the current time (injectable for testing).
	now func() time.Time
}

// NewBareMetalWorkerReconciler creates a reconciler with production defaults.
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

	now := r.now()
	var nextRequeue time.Duration
	allTerminal := true
	hasFailures := false

	for i := range instance.Status.Workers {
		worker := &instance.Status.Workers[i]

		// Check if max retries exhausted
		if worker.AttemptCount >= r.MaxRetries {
			hasFailures = true
			continue
		}

		// Check if worker is ready
		ready, err := r.BMIProvider.IsBMIReady(ctx, worker.BMIName, worker.BMINamespace)
		if err != nil {
			if IsTransientGRPCError(err) {
				log.Info("transient gRPC error checking BMI readiness, setting FulfillmentServiceUnavailable",
					"worker", worker.BMIName)
				instance.SetStatusCondition(
					v1alpha1.ConditionFulfillmentServiceUnavailable,
					metav1.ConditionTrue,
					sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
					v1alpha1.ReasonGRPCUnavailable,
				)
				if nextRequeue == 0 || provisioning.BackoffBaseDelay < nextRequeue {
					nextRequeue = provisioning.BackoffBaseDelay
				}
				allTerminal = false
				continue
			}
			return ctrl.Result{}, fmt.Errorf("checking BMI readiness for %s/%s: %w",
				worker.BMINamespace, worker.BMIName, err)
		}

		if ready {
			allTerminal = false
			// Clear fulfillment service unavailable if it was set
			instance.RemoveStatusCondition(v1alpha1.ConditionFulfillmentServiceUnavailable)
			continue
		}

		allTerminal = false

		// Check backoff window
		if worker.NextRetryTime != nil && now.Before(worker.NextRetryTime.Time) {
			remaining := worker.NextRetryTime.Time.Sub(now)
			if nextRequeue == 0 || remaining < nextRequeue {
				nextRequeue = remaining
			}
			continue
		}

		// Check agent registration timeout
		regTime, err := r.BMIProvider.GetBMIRegistrationTime(ctx, worker.BMIName, worker.BMINamespace)
		if err != nil {
			if IsTransientGRPCError(err) {
				log.Info("transient gRPC error checking agent registration, setting FulfillmentServiceUnavailable",
					"worker", worker.BMIName)
				instance.SetStatusCondition(
					v1alpha1.ConditionFulfillmentServiceUnavailable,
					metav1.ConditionTrue,
					sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
					v1alpha1.ReasonGRPCUnavailable,
				)
				if nextRequeue == 0 || provisioning.BackoffBaseDelay < nextRequeue {
					nextRequeue = provisioning.BackoffBaseDelay
				}
				continue
			}
			return ctrl.Result{}, fmt.Errorf("checking agent registration for %s/%s: %w",
				worker.BMINamespace, worker.BMIName, err)
		}

		if regTime.IsZero() {
			// Agent not yet registered; check if BMI creation exceeded timeout
			bmiCreationTime, hasCreationTime := r.getBMICreationTime(worker)
			if hasCreationTime && now.Sub(bmiCreationTime) >= r.AgentRegistrationTimeout {
				log.Info("agent registration timeout, triggering BMI replacement",
					"worker", worker.BMIName, "timeout", r.AgentRegistrationTimeout)

				result, err := r.replaceBMI(ctx, instance, worker, v1alpha1.ReasonAgentRegistrationTimeout,
					fmt.Sprintf("Agent failed to register within %s", r.AgentRegistrationTimeout))
				if err != nil {
					return ctrl.Result{}, err
				}
				if result.RequeueAfter > 0 && (nextRequeue == 0 || result.RequeueAfter < nextRequeue) {
					nextRequeue = result.RequeueAfter
				}
				continue
			}

			// Still waiting for agent registration
			if hasCreationTime {
				remaining := r.AgentRegistrationTimeout - now.Sub(bmiCreationTime)
				if remaining > 0 && (nextRequeue == 0 || remaining < nextRequeue) {
					nextRequeue = remaining
				}
			} else {
				// No creation timestamp yet; requeue to check again shortly.
				if nextRequeue == 0 || bootingWorkerRequeueInterval < nextRequeue {
					nextRequeue = bootingWorkerRequeueInterval
				}
			}
			continue
		}

		// Agent is registered but BMI is not yet ready — poll periodically.
		if nextRequeue == 0 || bootingWorkerRequeueInterval < nextRequeue {
			nextRequeue = bootingWorkerRequeueInterval
		}
	}

	// Report worker status to fulfillment service
	if r.FulfillmentClient != nil {
		if err := r.FulfillmentClient.ReportWorkerStatus(ctx, instance.Name, instance.Status.Workers); err != nil {
			if IsTransientGRPCError(err) {
				log.Info("transient gRPC error reporting worker status",
					"clusterOrder", instance.Name)
				instance.SetStatusCondition(
					v1alpha1.ConditionFulfillmentServiceUnavailable,
					metav1.ConditionTrue,
					sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
					v1alpha1.ReasonGRPCUnavailable,
				)
			} else {
				return ctrl.Result{}, fmt.Errorf("reporting worker status: %w", err)
			}
		}
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

// replaceBMI deletes the current BMI and creates a replacement, updating the
// worker status with backoff timing and failure information.
func (r *BareMetalWorkerReconciler) replaceBMI(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	worker *v1alpha1.WorkerStatus,
	reason, message string,
) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Capture the old BMI name before any mutation so log entries and
	// condition messages correctly reference the replaced instance.
	oldBMIName := worker.BMIName

	// Delete the failed BMI
	if err := r.BMIProvider.DeleteBMI(ctx, worker.BMIName, worker.BMINamespace); err != nil {
		if IsTransientGRPCError(err) {
			log.Info("transient gRPC error deleting BMI", "worker", worker.BMIName)
			instance.SetStatusCondition(
				v1alpha1.ConditionFulfillmentServiceUnavailable,
				metav1.ConditionTrue,
				sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
				v1alpha1.ReasonGRPCUnavailable,
			)
			return ctrl.Result{RequeueAfter: provisioning.BackoffBaseDelay}, nil
		}
		return ctrl.Result{}, fmt.Errorf("deleting BMI %s/%s: %w", worker.BMINamespace, worker.BMIName, err)
	}

	// Increment attempt count
	worker.AttemptCount++

	// Check if max retries exhausted after increment
	if worker.AttemptCount >= r.MaxRetries {
		now := r.now()
		failTime := metav1.NewTime(now)
		worker.LastFailureReason = reason
		worker.LastFailureTime = &failTime

		instance.SetStatusCondition(
			v1alpha1.ConditionWorkerProvisioningFailed,
			metav1.ConditionTrue,
			fmt.Sprintf("Worker %s: %s (attempt %d/%d)", oldBMIName, message, worker.AttemptCount, r.MaxRetries),
			reason,
		)
		log.Info("worker max retries exhausted",
			"worker", oldBMIName,
			"attempts", worker.AttemptCount,
			"maxRetries", r.MaxRetries,
		)
		return ctrl.Result{}, nil
	}

	// Compute backoff delay
	backoff := ComputeWorkerBackoff(worker.AttemptCount)
	now := r.now()
	nextRetry := metav1.NewTime(now.Add(backoff))
	failTime := metav1.NewTime(now)

	worker.NextRetryTime = &nextRetry
	worker.LastFailureReason = reason
	worker.LastFailureTime = &failTime

	// Create replacement BMI
	workerIndex := r.findWorkerIndex(instance, worker)
	newName, newNamespace, err := r.BMIProvider.CreateBMI(ctx, instance, workerIndex)
	if err != nil {
		if IsTransientGRPCError(err) {
			log.Info("transient gRPC error creating replacement BMI",
				"worker", oldBMIName)
			instance.SetStatusCondition(
				v1alpha1.ConditionFulfillmentServiceUnavailable,
				metav1.ConditionTrue,
				sanitizeFeedbackText(fmt.Sprintf("Transient gRPC error: %v", err)),
				v1alpha1.ReasonGRPCUnavailable,
			)
			return ctrl.Result{RequeueAfter: provisioning.BackoffBaseDelay}, nil
		}
		return ctrl.Result{}, fmt.Errorf("creating replacement BMI: %w", err)
	}

	worker.BMIName = newName
	worker.BMINamespace = newNamespace

	instance.SetStatusCondition(
		v1alpha1.ConditionWorkerProvisioningFailed,
		metav1.ConditionTrue,
		fmt.Sprintf("Replaced %s with %s (attempt %d/%d, next retry after %s): %s",
			oldBMIName, newName, worker.AttemptCount, r.MaxRetries, backoff, message),
		v1alpha1.ReasonBMIReplacementTriggered,
	)

	log.Info("BMI replacement triggered",
		"oldBMI", oldBMIName,
		"newBMI", newName,
		"attempt", worker.AttemptCount,
		"backoff", backoff,
	)

	return ctrl.Result{RequeueAfter: backoff}, nil
}

// getBMICreationTime returns the effective creation time of a worker's BMI
// and a boolean indicating whether a creation timestamp is available.
// When the worker has a NextRetryTime (set during a replacement), that value
// is used as the effective creation time. Otherwise, LastFailureTime is used.
// If neither timestamp is set (initial attempt with no failure history), the
// second return value is false so the caller can distinguish "no timestamp yet"
// from "timed out" and avoid a false-positive timeout on the first invocation.
func (r *BareMetalWorkerReconciler) getBMICreationTime(worker *v1alpha1.WorkerStatus) (time.Time, bool) {
	if worker.NextRetryTime != nil {
		return worker.NextRetryTime.Time, true
	}
	if worker.LastFailureTime != nil {
		return worker.LastFailureTime.Time, true
	}
	// No creation timestamp available — the BMI was just created and has no
	// prior failure history. Return zero time with false to signal that the
	// caller should not evaluate a timeout yet.
	return time.Time{}, false
}

// findWorkerIndex returns the index of the worker in the instance's Workers slice.
func (r *BareMetalWorkerReconciler) findWorkerIndex(instance *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) int {
	for i := range instance.Status.Workers {
		if instance.Status.Workers[i].BMIName == worker.BMIName {
			return i
		}
	}
	return 0
}
