// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// reconcileWorkers owns normal worker convergence. Finalization never enters
// this flow: observation repairs precede gates, then durable capacity actions,
// teardown, Agent convergence and the final merged status summary.
func (r *Reconciler) reconcileWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string) (ctrl.Result, error) {
	// One local observation supplies identity, ownership and early Agent phases.
	observed, res, err := r.observeWorkerResources(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}
	changes, err := r.observeExistingWorkers(ctx, co, tenant, observed)
	if err != nil {
		return ctrl.Result{}, err
	}
	if res, err := r.persistWorkerChangesAndRefresh(ctx, co, changes); err != nil || !res.IsZero() {
		return res, err
	}
	if len(changes) > 0 {
		return workerBoundaryRequeue(), nil
	}
	image, ignition, res, err := r.prepareWorkerProvisioning(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}

	res, err = r.reconcileWorkerCapacity(ctx, co, tenant, image, ignition, observed)
	if err != nil || !res.IsZero() {
		return res, err
	}

	return r.finishWorkerConvergence(ctx, co, observed)
}

// A bounded wakeup does not depend on status-only events or cache freshness.
func workerBoundaryRequeue() ctrl.Result {
	return ctrl.Result{RequeueAfter: time.Second}
}

func (r *Reconciler) finishWorkerConvergence(ctx context.Context, co *v1alpha1.ClusterOrder, observed *workerObservation) (ctrl.Result, error) {
	if err := r.refreshWorkerOrder(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileWorkerTeardown(ctx, co, observed); err != nil {
		return ctrl.Result{}, err
	}
	workers, res, err := r.reconcileObservedAgents(ctx, co, observed)
	if err != nil {
		return ctrl.Result{}, err
	}

	npRes, npErr := r.reconcileNodePoolReplicas(ctx, co)
	if npErr != nil {
		return ctrl.Result{}, npErr
	}

	if err := r.updateWorkerStatusWithAgent(ctx, co, workers); err != nil {
		return ctrl.Result{}, err
	}

	if !res.IsZero() {
		return res, nil
	}
	if hasTeardownWorkers(workers) {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}
	return npRes, nil
}
