// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package loadbalancer publishes reserved Service addresses for simulated control planes.
// It allocates no network resources and provides no packet connectivity.
package loadbalancer

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const serviceFinalizer = "service.kubernetes.io/load-balancer-cleanup"

// ServiceReconciler writes LoadBalancer status for unclassed Services.
// Service specs, control-plane status and kubeconfig data belong to their producers.
type ServiceReconciler struct {
	Client    client.Client
	APIReader client.Reader
}

// Reconcile models the generic unclassed Service-controller contract. Allocation
// is synthetic; no provider-specific behavior or control-plane readiness is assumed.
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	service := &corev1.Service{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, service); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cleanup := service.DeletionTimestamp != nil || service.Spec.Type != corev1.ServiceTypeLoadBalancer ||
		service.Spec.LoadBalancerClass != nil
	if cleanup && !controllerutil.ContainsFinalizer(service, serviceFinalizer) {
		return ctrl.Result{}, nil
	}

	if !cleanup {
		if err := kube.AddFinalizer(ctx, r.Client, service, serviceFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}

	next := service.DeepCopy()
	if cleanup {
		next.Status.LoadBalancer = corev1.LoadBalancerStatus{}
	} else {
		next.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "192.0.2.10"}}
	}

	if equality.Semantic.DeepEqual(service.Status, next.Status) {
		if cleanup {
			return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, service, serviceFinalizer)
		}

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(service, client.MergeFromWithOptimisticLock{}))
}

// SetupWithManager watches Service creation and changes. Each matching Service has
// an independent response; this controller reads no application resources.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-loadbalancer-service").
		For(&corev1.Service{}).
		WithOptions(kube.ControllerOptions()).
		Complete(r)
}
