// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// patchStatusFromBase applies a single optimistic status patch. The caller must
// have obtained base from an authoritative read before making its observation or
// decision; a conflict deliberately aborts this invocation.
func (r *Reconciler) patchStatusFromBase(
	ctx context.Context, base, next *v1alpha1.ClusterOrder,
) error {
	if reflect.DeepEqual(base.Status, next.Status) {
		return nil
	}
	return r.Status().Patch(ctx, next, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// patchStatus reads the authoritative order before applying a status-only
// mutation. It is used for condition writes that do not depend on an earlier
// external observation.
func (r *Reconciler) patchStatus(
	ctx context.Context, co *v1alpha1.ClusterOrder, mutate func(*v1alpha1.ClusterOrder),
) error {
	latest := co.DeepCopy()
	if err := r.readAuthoritativeOrder(ctx, latest); err != nil {
		return err
	}
	if !sameWorkerOrder(co, latest) {
		return errWorkerObservationChanged
	}
	base := latest.DeepCopy()
	mutate(latest)
	return r.patchStatusFromBase(ctx, base, latest)
}

func (r *Reconciler) readAuthoritativeOrder(ctx context.Context, co *v1alpha1.ClusterOrder) error {
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	latest := &v1alpha1.ClusterOrder{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		return err
	}
	*co = *latest
	return nil
}

func (r *Reconciler) updateWorkerStatus(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	next := co.DeepCopy()
	next.Status.Workers = append([]v1alpha1.WorkerStatus(nil), workers...)
	// Counts are derived from the intent and the journal, so they are persisted
	// with every worker write instead of lagging one reconcile behind it. The
	// failure summary follows the same rule: a retired failed record stops
	// reporting as soon as its retirement intent is durable.
	if err := setWorkerSummary(next); err != nil {
		return err
	}
	setWorkersFailedCondition(next)
	return r.patchStatusFromBase(ctx, co, next)
}

// updateWorkerStatusWithAgent patches status.workers, aggregate counts, and the
// WorkersFailed condition on the ClusterOrder. It also resets attemptCount for
// workers that have been Ready for MinHealthyDuration.
func (r *Reconciler) updateWorkerStatusWithAgent(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	log := ctrllog.FromContext(ctx)
	now := time.Now()
	workers = append([]v1alpha1.WorkerStatus(nil), workers...)
	projectReadySince(workers, now)
	resetHealthyWorkers(log, co, workers, now)
	next := co.DeepCopy()
	next.Status.Workers = workers
	if err := setWorkerSummary(next); err != nil {
		return err
	}
	setWorkersFailedCondition(next)
	return r.patchStatusFromBase(ctx, co, next)
}

// setWorkersFailedCondition reports actionable failures from the worker journal.
// A retired (Unbinding/Deleting) or otherwise non-selected failed record is no
// longer actionable, so it does not keep an otherwise converged order retrying.
func setWorkersFailedCondition(co *v1alpha1.ClusterOrder) {
	failedMsg := FormatWorkersFailed(co.Status.Workers)
	switch {
	case failedMsg != "":
		co.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
			metav1.ConditionTrue, failedMsg, reasonWorkersFailed)
	case apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionWorkersFailed):
		co.SetStatusCondition(v1alpha1.ConditionWorkersFailed,
			metav1.ConditionFalse, "all workers healthy", reasonWorkersFailedCleared)
	}
}

func sameWorkerOrder(observed, latest *v1alpha1.ClusterOrder) bool {
	return latest.UID == observed.UID && latest.Annotations["osac.openshift.io/tenant"] == observed.Annotations["osac.openshift.io/tenant"] &&
		latest.Labels[clusterOrderIDLabel] == observed.Labels[clusterOrderIDLabel] && latest.DeletionTimestamp.Equal(observed.DeletionTimestamp)
}

func sameCapacityPlan(expected, latest *v1alpha1.ClusterOrder) bool {
	return sameWorkerOrder(expected, latest) && expected.Generation == latest.Generation &&
		reflect.DeepEqual(expected.Spec, latest.Spec) && reflect.DeepEqual(expected.Status.Workers, latest.Status.Workers)
}

// checkCurrentCapacityPlan is an authoritative interruption boundary before an
// external action. Conditions may change, but spec, tenancy, deletion and all
// planned slots must still describe the plan. A stale plan returns before the
// action; a persistence conflict aborts the invocation rather than retrying
// inside it. The expectation never advances after an external action.
func (r *Reconciler) checkCurrentCapacityPlan(ctx context.Context, expected *v1alpha1.ClusterOrder) error {
	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return err
	}
	if !sameCapacityPlan(expected, latest) {
		return errWorkerObservationChanged
	}
	return nil
}

// handleFulfillmentError is the single orchestration boundary for fulfillment
// availability evidence. Callers return the adapter's wrapped error unchanged;
// this boundary persists the order-scoped condition exactly once and applies the
// one bounded unavailable delay. A condition-write failure is surfaced instead
// of pretending the outage was recorded, and every non-availability error keeps
// its own caller policy (authoritative absence, ownership, retry, ...).
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
	return r.patchStatus(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionFulfillmentServiceUnavailable, condStatus, message, reason)
	})
}

// errWorkerCountOverflow reports a requested capacity that cannot be represented
// in the status int32 count fields. Current schema limits keep this unreachable,
// but the conversion is checked instead of silently wrapping.
var errWorkerCountOverflow = errors.New("requested worker capacity overflows int32")

// workerCountSummary is the single desired/current/ready derivation shared by
// status persistence, metrics and readiness consumers.
type workerCountSummary struct {
	desired int32
	current int32
	ready   int32
}

// setWorkerSummary stores the summary derived from the order's intent and its
// own worker journal. It is the only place the three count fields are written.
func setWorkerSummary(co *v1alpha1.ClusterOrder) error {
	summary, err := summarizeWorkerCounts(co, co.Status.Workers)
	if err != nil {
		return err
	}
	co.Status.DesiredWorkers = &summary.desired
	co.Status.CurrentWorkers = &summary.current
	co.Status.ReadyWorkers = &summary.ready
	return nil
}

// summarizeWorkerCounts derives desired capacity from the requested bare-metal
// NodeSets and current/ready availability from the retained worker slots.
//
// Desired is the user's intent, independent of the status journal, so it is
// visible before any reservation exists. Current counts retained slots that hold
// a verified BMI identity in an active provisioning/binding/ready phase; ready
// is the Ready subset. Identity-less reservations, Failed records, retiring
// entries and capacity surplus never count, and readiness is partitioned per
// NodeSet so surplus in one set cannot compensate for another.
func summarizeWorkerCounts(co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus) (workerCountSummary, error) {
	desired, err := sumBareMetalNodeRequests(co)
	if err != nil {
		return workerCountSummary{}, err
	}
	summary := workerCountSummary{desired: desired}
	plan := planWorkerSlotsFor(co, workers)
	for i := range plan.selected {
		w := &plan.selected[i]
		if w.BareMetalInstance.ID == "" {
			continue
		}
		switch w.Phase {
		case workerPhaseProvisioning, workerPhaseWaitingForAgent, workerPhaseBinding, workerPhaseReady:
			summary.current++
		}
		if w.Phase == workerPhaseReady {
			summary.ready++
		}
	}
	return summary, nil
}

// sumBareMetalNodeRequests sums the positive requested capacity for bare-metal
// NodeSets with a checked int32 conversion. Non-bare-metal and non-positive
// requests are ignored; request validation owns rejecting them elsewhere.
func sumBareMetalNodeRequests(co *v1alpha1.ClusterOrder) (int32, error) {
	total := 0
	for i := range co.Spec.NodeRequests {
		nr := &co.Spec.NodeRequests[i]
		if !nr.IsBareMetal() || nr.NumberOfNodes <= 0 {
			continue
		}
		if total > math.MaxInt32-nr.NumberOfNodes {
			return 0, errWorkerCountOverflow
		}
		total += nr.NumberOfNodes
	}
	return int32(total), nil
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
	now := metav1.Now()
	return v1alpha1.WorkerStatus{
		NodeSet:           nodeSet,
		InstanceType:      instanceType,
		Name:              name,
		Kind:              workerKindBMI,
		BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: name, ID: resourceID},
		Phase:             phase,
		CreationTimestamp: now,
		// A freshly constructed slot starts its attempt now; the origin is part of
		// the reservation write, so it is durable before any external Create.
		AttemptStartedAt: &now,
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

// projectReadySince maintains the continuous healthy interval. Entering Ready
// starts an interval; leaving Ready for any reason, including a demotion,
// failure or cleanup transition, clears the previous interval. Disjoint healthy
// periods are therefore never accumulated for the healthy-reset decision.
func projectReadySince(workers []v1alpha1.WorkerStatus, now time.Time) {
	for i := range workers {
		if workers[i].Phase == workerPhaseReady && eligibleForAgentObservation(workers[i]) {
			if workers[i].ReadySince == nil {
				stamp := metav1.NewTime(now)
				workers[i].ReadySince = &stamp
			}
			continue
		}
		workers[i].ReadySince = nil
	}
}

func workerSlicesEqual(a, b []v1alpha1.WorkerStatus) bool {
	return reflect.DeepEqual(a, b)
}

func workerStatusesEqual(a, b []v1alpha1.WorkerStatus) bool {
	if len(a) != len(b) {
		return false
	}
	left := make(map[string]v1alpha1.WorkerStatus, len(a))
	for _, worker := range a {
		left[worker.Name] = worker
	}
	for _, worker := range b {
		other, ok := left[worker.Name]
		if !ok || !reflect.DeepEqual(other, worker) {
			return false
		}
	}
	return true
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

func workerByName(workers []v1alpha1.WorkerStatus, name string) *v1alpha1.WorkerStatus {
	for i := range workers {
		if workers[i].Name == name {
			return &workers[i]
		}
	}
	return nil
}
