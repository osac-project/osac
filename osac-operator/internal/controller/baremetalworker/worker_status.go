// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	workerKindBMI = "BareMetalInstance"

	workerPhaseProvisioning    = "Provisioning"
	workerPhaseWaitingForAgent = "WaitingForAgent"
	workerPhaseBinding         = "Binding"
	workerPhaseReady           = "Ready"
	workerPhaseFailed          = "Failed"
	workerPhaseUnbinding       = "Unbinding"
	workerPhaseDeleting        = "Deleting"

	reasonWorkersFailed        = "WorkersRetrying"
	reasonWorkersFailedCleared = "AllWorkersHealthy"

	reasonFulfillmentUnavailable = "FulfillmentServiceUnavailable"
	reasonFulfillmentAvailable   = "FulfillmentServiceAvailable"
	unavailableBackoff           = 5 * time.Minute
)

var errWorkerObservationChanged = errors.New("worker observation changed; reconcile again")

// workerChange is concrete evidence about one persisted slot, not a slice index.
// A nil replacement means removal of that exact observed slot.
type workerChange struct {
	observed    v1alpha1.WorkerStatus
	replacement *v1alpha1.WorkerStatus
}

func (r *Reconciler) updateWorkerStatus(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	changes := workerDifferences(co.Status.Workers, workers)
	stale := false
	err := r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		stale = !mergeWorkerChanges(co, latest, changes, true)
	})
	if err != nil {
		return err
	}
	if stale {
		return errWorkerObservationChanged
	}
	return r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)
}

// updateWorkerStatusWithAgent patches status.workers, aggregate counts, and the
// WorkersFailed condition on the ClusterOrder, re-reading and retrying on conflict.
// It also resets attemptCount for workers that have been Ready for MinHealthyDuration.
func (r *Reconciler) updateWorkerStatusWithAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	log := ctrllog.FromContext(ctx)
	workers = append([]v1alpha1.WorkerStatus(nil), workers...)
	resetHealthyWorkers(log, co, workers)
	changes := workerDifferences(co.Status.Workers, workers)
	stale := false
	err := r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		stale = !mergeWorkerChanges(co, latest, changes, false)
		if stale {
			return
		}
		desired, current, ready := computeWorkerAggregates(latest.Status.Workers)
		latest.Status.DesiredWorkers = &desired
		latest.Status.CurrentWorkers = &current
		latest.Status.ReadyWorkers = &ready

		failedMsg := FormatWorkersFailed(latest.Status.Workers)
		switch {
		case failedMsg != "":
			latest.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
				metav1.ConditionTrue, failedMsg, reasonWorkersFailed)
		case apimeta.IsStatusConditionTrue(latest.Status.Conditions, v1alpha1.ConditionWorkersFailed):
			latest.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
				metav1.ConditionFalse, "all workers healthy", reasonWorkersFailedCleared)
		}
	})
	if err != nil {
		return err
	}
	if stale {
		return errWorkerObservationChanged
	}
	return r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)
}

func (r *Reconciler) persistWorkerChangesAndRefresh(ctx context.Context, co *v1alpha1.ClusterOrder, changes []workerChange) (ctrl.Result, error) {
	if len(changes) == 0 {
		return ctrl.Result{}, nil
	}
	stale := false
	err := r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		stale = false
		if !sameWorkerOrder(co, latest) {
			stale = true
			return
		}
		// Validate the entire set first: do not partly apply stale evidence.
		candidate := latest.DeepCopy()
		for _, change := range changes {
			if !applyWorkerChange(candidate, change) {
				stale = true
				return
			}
		}
		latest.Status.Workers = candidate.Status.Workers
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("persisting observed workers: %w", err)
	}
	if err := r.refreshWorkerOrder(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	if stale {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) refreshWorkerOrder(ctx context.Context, co *v1alpha1.ClusterOrder) error {
	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		return err
	}
	if !sameWorkerOrder(co, latest) {
		return errWorkerObservationChanged
	}
	*co = *latest
	return nil
}

// patchStatusWithRetry re-reads the ClusterOrder and applies the mutate function to its status,
// retrying on conflict with optimistic locking.
func (r *Reconciler) patchStatusWithRetry(
	ctx context.Context, co *v1alpha1.ClusterOrder, mutate func(*v1alpha1.ClusterOrder),
) error {
	key := client.ObjectKeyFromObject(co)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		if !sameWorkerOrder(co, latest) {
			return errWorkerObservationChanged
		}
		base := latest.DeepCopy()
		mutate(latest)
		if reflect.DeepEqual(base.Status, latest.Status) {
			return nil
		}
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func sameWorkerOrder(observed, latest *v1alpha1.ClusterOrder) bool {
	return latest.UID == observed.UID && latest.Annotations["osac.openshift.io/tenant"] == observed.Annotations["osac.openshift.io/tenant"] &&
		latest.Labels[clusterOrderIDLabel] == observed.Labels[clusterOrderIDLabel] && latest.DeletionTimestamp.Equal(observed.DeletionTimestamp)
}

// checkCurrentCapacityPlan is an authoritative interruption boundary before an
// external action. Conditions may change, but spec, tenancy, deletion and all
// planned slots must still describe the plan. No provider call occurs in a
// conflict-retry callback. The expectation never advances after an external action.
func (r *Reconciler) checkCurrentCapacityPlan(ctx context.Context, expected *v1alpha1.ClusterOrder) error {
	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return err
	}
	if !sameWorkerOrder(expected, latest) || expected.Generation != latest.Generation || !reflect.DeepEqual(expected.Spec, latest.Spec) || !reflect.DeepEqual(expected.Status.Workers, latest.Status.Workers) {
		return errWorkerObservationChanged
	}
	return nil
}

// workerDifferences includes only previously observed slots. Capacity additions
// belong to the authoritative reservation callback, never to this merge.
func workerDifferences(before, after []v1alpha1.WorkerStatus) []workerChange {
	next := make(map[string]v1alpha1.WorkerStatus, len(after))
	for _, w := range after {
		next[w.Name] = w
	}
	var changes []workerChange
	for _, w := range before {
		if replacement, ok := next[w.Name]; ok {
			changes = appendWorkerDifference(changes, w, replacement)
		} else {
			changes = append(changes, workerChange{observed: w})
		}
	}
	return changes
}

func appendWorkerDifference(changes []workerChange, before, after v1alpha1.WorkerStatus) []workerChange {
	if reflect.DeepEqual(before, after) {
		return changes
	}
	return append(changes, workerChange{observed: before, replacement: &after})
}

func mergeWorkerChanges(co, latest *v1alpha1.ClusterOrder, changes []workerChange, capacity bool) bool {
	if !sameWorkerOrder(co, latest) {
		return false
	}
	if capacity && (co.Generation != latest.Generation || !reflect.DeepEqual(co.Spec, latest.Spec)) {
		return false
	}
	candidate := latest.DeepCopy()
	for _, change := range changes {
		if !applyWorkerChange(candidate, change) {
			return false
		}
	}
	latest.Status.Workers = candidate.Status.Workers
	return true
}

func applyWorkerChange(latest *v1alpha1.ClusterOrder, change workerChange) bool {
	for i := range latest.Status.Workers {
		w := &latest.Status.Workers[i]
		if w.Name != change.observed.Name {
			continue
		}
		// Identity, lifecycle, clocks and failure history must still describe the
		// observation. Unrelated appended workers and order status fields survive.
		if !reflect.DeepEqual(*w, change.observed) {
			return false
		}
		if change.replacement == nil {
			latest.Status.Workers = append(latest.Status.Workers[:i], latest.Status.Workers[i+1:]...)
		} else {
			*w = *change.replacement
		}
		return true
	}
	return false
}

// handleFulfillmentError converts service unavailability into a backoff only
// after persisting its condition. Other errors pass through unchanged.
func (r *Reconciler) handleFulfillmentError(ctx context.Context, co *v1alpha1.ClusterOrder, err error) (ctrl.Result, error) {
	if !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		return ctrl.Result{}, err
	}
	ctrllog.FromContext(ctx).Info("fulfillment service unavailable, backing off")
	if condErr := r.setFulfillmentServiceUnavailable(ctx, co, metav1.ConditionTrue,
		reasonFulfillmentUnavailable, err.Error()); condErr != nil {
		return ctrl.Result{}, fmt.Errorf("persisting fulfillment service unavailable condition: %w", condErr)
	}
	return ctrl.Result{RequeueAfter: unavailableBackoff}, nil
}

func (r *Reconciler) setFulfillmentServiceUnavailable(
	ctx context.Context, co *v1alpha1.ClusterOrder, condStatus metav1.ConditionStatus, reason, message string,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionFulfillmentServiceUnavailable, condStatus, message, reason)
	})
}

func computeWorkerAggregates(workers []v1alpha1.WorkerStatus) (desired, current, ready int32) {
	for _, w := range workers {
		desired++
		switch w.Phase {
		case workerPhaseProvisioning, workerPhaseWaitingForAgent, workerPhaseBinding, workerPhaseReady,
			workerPhaseUnbinding, workerPhaseDeleting:
			current++
		}
		if w.Phase == workerPhaseReady {
			ready++
		}
	}
	return
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

func newWorkerStatus(nodeSet, instanceType, name, resourceID, phase string) v1alpha1.WorkerStatus {
	return v1alpha1.WorkerStatus{
		NodeSet:           nodeSet,
		InstanceType:      instanceType,
		Name:              name,
		Kind:              workerKindBMI,
		BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: name, ID: resourceID},
		Phase:             phase,
		CreationTimestamp: metav1.Now(),
	}
}

// workerCreated preserves the slot's history while recording the external identity.
func workerCreated(w *v1alpha1.WorkerStatus, name, id string) {
	w.BareMetalInstance = v1alpha1.BareMetalInstanceReference{Name: name, ID: id}
	w.Phase = workerPhaseWaitingForAgent
	w.NextRetryTime = nil
	if w.CreationTimestamp.IsZero() {
		w.CreationTimestamp = metav1.Now()
	}
}

func initializeReadySince(workers []v1alpha1.WorkerStatus) {
	now := metav1.Now()
	for i := range workers {
		if eligibleForAgentObservation(workers[i]) && workers[i].Phase == workerPhaseReady && workers[i].ReadySince == nil {
			workers[i].ReadySince = &now
		}
	}
}

func workerSlicesEqual(a, b []v1alpha1.WorkerStatus) bool {
	return reflect.DeepEqual(a, b)
}

func countWorkersInPhase(workers []v1alpha1.WorkerStatus, phase string) int {
	n := 0
	for i := range workers {
		if workers[i].Phase == phase {
			n++
		}
	}
	return n
}

func setWorkerPhase(workers []v1alpha1.WorkerStatus, name, phase string) {
	for i := range workers {
		if workers[i].Name == name {
			workers[i].Phase = phase
			return
		}
	}
}

func workerByName(workers []v1alpha1.WorkerStatus, name string) *v1alpha1.WorkerStatus {
	for i := range workers {
		if workers[i].Name == name {
			return &workers[i]
		}
	}
	return nil
}
