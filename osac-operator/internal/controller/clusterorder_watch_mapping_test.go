// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func TestMapSubnetToClusterSupportsAPIIDsAndLegacyNames(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding OSAC API types to scheme: %v", err)
	}

	subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{
		Namespace: "networking",
		Name:      "subnet-cr-name",
		Labels:    map[string]string{osacSubnetIDLabel: "subnet-api-id"},
	}}
	orders := []*v1alpha1.ClusterOrder{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "osac", Name: "api-id-order"},
			Spec: v1alpha1.ClusterOrderSpec{NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: "subnet-api-id"}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "osac", Name: "legacy-name-order"},
			Spec: v1alpha1.ClusterOrderSpec{NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: "subnet-cr-name"}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "osac", Name: "other-order"},
			Spec: v1alpha1.ClusterOrderSpec{NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: "other-subnet"}}},
	}
	objects := make([]runtime.Object, 1, 1+len(orders))
	objects[0] = subnet
	for _, order := range orders {
		objects = append(objects, order)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
	reconciler := &ClusterOrderReconciler{
		Client:                kube,
		ClusterOrderNamespace: "osac",
		NetworkingNamespace:   "networking",
	}

	requests := reconciler.mapSubnetToCluster(context.Background(), subnet)
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	sort.Strings(names)
	want := []string{"api-id-order", "legacy-name-order"}
	if len(names) != len(want) {
		t.Fatalf("mapped ClusterOrders = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("mapped ClusterOrders = %v, want %v", names, want)
		}
	}
}
