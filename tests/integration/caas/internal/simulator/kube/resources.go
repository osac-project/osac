// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package kube contains shared Kubernetes operations and ownership checks for
// the simulator's provider controllers and physical-host model.
package kube

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ManagedByLabel          = "app.kubernetes.io/managed-by"
	ManagerName             = "osac-provider-simulator"
	InfraEnvLabel           = "infraenvs.agent-install.openshift.io"
	ClusterLabel            = "cluster.x-k8s.io/cluster-name"
	HostedClusterAnnotation = "hypershift.openshift.io/cluster"
	NodePoolAnnotation      = "hypershift.openshift.io/nodePool"
	MachineHook             = "pre-terminate.delete.hook.machine.cluster.x-k8s.io/agentmachine"
)

// Owner returns the object's controller reference if its kind matches.
// Other owner references do not identify the controller responsible for the object.
func Owner(o metav1.Object, kind string) *metav1.OwnerReference {
	ref := metav1.GetControllerOf(o)
	if ref != nil && ref.Kind == kind {
		return ref
	}

	return nil
}

// OwnerReference returns a matching native owner, including a non-controller owner.
func OwnerReference(o metav1.Object, kind string) *metav1.OwnerReference {
	for _, ref := range o.GetOwnerReferences() {
		if ref.Kind == kind {
			return &ref
		}
	}

	return nil
}

// MatchesOwner checks the complete native owner identity in the parent namespace.
// Ordinary owner references support garbage collection without assigning a controller.
func MatchesOwner(child, parent metav1.Object, kind schema.GroupVersionKind) bool {
	if parent.GetUID() == "" || child.GetNamespace() != parent.GetNamespace() {
		return false
	}

	for _, owner := range child.GetOwnerReferences() {
		if owner.APIVersion == kind.GroupVersion().String() && owner.Kind == kind.Kind &&
			owner.Name == parent.GetName() && owner.UID == parent.GetUID() {
			return true
		}
	}

	return false
}

// MatchesController checks the owner's API, name and UID in the parent's namespace.
func MatchesController(child, parent metav1.Object, kind schema.GroupVersionKind) bool {
	owner := metav1.GetControllerOf(child)
	return owner != nil && parent.GetUID() != "" && child.GetNamespace() == parent.GetNamespace() &&
		owner.APIVersion == kind.GroupVersion().String() && owner.Kind == kind.Kind &&
		owner.Name == parent.GetName() && owner.UID == parent.GetUID()
}
