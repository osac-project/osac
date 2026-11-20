// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"context"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// SetupWithManager watches installation records and the Cluster and control plane
// that supply this provider's inputs.
func (r *AgentClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-capagent-agentcluster").
		For(&agentv1.AgentCluster{}).
		Watches(&hivev1.ClusterDeployment{}, handler.EnqueueRequestsFromMapFunc(r.clustersForInput)).
		Watches(&installv1.AgentClusterInstall{}, handler.EnqueueRequestsFromMapFunc(r.clustersForInput)).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.clustersForInput)).
		Watches(&hypershiftv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(r.clustersForInput)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// clustersForInput queues AgentClusters in the changed input's namespace.
// Reconciliation checks each provider's references; list failures queue nothing.
func (r *AgentClusterReconciler) clustersForInput(ctx context.Context, o client.Object) []reconcile.Request {
	var items agentv1.AgentClusterList

	if err := r.Client.List(ctx, &items, client.InNamespace(o.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list AgentClusters")
		return nil
	}

	var result []reconcile.Request

	for i := range items.Items {
		result = append(result, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&items.Items[i])})
	}

	return result
}

// SetupWithManager watches Agents, Machines and installation readiness for worker
// acquisition, recovery and release.
func (r *AgentMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-capagent-agentmachine").
		For(&agentv1.AgentMachine{}).
		Watches(&assistedv1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		Watches(&capiv1.Machine{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		Watches(&agentv1.AgentCluster{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		Watches(&hypershiftv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.machinesForInput)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// machinesForInput queues local providers for Machine and installation changes.
// Agent changes cross namespaces because hosts and Machines can live separately.
func (r *AgentMachineReconciler) machinesForInput(ctx context.Context, o client.Object) []reconcile.Request {
	namespace := o.GetNamespace()
	if _, isAgent := o.(*assistedv1.Agent); isAgent {
		namespace = ""
	}

	var items agentv1.AgentMachineList

	if err := r.Client.List(ctx, &items, client.InNamespace(namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list AgentMachines")
		return nil
	}

	var result []reconcile.Request

	for i := range items.Items {
		result = append(result, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&items.Items[i])})
	}

	return result
}
