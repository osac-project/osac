// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// SetupWithManager watches the Agent and the resources it uses for installation.
func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-assisted-agent").
		For(&assistedv1.Agent{}).
		Watches(&assistedv1.InfraEnv{}, handler.EnqueueRequestsFromMapFunc(r.agentsForInput)).
		Watches(&hivev1.ClusterDeployment{}, handler.EnqueueRequestsFromMapFunc(r.agentsForInput)).
		Watches(&installv1.AgentClusterInstall{}, handler.EnqueueRequestsFromMapFunc(r.agentsForInput)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.agentsForInput)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// SetupWithManager watches InfraEnvs in the configured Agent namespaces.
func (r *InfraEnvReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-assisted-infraenv").
		For(&assistedv1.InfraEnv{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.infraEnvsForSecret)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// SetupWithManager follows native deployment and installation events.
func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-assisted-deployment").
		For(&hivev1.ClusterDeployment{}).
		Watches(&installv1.AgentClusterInstall{}, handler.EnqueueRequestsFromMapFunc(deploymentForInstallation)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// infraEnvsForSecret wakes discovery inputs when their pull credentials change.
func (r *InfraEnvReconciler) infraEnvsForSecret(ctx context.Context, object client.Object) []reconcile.Request {
	if !r.Scope.Contains(object.GetNamespace()) {
		return nil
	}

	var environments assistedv1.InfraEnvList

	if err := r.APIReader.List(ctx, &environments, client.InNamespace(object.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list InfraEnvs for pull secret")
		return nil
	}

	var requests []reconcile.Request

	for i := range environments.Items {
		infra := &environments.Items[i]
		if infra.Spec.PullSecretRef != nil && infra.Spec.PullSecretRef.Name == object.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(infra)})
		}
	}

	return requests
}

// agentsForInput queues Agents using the changed discovery environment or deployment.
// Installation events follow their deployment reference, which can name another namespace.
func (r *AgentReconciler) agentsForInput(ctx context.Context, object client.Object) []reconcile.Request {
	deploymentName := object.GetName()
	if installation, ok := object.(*installv1.AgentClusterInstall); ok {
		deploymentName = installation.Spec.ClusterDeploymentRef.Name
		if deploymentName == "" {
			return nil
		}
	}

	var requests []reconcile.Request

	for _, namespace := range r.Scope.AgentNamespaces {
		var agents assistedv1.AgentList

		if err := r.APIReader.List(ctx, &agents, client.InNamespace(namespace)); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "list Agents for changed input")

			return nil
		}

		for i := range agents.Items {
			agent := &agents.Items[i]

			ref := agent.Spec.ClusterDeploymentName
			matches := ref != nil && ref.Namespace == object.GetNamespace() && ref.Name == deploymentName
			if _, discovery := object.(*assistedv1.InfraEnv); discovery {
				matches = agent.Namespace == object.GetNamespace() && agent.Labels[kube.InfraEnvLabel] == object.GetName()
			}

			if _, credential := object.(*corev1.Secret); credential {
				matches = true
			}

			if matches {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(agent)})
			}
		}
	}

	return requests
}

// deploymentForInstallation maps ordinary installation ownership through its native reference.
func deploymentForInstallation(_ context.Context, object client.Object) []ctrl.Request {
	installation := object.(*installv1.AgentClusterInstall)
	if installation.Spec.ClusterDeploymentRef.Name == "" {
		return nil
	}

	return []ctrl.Request{
		{
			NamespacedName: client.ObjectKey{
				Namespace: installation.Namespace,
				Name:      installation.Spec.ClusterDeploymentRef.Name,
			},
		},
	}
}
