// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = DescribeTable("Mapping installation events to Agents", func(ctx SpecContext, reference string) {
	By("preparing Agents whose deployment names or namespaces differ")

	installation := &installv1.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{Namespace: "control-plane", Name: "installation"},
		Spec:       installv1.AgentClusterInstallSpec{ClusterDeploymentRef: corev1.LocalObjectReference{Name: reference}},
	}
	agent := &assistedv1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "target"},
		Spec: assistedv1.AgentSpec{
			ClusterDeploymentName: &assistedv1.ClusterReference{Name: "deployment", Namespace: "control-plane"},
		},
	}
	decoy := agent.DeepCopy()
	decoy.Name = "same-as-install"
	decoy.Spec.ClusterDeploymentName.Name = "installation"
	other := agent.DeepCopy()
	other.Name = "other-control-plane"
	other.Spec.ClusterDeploymentName.Namespace = "other-control-plane"
	c := fake.NewClientBuilder().WithScheme(agentScheme()).WithObjects(agent, decoy, other).Build()
	r := &AgentReconciler{APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}

	By("following the installation's explicit deployment reference")

	requests := r.agentsForInput(ctx, installation)
	if reference == "deployment" {
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].NamespacedName).To(Equal(client.ObjectKeyFromObject(agent)))
	} else {
		Expect(requests).To(BeEmpty())
	}
},
	Entry("queues only Agents of the referenced deployment", "deployment"),
	Entry("ignores an empty deployment reference", ""),
	Entry("ignores a missing deployment", "missing"),
)

var _ = DescribeTable("Mapping installation events to deployments", func(ctx SpecContext, name string) {
	installation := &installv1.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{Namespace: "control-plane", Name: "installation"},
		Spec:       installv1.AgentClusterInstallSpec{ClusterDeploymentRef: corev1.LocalObjectReference{Name: name}},
	}
	requests := deploymentForInstallation(ctx, installation)
	if name == "" {
		Expect(requests).To(BeEmpty())
	} else {
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].NamespacedName).To(Equal(client.ObjectKey{Namespace: installation.Namespace, Name: name}))
	}
},
	Entry("queues the referenced deployment", "deployment"),
	Entry("ignores an empty reference", ""),
)
