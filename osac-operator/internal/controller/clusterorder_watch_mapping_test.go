// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

var _ = It("maps subnets to ClusterOrders by API ID and legacy name", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

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
	Expect(names).To(Equal(want))
})

var _ = It("maps owned automatic ingress ExternalIPs to their ClusterOrder", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	clusterID := "cluster-api-id"
	order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Namespace: "osac", Name: "caas-order",
		Labels: map[string]string{osacClusterOrderIDLabel: clusterID},
		Annotations: map[string]string{
			networktask.TenantAnnotation: "tenant-a",
		},
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(order).Build()
	reconciler := &ClusterOrderReconciler{
		Client:                kube,
		ClusterOrderNamespace: "osac",
		NetworkingNamespace:   "networking",
	}

	for _, tc := range []struct {
		name     string
		endpoint string
		owner    string
		tenant   string
		want     bool
	}{
		{name: "owned ingress ExternalIP", endpoint: autoCreatedEndpointIngress, owner: clusterID, tenant: "tenant-a", want: true},
		{name: "API ExternalIP", endpoint: autoCreatedEndpointAPI, owner: clusterID, tenant: "tenant-a"},
		{name: "foreign ExternalIP", endpoint: autoCreatedEndpointIngress, owner: "another-cluster", tenant: "tenant-a"},
		{name: "foreign tenant ExternalIP", endpoint: autoCreatedEndpointIngress, owner: clusterID, tenant: "tenant-b"},
	} {
		By(tc.name)
		ip := &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{
			Namespace: "networking",
			Name:      "automatic-eip",
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedKindLabel:     autoCreatedKindCluster,
				autoCreatedEndpointLabel: tc.endpoint,
			},
			Annotations: map[string]string{
				ownerReferenceAnnotation:     tc.owner,
				networktask.TenantAnnotation: tc.tenant,
			},
		}}
		requests := reconciler.mapAutomaticExternalIPToCluster(context.Background(), ip)
		if tc.want {
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal(order.Name))
		} else {
			Expect(requests).To(BeEmpty())
		}
	}
})
