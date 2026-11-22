// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"slices"
	"strings"

	routev1 "github.com/openshift/api/route/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// SetupWithManager registers the HostedCluster controller and watches CAPI Clusters,
// HostedControlPlanes, source Secrets and NodePools so published status, access
// data and deletion trigger another pass.
func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("simulated-hypershift-hostedcluster").
		For(&hypershiftv1.HostedCluster{}).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.hostedClusterForChild)).
		Watches(&hypershiftv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(r.hostedClusterForChild)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.hostedClustersForSecret)).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(r.hostedClusterForChild)).
		Watches(&routev1.Route{}, handler.EnqueueRequestsFromMapFunc(r.hostedClusterForChild)).
		Watches(&hypershiftv1.NodePool{}, handler.EnqueueRequestsFromMapFunc(hostedClusterForPool)).
		WithOptions(kube.ControllerOptions()).
		Complete(r)
}

// hostedClusterForChild follows native annotations or owner links to the HostedCluster.
func (r *HostedClusterReconciler) hostedClusterForChild(ctx context.Context, o client.Object) []reconcile.Request {
	if requests := annotatedParent(o, kube.HostedClusterAnnotation); len(requests) > 0 {
		return requests
	}

	switch o.(type) {
	case *corev1.Secret, *corev1.Service, *routev1.Route:
		owner := kube.Owner(o, "HostedControlPlane")
		if owner == nil {
			return nil
		}

		cp := &hypershiftv1.HostedControlPlane{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: o.GetNamespace(), Name: owner.Name}, cp); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "read child control plane")
			return nil
		}

		if !kube.MatchesController(o, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
			return nil
		}

		o = cp
	}

	return annotatedParent(o, kube.HostedClusterAnnotation)
}

// hostedClustersForSecret follows HCP-owned outputs and HostedCluster credential references.
func (r *HostedClusterReconciler) hostedClustersForSecret(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	requests := r.hostedClusterForChild(ctx, object)

	var clusters hypershiftv1.HostedClusterList

	if err := r.Client.List(ctx, &clusters, client.InNamespace(object.GetNamespace())); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list HostedClusters for credential Secret")
		return requests
	}

	for i := range clusters.Items {
		spec := &clusters.Items[i].Spec
		names := []string{spec.PullSecret.Name, spec.SSHKey.Name}
		if spec.AuditWebhook != nil {
			names = append(names, spec.AuditWebhook.Name)
		}

		if spec.ServiceAccountSigningKey != nil {
			names = append(names, spec.ServiceAccountSigningKey.Name)
		}

		if encryption := spec.SecretEncryption; encryption != nil && encryption.AESCBC != nil {
			names = append(names, encryption.AESCBC.ActiveKey.Name)
			if encryption.AESCBC.BackupKey != nil {
				names = append(names, encryption.AESCBC.BackupKey.Name)
			}
		}

		if spec.Etcd.Unmanaged != nil {
			names = append(names, spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
		}

		if slices.Contains(names, object.GetName()) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&clusters.Items[i])})
		}
	}

	return requests
}

// hostedClusterForPool uses the event's clusterName, including after pool deletion.
func hostedClusterForPool(_ context.Context, object client.Object) []reconcile.Request {
	pool := object.(*hypershiftv1.NodePool)
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: pool.Namespace, Name: pool.Spec.ClusterName}}}
}

// SetupWithManager registers the NodePool controller and watches worker resources,
// HostedClusters, HostedControlPlanes and CAPI Clusters for progress or parent changes.
func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-hypershift-nodepool").For(&hypershiftv1.NodePool{}).
		Watches(&capiv1.MachineSet{}, handler.EnqueueRequestsFromMapFunc(nodePoolForChild)).
		Watches(&capiv1.Machine{}, handler.EnqueueRequestsFromMapFunc(nodePoolForChild)).
		Watches(&agentv1.AgentMachineTemplate{}, handler.EnqueueRequestsFromMapFunc(nodePoolForChild)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.poolsForSecret)).
		Watches(&hypershiftv1.HostedCluster{}, handler.EnqueueRequestsFromMapFunc(r.poolsForCluster)).
		Watches(&hypershiftv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(r.poolsForCluster)).
		Watches(&capiv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(r.poolsForCluster)).
		WithOptions(kube.ControllerOptions()).Complete(r)
}

// poolsForSecret follows pool-owned inputs or the original HCP's shared ignition CA.
func (r *NodePoolReconciler) poolsForSecret(ctx context.Context, object client.Object) []reconcile.Request {
	if requests := nodePoolForChild(ctx, object); len(requests) > 0 {
		return requests
	}

	owner := kube.Owner(object, "HostedControlPlane")
	if object.GetName() != "ignition-server-ca-cert" || owner == nil {
		return nil
	}

	cp := &hypershiftv1.HostedControlPlane{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: object.GetNamespace(), Name: owner.Name}, cp); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "read ignition CA control plane")
		return nil
	}

	if !kube.MatchesController(object, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil
	}

	return r.poolsForCluster(ctx, cp)
}

// nodePoolForChild queues the NodePool named by the worker resource's
// hypershift.openshift.io/nodePool annotation. It carries the pool's namespace
// and name across namespaces.
func nodePoolForChild(ctx context.Context, o client.Object) []reconcile.Request {
	requests := annotatedParent(o, kube.NodePoolAnnotation)
	ctrl.LoggerFrom(ctx).V(4).Info("mapped worker resource to NodePool",
		"sourceNamespace", o.GetNamespace(),
		"source", o.GetName(),
		"sourceUID", o.GetUID(),
		"targets", len(requests),
	)

	return requests
}

// annotatedParent splits the annotation at key into a namespace and name and queues
// that parent. It returns no request when the annotation has no slash.
func annotatedParent(o client.Object, key string) []reconcile.Request {
	ns, name, ok := strings.Cut(o.GetAnnotations()[key], "/")
	if !ok {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: ns, Name: name}}}
}

// poolsForCluster follows a HostedCluster or an annotated HCP/CAPI event to its pools.
// A list failure is logged and queues no requests; the HostedCluster may be absent.
func (r *NodePoolReconciler) poolsForCluster(ctx context.Context, o client.Object) []reconcile.Request {
	parent := client.ObjectKeyFromObject(o)

	switch o.(type) {
	case *hypershiftv1.HostedControlPlane, *capiv1.Cluster:
		requests := annotatedParent(o, kube.HostedClusterAnnotation)
		if len(requests) == 0 {
			return nil
		}

		parent = requests[0].NamespacedName
	}

	if parent.Namespace == "" || parent.Name == "" {
		return nil
	}

	var pools hypershiftv1.NodePoolList

	if err := r.Client.List(ctx, &pools, client.InNamespace(parent.Namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "list NodePools")
		return nil
	}

	var result []reconcile.Request

	for i := range pools.Items {
		if pools.Items[i].Spec.ClusterName == parent.Name {
			result = append(result, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&pools.Items[i])})
		}
	}

	ctrl.LoggerFrom(ctx).V(4).Info("mapped cluster resource to NodePools",
		"namespace", parent.Namespace,
		"hostedCluster", parent.Name,
		"sourceUID", o.GetUID(),
		"targets", len(result),
	)

	return result
}
