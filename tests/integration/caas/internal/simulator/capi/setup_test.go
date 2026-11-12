// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("Native CAPI event associations", func() {
	It("maps cached providers and workers while authoritative lists fail", func(ctx SpecContext) {
		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster"},
			Spec: capiv1.ClusterSpec{InfrastructureRef: &corev1.ObjectReference{
				APIVersion: agentv1.GroupVersion.String(), Kind: "AgentCluster", Name: "infrastructure",
			}},
		}
		infra := &agentv1.AgentCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "infrastructure"}}
		set := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "workers"},
			Spec:       capiv1.MachineSetSpec{ClusterName: cluster.Name},
		}
		machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
			Namespace: "cp", Name: "worker", Labels: map[string]string{kube.ClusterLabel: cluster.Name},
		}}
		other := set.DeepCopy()
		other.Name, other.Spec.ClusterName = "other", "other-cluster"
		c := fake.NewClientBuilder().WithScheme(capiScheme()).WithObjects(cluster, infra, set, machine, other).Build()
		reader := interceptor.NewClient(c, interceptor.Funcs{List: func(
			context.Context, client.WithWatch, client.ObjectList, ...client.ListOption,
		) error {
			return fmt.Errorf("authoritative list unavailable")
		}})
		r := &ClusterReconciler{Client: c, APIReader: reader}
		Expect(
			r.clustersForProvider(ctx, infra),
		).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}))

		s := &MachineSetReconciler{Client: c, APIReader: reader}
		Expect(s.setsForCluster(ctx, cluster)).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)}))

		m := &MachineReconciler{Client: c, APIReader: reader}
		Expect(
			m.machinesForCluster(ctx, cluster),
		).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(machine)}))
	})
})
