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
// this flow. Stages run in dependency order and each ends at its own durable
// boundary: observation and repair, one InfraEnv observation (object, UID and
// current artifact evidence), existing-worker lifecycle (retirement, reservation,
// failed cleanup), creation with lazily resolved inputs, then teardown, Agent
// binding, NodePool replicas and summary. Creation prerequisites never gate
// existing-worker work: a wait or error from the create stage is merged into the
// final scheduling decision instead.
func (r *Reconciler) reconcileWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string) (ctrl.Result, error) {
	// One local observation supplies identity, ownership and early Agent phases.
	observed, res, err := r.observeWorkerResources(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}
	workers, err := r.observeExistingWorkers(ctx, co, tenant, observed)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}

	// InfraEnv object, UID evidence and current artifact evidence: one observation
	// per invocation. Creating the object, replacing stale Ready evidence or
	// persisting a stale-worker classification is a durable boundary; a missing
	// creation prerequisite, an unknown lookup or a foreign object is held so
	// existing-worker work still runs.
	infra, infraRes, infraMutated, infraBoundaryErr := r.observeInfraEnvEvidence(ctx, co)
	if infraMutated {
		return infraRes, infraBoundaryErr
	}

	// Retirement, durable reservations and failed-incarnation cleanup need no
	// creation inputs, so a missing pull secret, ignition or disk image cannot
	// starve them.
	lifecycleRes, err := r.reconcileWorkerLifecycle(ctx, co, tenant)
	if err != nil || !lifecycleRes.IsZero() {
		return lifecycleRes, err
	}

	// At most one Create for the selected due slot, with image, ignition and
	// instance type resolved only now, from the observation made above.
	created, creationRes, creationErr := r.reconcileWorkerCreation(ctx, co, tenant, observed, infra)
	if created {
		return creationRes, creationErr
	}

	// Teardown, Agent binding, NodePool replicas and the summary are independent
	// of creation prerequisites, so they converge even when a create cannot.
	finishRes, err := r.finishWorkerConvergence(ctx, co, observed)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Surface a held observation or creation-prerequisite failure only after
	// independent lifecycle work has been persisted, instead of pretending the
	// invocation completed.
	if infra.err != nil {
		return ctrl.Result{}, infra.err
	}
	if creationErr != nil {
		return ctrl.Result{}, creationErr
	}
	if !finishRes.IsZero() {
		return finishRes, nil
	}
	return mergeWorkerWaits(creationRes, r.workerRecheckDeadline(co.Status.Workers, time.Now())), nil
}

// mergeWorkerWaits keeps the earliest positive recheck delay, clamped so a
// stalled dependency cannot spin. A zero result means no explicit timer and the
// invocation relies on watches.
func mergeWorkerWaits(results ...ctrl.Result) ctrl.Result {
	wait := time.Duration(0)
	for _, res := range results {
		if res.RequeueAfter <= 0 {
			continue
		}
		if wait == 0 || res.RequeueAfter < wait {
			wait = res.RequeueAfter
		}
	}
	if wait == 0 {
		return ctrl.Result{}
	}
	if wait < time.Second {
		wait = time.Second
	}
	return ctrl.Result{RequeueAfter: wait}
}

// A bounded wakeup does not depend on status-only events or cache freshness.
func workerBoundaryRequeue() ctrl.Result {
	return ctrl.Result{RequeueAfter: time.Second}
}

func (r *Reconciler) finishWorkerConvergence(ctx context.Context, co *v1alpha1.ClusterOrder, observed *workerObservation) (ctrl.Result, error) {
	// Earlier stages may have persisted status or annotations; patch from the
	// successful writes' resource version instead of a stale snapshot.
	if err := r.readAuthoritativeOrder(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	// Teardown mutates Agent/BMI state, so it ends the invocation and the next
	// observation supplies fresh evidence; never project from a mutated snapshot.
	stop, err := r.reconcileWorkerTeardown(ctx, co)
	if err != nil {
		return ctrl.Result{}, err
	}
	if stop {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}
	workers, res, err := r.reconcileObservedAgents(ctx, co, co.Status.Workers, observed)
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
