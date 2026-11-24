// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package hcco represents the omitted guest version observation on HostedControlPlane.
// The real hosted-cluster configuration operator obtains it from the guest CVO.
package hcco

import (
	"context"
	"time"

	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// StatusReconciler supplies a fixed healthy guest-version condition. Guest CVO
// history, upgrade completion and workload health require a guest API and are omitted.
type StatusReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Scope     kube.Scope
}

// Reconcile supplies a fixed healthy guest-version observation for served HCPs.
// Guest execution is omitted; CPO availability is an independent observation.
func (r *StatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	controlPlane := &hypershiftv1.HostedControlPlane{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, controlPlane); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if controlPlane.DeletionTimestamp != nil ||
		controlPlane.Spec.Platform.Type != hypershiftv1.NonePlatform || controlPlane.Spec.Platform.Agent == nil ||
		!r.Scope.Contains(controlPlane.Spec.Platform.Agent.AgentNamespace) {
		return ctrl.Result{}, nil
	}

	status, reason := metav1.ConditionFalse, "SimulatedGuestVersion"

	next := controlPlane.DeepCopy()
	meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
		Type: "ClusterVersionFailing", Status: status, Reason: reason,
		Message: "Simulated provider observation", ObservedGeneration: controlPlane.Generation,
	})

	if equality.Semantic.DeepEqual(controlPlane.Status, next.Status) {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(controlPlane, client.MergeFromWithOptimisticLock{}))
}

// SetupWithManager watches HCP changes and skips unchanged guest observations.
func (r *StatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-hcco-status").
		For(&hypershiftv1.HostedControlPlane{}).
		WithOptions(kube.ControllerOptions()).
		Complete(r)
}
