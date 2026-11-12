// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// SetupWithManager registers separate Cluster, MachineSet and
// Machine controllers. Child changes wake their owners; Cluster changes also wake
// MachineSets that may be waiting for infrastructure readiness.
func SetupWithManager(mgr ctrl.Manager, reader client.Reader) error {
	c := &ClusterReconciler{Client: mgr.GetClient(), APIReader: reader}
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("simulated-capi-cluster").
		For(&capiv1.Cluster{}).
		Watches(&agentv1.AgentCluster{}, handler.EnqueueRequestsFromMapFunc(c.clustersForProvider)).
		Watches(&hypershiftv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(c.clustersForProvider)).
		Owns(&capiv1.MachineSet{}, builder.MatchEveryOwner).
		WithOptions(kube.ControllerOptions()).
		Complete(c); err != nil {
		return err
	}

	s := &MachineSetReconciler{Client: mgr.GetClient(), APIReader: reader}
	if err := ctrl.NewControllerManagedBy(mgr).
		Named("simulated-capi-machineset").
		For(&capiv1.MachineSet{}).
		Owns(&capiv1.Machine{}).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(s.setsForCluster)).
		WithOptions(kube.ControllerOptions()).
		Complete(s); err != nil {
		return err
	}

	m := &MachineReconciler{Client: mgr.GetClient(), APIReader: reader}

	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-capi-machine").
		For(&capiv1.Machine{}).
		Owns(&agentv1.AgentMachine{}).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(m.machinesForCluster)).
		WithOptions(kube.ControllerOptions()).
		Complete(m)
}

// clustersForProvider wakes referenced providers before CAPI has adopted their ownership.
func (r *ClusterReconciler) clustersForProvider(ctx context.Context, object client.Object) []reconcile.Request {
	var clusters capiv1.ClusterList

	if err := r.Client.List(ctx, &clusters, client.InNamespace(object.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list Clusters for provider")
		return nil
	}

	var requests []reconcile.Request

	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		ref := cluster.Spec.InfrastructureRef
		if _, controlPlane := object.(*hypershiftv1.HostedControlPlane); controlPlane {
			ref = cluster.Spec.ControlPlaneRef
		}

		if ref != nil && ref.Name == object.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		}
	}

	return requests
}

// machinesForCluster resumes Machines when their Cluster's pause or inputs change.
func (r *MachineReconciler) machinesForCluster(ctx context.Context, object client.Object) []reconcile.Request {
	var machines capiv1.MachineList

	if err := r.Client.List(
		ctx,
		&machines,
		client.InNamespace(object.GetNamespace()),
		client.MatchingLabels{kube.ClusterLabel: object.GetName()},
	); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list Machines for Cluster")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(machines.Items))
	for i := range machines.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&machines.Items[i])})
	}

	return requests
}

// setsForCluster queues MachineSets whose clusterName matches the changed Cluster
// in the same namespace. A list failure is logged and queues no requests.
func (r *MachineSetReconciler) setsForCluster(ctx context.Context, o client.Object) []reconcile.Request {
	var sets capiv1.MachineSetList

	if err := r.Client.List(ctx, &sets, client.InNamespace(o.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list MachineSets")
		return nil
	}

	var result []reconcile.Request

	for i := range sets.Items {
		if sets.Items[i].Spec.ClusterName == o.GetName() {
			result = append(result, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&sets.Items[i])})
		}
	}

	ctrl.LoggerFrom(ctx).V(4).Info("mapped Cluster to MachineSets",
		"cluster", o.GetName(),
		"namespace", o.GetNamespace(),
		"uid", o.GetUID(),
		"targets", len(result),
	)

	return result
}
