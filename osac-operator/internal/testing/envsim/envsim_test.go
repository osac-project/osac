/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package envsim

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const testNamespace = "default"

func unstructuredOf(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(testNamespace)
	return u
}

func getUnstructured(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	GinkgoHelper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: testNamespace}, u)).To(Succeed())
	return u
}

var _ = Describe("Environment simulator", func() {
	var sim *Simulator

	BeforeEach(func() {
		sim = New(k8sClient)
	})

	It("populates the InfraEnv discovery ignition URL", func() {
		infraEnv := unstructuredOf(infraEnvGVK, "ie-ready")
		Expect(k8sClient.Create(ctx, infraEnv)).To(Succeed())

		Expect(sim.MarkInfraEnvReady(ctx, "ie-ready", testNamespace, "https://ignition.example/x.ign")).To(Succeed())

		got := getUnstructured(infraEnvGVK, "ie-ready")
		url, found, err := unstructured.NestedString(got.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(url).To(Equal("https://ignition.example/x.ign"))
	})

	It("registers an Agent with the configured inventory MAC and binding", func() {
		Expect(sim.RegisterAgent(ctx, AgentOptions{
			Name:                  "agent-reg",
			Namespace:             testNamespace,
			MAC:                   "aa:bb:cc:dd:ee:ff",
			ClusterDeploymentName: "cd-reg",
		})).To(Succeed())

		got := getUnstructured(agentGVK, "agent-reg")
		ifaces, found, err := unstructured.NestedSlice(got.Object, "status", "inventory", "interfaces")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(ifaces).To(HaveLen(1))
		Expect(ifaces[0].(map[string]interface{})["macAddress"]).To(Equal("aa:bb:cc:dd:ee:ff"))

		cdName, found, err := unstructured.NestedString(got.Object, "spec", "clusterDeploymentName", "name")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(cdName).To(Equal("cd-reg"))
	})

	It("registers an unbound Agent (no clusterDeploymentName) with its MAC", func() {
		Expect(sim.RegisterAgent(ctx, AgentOptions{
			Name:      "agent-unbound",
			Namespace: testNamespace,
			MAC:       "de:ad:be:ef:00:01",
		})).To(Succeed())

		got := getUnstructured(agentGVK, "agent-unbound")
		ifaces, found, err := unstructured.NestedSlice(got.Object, "status", "inventory", "interfaces")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(ifaces).To(HaveLen(1))
		Expect(ifaces[0].(map[string]interface{})["macAddress"]).To(Equal("de:ad:be:ef:00:01"))

		_, found, err = unstructured.NestedMap(got.Object, "spec", "clusterDeploymentName")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeFalse(), "an unbound Agent must not carry clusterDeploymentName")
	})

	It("unbinds an Agent to unbinding-pending-user-action and clears its binding", func() {
		Expect(sim.RegisterAgent(ctx, AgentOptions{
			Name:                  "agent-unbind",
			Namespace:             testNamespace,
			MAC:                   "11:22:33:44:55:66",
			ClusterDeploymentName: "cd-unbind",
		})).To(Succeed())

		Expect(sim.UnbindAgent(ctx, "agent-unbind", testNamespace)).To(Succeed())

		got := getUnstructured(agentGVK, "agent-unbind")
		state, found, err := unstructured.NestedString(got.Object, "status", "debugInfo", "state")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(state).To(Equal(AgentUnbindingState))

		_, found, err = unstructured.NestedMap(got.Object, "spec", "clusterDeploymentName")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeFalse(), "clusterDeploymentName should be cleared on unbind")
	})

	It("ensures a ClusterDeployment exists and is idempotent", func() {
		Expect(sim.EnsureClusterDeployment(ctx, "cd-ensure", testNamespace)).To(Succeed())
		getUnstructured(clusterDeploymentGVK, "cd-ensure") // exists

		Expect(sim.EnsureClusterDeployment(ctx, "cd-ensure", testNamespace)).To(Succeed(), "second call must be idempotent")
	})

	It("sets NodePool observed replicas", func() {
		np := &hypershiftv1beta1.NodePool{}
		np.SetName("np-scale")
		np.SetNamespace(testNamespace)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		Expect(sim.SetNodePoolReplicas(ctx, "np-scale", testNamespace, 3)).To(Succeed())

		got := &hypershiftv1beta1.NodePool{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "np-scale", Namespace: testNamespace}, got)).To(Succeed())
		Expect(got.Status.Replicas).To(Equal(int32(3)))
	})
})
