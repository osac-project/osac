// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"fmt"
	"time"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const (
	deploymentFinalizer   = "clusterdeployments.agent-install.openshift.io/ai-deprovision"
	installationFinalizer = "agentclusterinstall.agent-install.openshift.io/ai-deprovision"
)

// DeploymentReconciler owns installation records and holds deletion until Assisted cleanup finishes.
// Backend execution is omitted; native Agent references carry installation and cleanup.
type DeploymentReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Scope     kube.Scope
}

// Reconcile establishes ordinary ACI ownership and completes installation cleanup
// before releasing the deployment. CAP-Agent owns the creation-time installation spec.
func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	deployment := &hivev1.ClusterDeployment{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, deployment); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ref := deployment.Spec.ClusterInstallRef
	if ref == nil || ref.Group != installv1.GroupVersion.Group || ref.Kind != "AgentClusterInstall" {
		return ctrl.Result{}, nil
	}

	installation := &installv1.AgentClusterInstall{}

	err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: deployment.Namespace, Name: deployment.Spec.ClusterInstallRef.Name},
		installation,
	)
	if apierrors.IsNotFound(err) {
		if deployment.DeletionTimestamp != nil {
			return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, deployment, deploymentFinalizer)
		}

		return ctrl.Result{}, nil
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	if err := validateInstallation(deployment, installation); err != nil {
		return ctrl.Result{}, err
	}

	if installation.DeletionTimestamp != nil {
		if !controllerutil.ContainsFinalizer(installation, installationFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.unbindAgents(ctx, deployment); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, installation, installationFinalizer)
	}

	// Protect installation cleanup before holding or deleting the deployment.
	if err := kube.AddFinalizer(ctx, r.Client, installation, installationFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if deployment.DeletionTimestamp != nil {
		return ctrl.Result{RequeueAfter: time.Second}, kube.Delete(ctx, r.Client, installation)
	}

	if err := kube.AddFinalizer(ctx, r.Client, deployment, deploymentFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.ensureInstallationOwner(ctx, deployment, installation)
}

// validateInstallation accepts this deployment's installation or its pending ownerless record.
func validateInstallation(deployment *hivev1.ClusterDeployment, installation *installv1.AgentClusterInstall) error {
	if installation.Spec.ClusterDeploymentRef.Name != deployment.Name {
		return fmt.Errorf("installation belongs to another deployment")
	}

	if len(installation.OwnerReferences) > 0 &&
		!kube.MatchesOwner(installation, deployment, hivev1.SchemeGroupVersion.WithKind("ClusterDeployment")) {
		return fmt.Errorf("installation belongs to another deployment incarnation")
	}

	return nil
}

// ensureInstallationOwner establishes ordinary ownership after both cleanup finalizers are persisted.
func (r *DeploymentReconciler) ensureInstallationOwner(
	ctx context.Context,
	deployment *hivev1.ClusterDeployment,
	installation *installv1.AgentClusterInstall,
) error {
	if len(installation.OwnerReferences) == 0 {
		before := installation.DeepCopy()
		if err := controllerutil.SetOwnerReference(deployment, installation, r.Client.Scheme()); err != nil {
			return err
		}

		if err := r.Client.Patch(
			ctx,
			installation,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return err
		}
	}

	return nil
}

// deleteAgentOnUnbind selects deployment-specific discovery Agents for deletion.
// Late-binding Agents are deleted only when the deployment preserves its hosts.
func (r *DeploymentReconciler) deleteAgentOnUnbind(
	ctx context.Context, agent *assistedv1.Agent, deployment *hivev1.ClusterDeployment,
) bool {
	name := agent.Labels[assistedv1.InfraEnvNameLabel]
	if name == "" {
		return false
	}

	infra := &assistedv1.InfraEnv{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: agent.Namespace, Name: name}, infra); err != nil {
		ctrl.LoggerFrom(ctx).
			Error(err, "read Agent InfraEnv during deployment cleanup", "namespace", agent.Namespace, "name", name)

		return false
	}

	if ref := infra.Spec.ClusterRef; ref != nil {
		return ref.Namespace == deployment.Namespace && ref.Name == deployment.Name
	}

	return deployment.Spec.PreserveOnDelete
}

// unbindAgents deletes Agents selected by native InfraEnv policy or clears their
// deployment reference. CAP-Agent retains its claim and bootstrap cleanup responsibility.
// Hosts outside served namespaces stay untouched.
func (r *DeploymentReconciler) unbindAgents(ctx context.Context, deployment *hivev1.ClusterDeployment) error {
	for _, namespace := range r.Scope.AgentNamespaces {
		var agents assistedv1.AgentList

		if err := r.APIReader.List(ctx, &agents, client.InNamespace(namespace)); err != nil {
			return err
		}

		for i := range agents.Items {
			if err := r.unbindAgent(ctx, &agents.Items[i], deployment); err != nil {
				return err
			}
		}
	}

	return nil
}

// unbindAgent releases one matching deployment association according to its InfraEnv policy.
func (r *DeploymentReconciler) unbindAgent(
	ctx context.Context, agent *assistedv1.Agent, deployment *hivev1.ClusterDeployment,
) error {
	ref := agent.Spec.ClusterDeploymentName
	if ref == nil || ref.Namespace != deployment.Namespace || ref.Name != deployment.Name {
		return nil
	}

	if r.deleteAgentOnUnbind(ctx, agent, deployment) {
		return kube.Delete(ctx, r.Client, agent)
	}

	before := agent.DeepCopy()
	agent.Spec.ClusterDeploymentName = nil

	return r.Client.Patch(ctx, agent, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
