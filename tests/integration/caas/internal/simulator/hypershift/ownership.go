// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// validateOutput checks source annotations and complete controller identity
// before changing an existing output. Writers retain the
// observed resource version in their optimistic patch.
func validateOutput(current, desired client.Object) error {
	for _, key := range []string{kube.HostedClusterAnnotation, kube.NodePoolAnnotation} {
		if value, supplied := desired.GetAnnotations()[key]; supplied && current.GetAnnotations()[key] != value {
			return fmt.Errorf("%T %s/%s belongs to another source", current, current.GetNamespace(), current.GetName())
		}
	}

	owner, expected := metav1.GetControllerOf(current), metav1.GetControllerOf(desired)
	if expected == nil && owner == nil {
		return nil
	}

	if owner == nil || expected == nil || owner.UID != expected.UID ||
		owner.Name != expected.Name || owner.Kind != expected.Kind || owner.APIVersion != expected.APIVersion {
		return fmt.Errorf("%T %s/%s belongs to another controller", current, current.GetNamespace(), current.GetName())
	}

	return nil
}

// validateProvider accepts an original CAPI provider or an unadopted HyperShift output.
func validateProvider(provider client.Object, cluster *capiv1.Cluster) error {
	if cluster.Annotations[kube.HostedClusterAnnotation] == "" ||
		provider.GetAnnotations()[kube.HostedClusterAnnotation] != cluster.Annotations[kube.HostedClusterAnnotation] {
		return fmt.Errorf("provider belongs to another HostedCluster")
	}

	for _, owner := range provider.GetOwnerReferences() {
		if owner.Kind == "Cluster" && (owner.APIVersion != capiv1.GroupVersion.String() ||
			owner.Name != cluster.Name || owner.UID != cluster.UID) {
			return fmt.Errorf("provider belongs to another Cluster incarnation")
		}
	}

	if metav1.GetControllerOf(provider) != nil &&
		!kube.MatchesController(provider, cluster, capiv1.GroupVersion.WithKind("Cluster")) {
		return fmt.Errorf("provider belongs to another Cluster incarnation")
	}

	return nil
}

// hostedClusterOutput recognizes outputs linked across namespaces to this cluster.
func hostedClusterOutput(output, cluster metav1.Object) bool {
	return cluster.GetUID() != "" &&
		output.GetAnnotations()[kube.HostedClusterAnnotation] == cluster.GetNamespace()+"/"+cluster.GetName()
}

// nodePoolOutput recognizes the source pool link. Callers also check the current
// CAPI Cluster controller before deleting worker inputs.
func nodePoolOutput(output, pool metav1.Object) bool {
	return pool.GetUID() != "" &&
		output.GetAnnotations()[kube.NodePoolAnnotation] == pool.GetNamespace()+"/"+pool.GetName()
}
