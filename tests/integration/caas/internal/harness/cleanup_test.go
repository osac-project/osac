// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Native resource cleanup observations", func() {
	It("returns a resource listing failure without treating it as empty", func(ctx SpecContext) {
		scheme, err := apiScheme()
		Expect(err).NotTo(HaveOccurred())

		failure := errors.New("resource listing unavailable")
		c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return failure
			},
		}).Build()
		Expect(CheckNativeResourcesGone(ctx, &Environment{Namespace: "hub", Client: c})).To(MatchError(failure))
	})

	It("accepts empty native inputs and outputs", func(ctx SpecContext) {
		scheme, err := apiScheme()
		Expect(err).NotTo(HaveOccurred())

		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		Expect(CheckNativeResourcesGone(ctx, &Environment{Namespace: "hub", Client: c})).To(Succeed())
	})

	DescribeTable("preserves discovery scope and checks working resources across namespaces", func(
		ctx SpecContext, object client.Object, namespace string, managed, remains bool,
	) {
		object = object.DeepCopyObject().(client.Object)
		object.SetName("retained")
		object.SetNamespace(namespace)

		if managed {
			object.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "osac-provider-simulator"})
		}

		scheme, err := apiScheme()
		Expect(err).NotTo(HaveOccurred())

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).Build()

		err = CheckNativeResourcesGone(ctx, &Environment{Namespace: "hub", Client: c})
		if remains {
			Expect(err).To(HaveOccurred())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("hub InfraEnv", &assistedv1.InfraEnv{}, "hub", false, true),
		Entry("foreign InfraEnv", &assistedv1.InfraEnv{}, "foreign", false, false),
		Entry("hub Agent", &assistedv1.Agent{}, "hub", false, true),
		Entry("foreign Agent", &assistedv1.Agent{}, "foreign", false, false),
		Entry("simulator Agent outside the hub", &assistedv1.Agent{}, "foreign", true, true),
		Entry("HostedCluster outside the hub", &hypershiftv1.HostedCluster{}, "working", false, true),
		Entry("NodePool outside the hub", &hypershiftv1.NodePool{}, "working", false, true),
		Entry("HostedControlPlane", &hypershiftv1.HostedControlPlane{}, "working", false, true),
		Entry("CAPI Cluster", &capiv1.Cluster{}, "working", false, true),
		Entry("MachineSet", &capiv1.MachineSet{}, "working", false, true),
		Entry("Machine", &capiv1.Machine{}, "working", false, true),
		Entry("AgentCluster", &agentv1.AgentCluster{}, "working", false, true),
		Entry("AgentMachine", &agentv1.AgentMachine{}, "working", false, true),
		Entry("AgentMachineTemplate", &agentv1.AgentMachineTemplate{}, "working", false, true),
		Entry("ClusterDeployment", &hivev1.ClusterDeployment{}, "working", false, true),
		Entry("AgentClusterInstall", &installv1.AgentClusterInstall{}, "working", false, true),
		Entry("simulator namespace", &corev1.Namespace{}, "", true, true),
		Entry("foreign namespace", &corev1.Namespace{}, "", false, false),
		Entry("simulator Service", &corev1.Service{}, "working", true, true),
		Entry("foreign Service", &corev1.Service{}, "working", false, false),
		Entry("simulator Secret", &corev1.Secret{}, "working", true, true),
		Entry("foreign Secret", &corev1.Secret{}, "working", false, false),
	)
})
