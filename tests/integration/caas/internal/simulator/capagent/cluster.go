// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package capagent simulates CAP-Agent host claims and installation inputs.
package capagent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hiveagent "github.com/openshift/hive/apis/hive/v1/agent"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const agentClusterFinalizer = "agentclustercapi-provider.agent-install.openshift.io/deprovision"

// AgentClusterReconciler supplies installation records for additional workers.
type AgentClusterReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Scope     kube.Scope
}

// Reconcile follows the original Cluster and control plane. It publishes readiness
// only after both deployment and installation records have been persisted.
func (r *AgentClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	infra := &agentv1.AgentCluster{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, infra); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if infra.DeletionTimestamp != nil {
		return ctrl.Result{}, r.finalizeCluster(ctx, infra)
	}

	owner := kube.Owner(infra, "Cluster")
	if owner == nil {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	cluster, cp, err := controlPlane(ctx, r.APIReader, infra.Namespace, owner.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil || !kube.MatchesController(infra, cluster, capiv1.GroupVersion.WithKind("Cluster")) || cp == nil ||
		cp.Spec.Platform.Agent == nil || !r.Scope.Contains(cp.Spec.Platform.Agent.AgentNamespace) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if err := kube.AddFinalizer(ctx, r.Client, infra, agentClusterFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if infra.Annotations[capiv1.PausedAnnotation] == "true" {
		return ctrl.Result{}, r.orphanDeployment(ctx, infra)
	}

	if cp.Status.KubeConfig == nil || cp.Status.KubeConfig.Name == "" {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	deployment, err := r.ensureDeployment(ctx, infra, cluster, cp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureInstallation(ctx, deployment, infra); err != nil {
		return ctrl.Result{}, err
	}

	next := infra.DeepCopy()

	next.Status.ClusterDeploymentRef = agentv1.ClusterDeploymentReference{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
	}
	if deployment.DeletionTimestamp == nil && infra.Spec.ControlPlaneEndpoint.IsValid() {
		next.Status.Ready = true
	}

	if equality.Semantic.DeepEqual(infra.Status, next.Status) {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(infra, client.MergeFromWithOptimisticLock{}))
}

// ensureDeployment creates the existing-cluster record with its native ordinary
// AgentCluster owner. Existing deployment inputs belong to their original writers.
func (r *AgentClusterReconciler) ensureDeployment(
	ctx context.Context, infra *agentv1.AgentCluster, cluster *capiv1.Cluster, cp *hypershiftv1.HostedControlPlane,
) (*hivev1.ClusterDeployment, error) {
	current := &hivev1.ClusterDeployment{}

	if ref := infra.Status.ClusterDeploymentRef; ref.Name != "" {
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, current); err != nil {
			return nil, err
		}

		return current, r.ownDeployment(ctx, infra, current)
	}

	var deployments hivev1.ClusterDeploymentList

	if err := r.APIReader.List(ctx, &deployments, client.InNamespace(infra.Namespace),
		client.MatchingLabels{"agentClusterRef": infra.Name}); err != nil {
		return nil, err
	}

	if len(deployments.Items) > 0 {
		current = &deployments.Items[0]
		return current, r.ownDeployment(ctx, infra, current)
	}

	current = &hivev1.ClusterDeployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: infra.Namespace, Name: infra.Name,
		Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName, "agentClusterRef": infra.Name},
	}}
	if err := controllerutil.SetOwnerReference(infra, current, r.Client.Scheme()); err != nil {
		return nil, err
	}

	current.Spec.Installed = true
	current.Spec.ClusterName = cp.Name
	current.Spec.BaseDomain = cp.Spec.DNS.BaseDomain
	current.Spec.Platform.AgentBareMetal = &hiveagent.BareMetalPlatform{}
	current.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: cp.Spec.PullSecret.Name}
	current.Spec.ClusterInstallRef = &hivev1.ClusterInstallLocalReference{
		Group: installv1.GroupVersion.Group, Version: installv1.GroupVersion.Version,
		Kind: "AgentClusterInstall", Name: infra.Name,
	}
	current.Spec.ClusterMetadata = &hivev1.ClusterMetadata{
		ClusterID: string(cluster.UID), InfraID: string(cluster.UID),
		AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: cp.Status.KubeConfig.Name},
	}

	return current, r.Client.Create(ctx, current)
}

// ownDeployment restores ordinary ownership after a deliberate pause orphaned the record.
// Existing owners must match the original AgentCluster before any owner setter runs.
func (r *AgentClusterReconciler) ownDeployment(
	ctx context.Context,
	infra *agentv1.AgentCluster,
	deployment *hivev1.ClusterDeployment,
) error {
	owned := kube.MatchesOwner(deployment, infra, agentv1.GroupVersion.WithKind("AgentCluster"))
	if owned && deployment.Labels["agentClusterRef"] == infra.Name {
		return nil
	}

	if !owned && len(deployment.OwnerReferences) > 0 {
		return fmt.Errorf("cluster deployment belongs to another AgentCluster")
	}

	before := deployment.DeepCopy()
	if err := controllerutil.SetOwnerReference(infra, deployment, r.Client.Scheme()); err != nil {
		return err
	}

	metav1.SetMetaDataLabel(&deployment.ObjectMeta, "agentClusterRef", infra.Name)

	return r.Client.Patch(ctx, deployment, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// ensureInstallation creates immutable installation inputs. Assisted establishes
// the native deployment owner; existing records retain their spec and status.
func (r *AgentClusterReconciler) ensureInstallation(
	ctx context.Context, deployment *hivev1.ClusterDeployment, infra *agentv1.AgentCluster,
) error {
	current := &installv1.AgentClusterInstall{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(deployment), current)
	if err == nil {
		if current.Spec.ClusterDeploymentRef.Name != deployment.Name {
			return fmt.Errorf("installation references another ClusterDeployment")
		}

		if len(current.OwnerReferences) > 0 &&
			!kube.MatchesOwner(current, deployment, hivev1.SchemeGroupVersion.WithKind("ClusterDeployment")) {
			return fmt.Errorf("installation belongs to another ClusterDeployment")
		}

		return nil
	}

	if !apierrors.IsNotFound(err) {
		return err
	}

	current = &installv1.AgentClusterInstall{ObjectMeta: metav1.ObjectMeta{
		Namespace: deployment.Namespace, Name: deployment.Name,
		Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
	}}
	current.Spec.ClusterDeploymentRef = corev1.LocalObjectReference{Name: deployment.Name}

	current.Spec.ProvisionRequirements.ControlPlaneAgents = 3
	current.Spec.Networking.UserManagedNetworking = new(true)
	if endpoint := infra.Spec.IgnitionEndpoint; endpoint != nil {
		separator := strings.LastIndex(endpoint.Url, "/")
		if separator < 0 {
			return fmt.Errorf("invalid AgentCluster ignition endpoint")
		}

		current.Spec.IgnitionEndpoint = &installv1.IgnitionEndpoint{Url: endpoint.Url[:separator]}
		if ca := endpoint.CaCertificateReference; ca != nil {
			current.Spec.IgnitionEndpoint.CaCertificateReference = &installv1.CaCertificateReference{
				Namespace: ca.Namespace, Name: ca.Name,
			}
		}
	}

	return r.Client.Create(ctx, current)
}

// finalizeCluster preserves paused deployments and removes only CAP-Agent's finalizer.
func (r *AgentClusterReconciler) finalizeCluster(ctx context.Context, infra *agentv1.AgentCluster) error {
	if infra.Annotations[capiv1.PausedAnnotation] == "true" {
		if err := r.orphanDeployment(ctx, infra); err != nil {
			return err
		}
	}

	return kube.RemoveFinalizer(ctx, r.Client, infra, agentClusterFinalizer)
}

// orphanDeployment clears only this original AgentCluster's ordinary owner reference.
func (r *AgentClusterReconciler) orphanDeployment(ctx context.Context, infra *agentv1.AgentCluster) error {
	ref := infra.Status.ClusterDeploymentRef
	if ref.Name == "" {
		return nil
	}

	deployment := &hivev1.ClusterDeployment{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, deployment)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	if !kube.MatchesOwner(deployment, infra, agentv1.GroupVersion.WithKind("AgentCluster")) {
		return nil
	}

	before := deployment.DeepCopy()
	deployment.OwnerReferences = slices.DeleteFunc(deployment.OwnerReferences, func(owner metav1.OwnerReference) bool {
		return owner.APIVersion == agentv1.GroupVersion.String() && owner.Kind == "AgentCluster" &&
			owner.Name == infra.Name && owner.UID == infra.UID
	})

	return r.Client.Patch(ctx, deployment, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// controlPlane resolves a same-namespace reference and checks the original Cluster
// controller. Missing inputs return nil; invalid references return an error.
func controlPlane(ctx context.Context, r client.Reader, namespace, name string) (
	*capiv1.Cluster, *hypershiftv1.HostedControlPlane, error,
) {
	cluster := &capiv1.Cluster{}

	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cluster)
	if apierrors.IsNotFound(err) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	ref := cluster.Spec.ControlPlaneRef
	if ref == nil || ref.Name == "" {
		return cluster, nil, nil
	}

	if ref.APIVersion != hypershiftv1.GroupVersion.String() || ref.Kind != "HostedControlPlane" ||
		(ref.Namespace != "" && ref.Namespace != namespace) {
		return cluster, nil, fmt.Errorf("cluster requires a same-namespace HostedControlPlane reference")
	}

	cp := &hypershiftv1.HostedControlPlane{}

	err = r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, cp)
	if apierrors.IsNotFound(err) {
		return cluster, nil, nil
	}

	if err != nil {
		return cluster, nil, err
	}

	if !kube.MatchesController(cp, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return cluster, nil, fmt.Errorf("hosted control plane belongs to another Cluster")
	}

	return cluster, cp, nil
}
