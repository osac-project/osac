// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	agentUnbindingState              = "unbinding-pending-user-action"
	agentUnbindingTimeout            = 30 * time.Minute
	eventReasonAgentUnbindingTimeout = "AgentUnbindingTimeout"
	teardownRequeueInterval          = 30 * time.Second
)

func (r *Reconciler) handleVerifiedClusterDeletion(ctx context.Context, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(co, bmWorkerFinalizer) {
		return ctrl.Result{}, nil
	}
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return ctrl.Result{}, err
	}
	observed, res, err := r.observeWorkerResources(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}
	workers, err := r.observeDeletionWorkers(ctx, co, tenant, observed)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}
	return r.handleClusterDeletion(ctx, co)
}

func (r *Reconciler) handleClusterDeletion(ctx context.Context, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(co, bmWorkerFinalizer) {
		return ctrl.Result{}, nil
	}

	log := ctrllog.FromContext(ctx)
	log.Info("handling cluster deletion", "clusterOrder", co.Name)

	workers := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	changed := false
	for i := range workers {
		if workers[i].Kind != workerKindBMI || workers[i].Phase == workerPhaseUnbinding || workers[i].Phase == workerPhaseDeleting {
			continue
		}
		workers[i].Phase = workerPhaseUnbinding
		now := metav1.Now()
		workers[i].LastFailureTime = &now
		// Cluster deletion is a lifecycle demotion: the healthy interval ends.
		workers[i].ReadySince = nil
		changed = true
	}

	if changed {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}

	// CAP-Agent must unbind the Agent and release its Machine pre-terminate hook
	// before BMaaS tears down the host. Agents that never registered proceed to
	// Deleting immediately; bound Agents are deleted only after unbinding.
	workers = r.reconcileTeardownWorkers(ctx, co, workers)

	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating worker status during cluster deletion: %w", err)
		}
	}

	if len(workers) > 0 {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}

	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-reading ClusterOrder for finalizer removal: %w", err)
	}
	if !sameWorkerOrder(co, latest) {
		return ctrl.Result{}, errWorkerObservationChanged
	}
	// A status merge or another controller can expose new references after the
	// original teardown slice emptied. Never orphan that authoritative status.
	if len(latest.Status.Workers) > 0 {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}
	if controllerutil.RemoveFinalizer(latest, bmWorkerFinalizer) {
		if err := r.Update(ctx, latest); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing baremetalworker finalizer: %w", err)
		}
	}
	log.Info("cluster deletion complete, finalizer removed", "clusterOrder", co.Name)
	return ctrl.Result{}, nil
}

// reconcileWorkerTeardown persists shared cleanup progress and summaries. Its
// boolean marks a return boundary (including pending cleanup), not a status
// change. Never continue Agent convergence from a possibly mutated snapshot.
func (r *Reconciler) reconcileWorkerTeardown(ctx context.Context, co *v1alpha1.ClusterOrder) (bool, error) {
	if !hasTeardownWorkers(co.Status.Workers) {
		return false, nil
	}
	workers := r.reconcileTeardownWorkers(ctx, co, co.Status.Workers)
	if err := r.updateWorkerStatusWithAgent(ctx, co, workers); err != nil {
		return false, fmt.Errorf("persisting worker teardown: %w", err)
	}
	return true, nil
}

// reconcileTeardownWorkers obtains fresh cleanup evidence and handles each
// worker's Unbinding -> Deleting transition in order. A Delete request never
// counts as confirmed BMI absence. Persistence is the caller's named boundary.
func (r *Reconciler) reconcileTeardownWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus) []v1alpha1.WorkerStatus {
	var kept []v1alpha1.WorkerStatus
	for _, w := range workers {
		switch w.Phase {
		case workerPhaseUnbinding, workerPhaseDeleting:
			// Cleanup always rechecks Agent detach/removal, even after Deleting.
		default:
			kept = append(kept, w)
			continue
		}
		gone, err := r.cleanupWorker(ctx, co, &w)
		if err != nil {
			ctrllog.FromContext(ctx).Error(err, "cleaning up worker", "worker", w.Name)
		}
		if !gone {
			kept = append(kept, w)
			continue
		}
		r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerDeleted, "ConfirmDeletion", "worker %s removed after Agent and BMI %s deletion confirmed", w.Name, w.BareMetalInstance.ID)
	}
	return kept
}

func hasTeardownWorkers(workers []v1alpha1.WorkerStatus) bool {
	for _, w := range workers {
		if w.Phase == workerPhaseUnbinding || w.Phase == workerPhaseDeleting {
			return true
		}
	}
	return false
}

func isDetachedKnownUnbound(agent *unstructured.Unstructured, state string) bool {
	if state != "known-unbound" {
		return false
	}
	_, boundToCluster, err := unstructured.NestedFieldNoCopy(agent.Object, "spec", "clusterDeploymentName")
	if err != nil || boundToCluster {
		return false
	}
	_, boundToMachine := agent.GetLabels()["agentMachineRef"]
	return !boundToMachine
}

func (r *Reconciler) checkUnbindingTimeout(
	co *v1alpha1.ClusterOrder, w *v1alpha1.WorkerStatus,
	agent *unstructured.Unstructured, now time.Time,
) {
	if w.LastFailureTime == nil || now.Sub(w.LastFailureTime.Time) <= agentUnbindingTimeout {
		return
	}
	w.LastFailureReason = eventReasonAgentUnbindingTimeout
	w.LastFailureMessage = fmt.Sprintf("agent %s stuck unbinding for > %s", agent.GetName(), agentUnbindingTimeout)
	r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonAgentUnbindingTimeout, "UnbindTimeout",
		"worker %s: agent %s stuck unbinding", w.Name, agent.GetName())
}
