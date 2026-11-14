// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package capi simulates Cluster API worker creation, provider readiness and deletion.
// Guest Nodes and drain are outside this model.
package capi

import (
	"context"
	"fmt"
	"time"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const clusterFinalizer = "cluster.cluster.x-k8s.io"

// ClusterReconciler observes provider readiness and waits for worker cleanup.
type ClusterReconciler struct {
	Client    client.Client
	APIReader client.Reader
}

// Reconcile copies readiness from the referenced providers. During deletion it
// waits for workers before deleting the providers and removing its finalizer.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cluster := &capiv1.Cluster{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp == nil {
		if err := kube.AddFinalizer(ctx, r.Client, cluster, clusterFinalizer); err != nil {
			return ctrl.Result{}, err
		}
	}

	if annotations.IsPaused(cluster, cluster) {
		return ctrl.Result{}, nil
	}

	if cluster.DeletionTimestamp != nil {
		return r.delete(ctx, cluster)
	}

	infra, err := r.infrastructureProvider(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	cp, err := r.controlPlaneProvider(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	next := clusterStatus(cluster, infra, cp)
	if !equality.Semantic.DeepEqual(cluster.Spec, next.Spec) {
		// The spec response contains stored status; retain these new observations separately.
		spec := next.DeepCopy()
		if err := r.Client.Patch(
			ctx,
			spec,
			client.MergeFromWithOptions(cluster, client.MergeFromWithOptimisticLock{}),
		); err != nil {
			return ctrl.Result{}, err
		}

		cluster.Spec, cluster.ResourceVersion = spec.Spec, spec.ResourceVersion
		next.ResourceVersion = spec.ResourceVersion
	}

	if equality.Semantic.DeepEqual(cluster.Status, next.Status) {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.Client.Status().Patch(ctx, next,
		client.MergeFromWithOptions(cluster, client.MergeFromWithOptimisticLock{}))
}

// clusterStatus separates sticky provisioning observations from current conditions.
func clusterStatus(
	cluster *capiv1.Cluster,
	infra *agentv1.AgentCluster,
	cp *hypershiftv1.HostedControlPlane,
) *capiv1.Cluster {
	next := cluster.DeepCopy()
	if infra != nil && infra.Status.Ready {
		next.Status.InfrastructureReady = true
		conditions.MarkTrue(next, capiv1.InfrastructureReadyCondition)

		if !next.Spec.ControlPlaneEndpoint.IsValid() {
			next.Spec.ControlPlaneEndpoint = infra.Spec.ControlPlaneEndpoint
		}
	}

	if infra != nil && !infra.Status.Ready {
		conditions.MarkFalse(
			next,
			capiv1.InfrastructureReadyCondition,
			"WaitingForInfrastructure",
			capiv1.ConditionSeverityInfo,
			"Waiting for infrastructure",
		)
	}

	if cluster.Spec.InfrastructureRef == nil {
		next.Status.InfrastructureReady = true
		conditions.MarkTrue(next, capiv1.InfrastructureReadyCondition)
	}

	if cp != nil {
		next.Status.ControlPlaneReady = cluster.Status.ControlPlaneReady || cp.Status.Ready
		if cp.Status.Ready {
			conditions.MarkTrue(next, capiv1.ControlPlaneReadyCondition)
		} else {
			conditions.MarkFalse(
				next,
				capiv1.ControlPlaneReadyCondition,
				"WaitingForControlPlane",
				capiv1.ConditionSeverityInfo,
				"Waiting for the control plane",
			)
		}

		if cp.Status.Initialized {
			conditions.MarkTrue(next, capiv1.ControlPlaneInitializedCondition)
		}
	}

	conditions.SetSummary(
		next,
		conditions.WithConditions(capiv1.InfrastructureReadyCondition, capiv1.ControlPlaneReadyCondition),
	)

	return next
}

// infrastructureProvider resolves and adopts this Cluster's supported infrastructure reference.
func (r *ClusterReconciler) infrastructureProvider(
	ctx context.Context,
	cluster *capiv1.Cluster,
) (*agentv1.AgentCluster, error) {
	ref := cluster.Spec.InfrastructureRef
	if ref == nil {
		return nil, nil
	}

	if ref.APIVersion != agentv1.GroupVersion.String() || ref.Kind != "AgentCluster" ||
		(ref.Namespace != "" && ref.Namespace != cluster.Namespace) {
		return nil, fmt.Errorf("unsupported Cluster infrastructureRef")
	}

	infra := &agentv1.AgentCluster{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: ref.Name}, infra)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return infra, r.adoptProvider(ctx, cluster, infra)
}

// controlPlaneProvider resolves and adopts this Cluster's supported control-plane reference.
func (r *ClusterReconciler) controlPlaneProvider(
	ctx context.Context,
	cluster *capiv1.Cluster,
) (*hypershiftv1.HostedControlPlane, error) {
	ref := cluster.Spec.ControlPlaneRef
	if ref == nil {
		return nil, nil
	}

	if ref.APIVersion != hypershiftv1.GroupVersion.String() || ref.Kind != "HostedControlPlane" ||
		(ref.Namespace != "" && ref.Namespace != cluster.Namespace) {
		return nil, fmt.Errorf("unsupported Cluster controlPlaneRef")
	}

	cp := &hypershiftv1.HostedControlPlane{}

	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: ref.Name}, cp)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return cp, r.adoptProvider(ctx, cluster, cp)
}

// adoptProvider establishes CAPI ownership of the referenced provider.
// An existing controller must retain its complete original identity.
func (r *ClusterReconciler) adoptProvider(ctx context.Context, cluster *capiv1.Cluster, provider client.Object) error {
	if metav1.GetControllerOf(provider) != nil {
		if !kube.MatchesController(provider, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
			return fmt.Errorf("provider belongs to another Cluster incarnation")
		}

		if provider.GetLabels()[kube.ClusterLabel] == cluster.Name {
			return nil
		}
	}

	for _, owner := range provider.GetOwnerReferences() {
		if owner.Kind == "Cluster" && (owner.APIVersion != capiv1.GroupVersion.String() ||
			owner.Name != cluster.Name || owner.UID != cluster.UID) {
			return fmt.Errorf("provider belongs to another Cluster incarnation")
		}
	}

	before := provider.DeepCopyObject().(client.Object)
	if err := controllerutil.SetControllerReference(cluster, provider, r.Client.Scheme()); err != nil {
		return err
	}

	labels := provider.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[kube.ClusterLabel] = cluster.Name
	provider.SetLabels(labels)

	return r.Client.Patch(ctx, provider, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// delete waits for worker descendants before deleting the providers.
func (r *ClusterReconciler) delete(ctx context.Context, cluster *capiv1.Cluster) (ctrl.Result, error) {
	pending, err := r.deleteWorkers(ctx, cluster)
	if err != nil || pending {
		return ctrl.Result{RequeueAfter: time.Second}, err
	}

	cp, err := r.controlPlaneProvider(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cp != nil {
		return ctrl.Result{RequeueAfter: time.Second}, kube.Delete(ctx, r.Client, cp)
	}

	infra, err := r.infrastructureProvider(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if infra != nil {
		return ctrl.Result{RequeueAfter: time.Second}, kube.Delete(ctx, r.Client, infra)
	}

	return ctrl.Result{}, kube.RemoveFinalizer(ctx, r.Client, cluster, clusterFinalizer)
}

// deleteWorkers removes owned MachineSets before Machines and waits for all worker descendants.
// Foreign workers remain unchanged and still hold provider deletion.
func (r *ClusterReconciler) deleteWorkers(ctx context.Context, cluster *capiv1.Cluster) (bool, error) {
	var sets capiv1.MachineSetList

	if err := r.APIReader.List(ctx, &sets, client.InNamespace(cluster.Namespace),
		client.MatchingLabels{kube.ClusterLabel: cluster.Name}); err != nil {
		return false, err
	}

	pending := false

	for i := range sets.Items {
		if kube.MatchesOwner(&sets.Items[i], cluster, capiv1.GroupVersion.WithKind("Cluster")) {
			if err := kube.Delete(ctx, r.Client, &sets.Items[i]); err != nil {
				return false, err
			}

			pending = true
		}
	}

	if pending {
		return true, nil
	}

	var machines capiv1.MachineList

	if err := r.APIReader.List(ctx, &machines, client.InNamespace(cluster.Namespace),
		client.MatchingLabels{kube.ClusterLabel: cluster.Name}); err != nil {
		return false, err
	}

	for i := range machines.Items {
		if _, controlPlane := machines.Items[i].Labels[capiv1.MachineControlPlaneLabel]; controlPlane &&
			cluster.Spec.ControlPlaneRef != nil {
			continue
		}

		pending = true

		if kube.MatchesOwner(&machines.Items[i], cluster, capiv1.GroupVersion.WithKind("Cluster")) {
			if err := kube.Delete(ctx, r.Client, &machines.Items[i]); err != nil {
				return false, err
			}
		}
	}

	return len(sets.Items) > 0 || pending, nil
}
