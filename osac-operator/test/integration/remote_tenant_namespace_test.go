/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
)

// registerTenantNamespaceSpecs reuses the enclosing two-cluster suite's setup.
// The getter reads its state after BeforeAll has initialized it.
func registerTenantNamespaceSpecs(env func() (string, map[string]string)) {
	Context("Tenant namespace lifecycle", func() {
		It("creates and deletes only the Tenant namespace on the remote cluster", func() {
			remoteKubeconfigPath, tenantUIDs := env()
			const name = "osac-7297-tenant-namespace"
			const tenantFinalizer = "osac.openshift.io/tenant"
			const holdFinalizer = "test.osac.openshift.io/hold-tenant-namespace"
			const namespaceHoldFinalizer = "test.osac.openshift.io/hold-remote-namespace"

			By("creating a standalone Tenant with a test finalizer and no storage controller ownership")
			tenantUID, err := createMgmtFixtureFromYAML(tenantNamespaceLifecycleCRYAML(name, operatorNamespace))
			Expect(err).NotTo(HaveOccurred())
			tenantUIDs[name] = tenantUID

			By("waiting for the Tenant controller to create the named namespace on the workload cluster")
			Eventually(func(g Gomega) {
				status := fetchTenantStatus(g, name)
				g.Expect(status.Phase).To(Equal("Ready"))
				g.Expect(status.Finalizers).To(ContainElement(tenantFinalizer))
				g.Expect(status.Finalizers).To(ContainElement(holdFinalizer))

				out, err := runKubectlWithKubeconfig(remoteKubeconfigPath, "get", "namespace", name, "-o", "json")
				g.Expect(err).NotTo(HaveOccurred())
				var namespace struct {
					Metadata struct {
						Labels map[string]string `json:"labels"`
					} `json:"metadata"`
				}
				g.Expect(json.Unmarshal(out, &namespace)).To(Succeed())
				g.Expect(namespace.Metadata.Labels).To(HaveKeyWithValue("osac.openshift.io/tenant", name))
				g.Expect(namespace.Metadata.Labels).To(HaveKeyWithValue("osac.openshift.io/tenant-ref", name))
				g.Expect(namespace.Metadata.Labels).To(HaveKeyWithValue("osac.openshift.io/project", operatorNamespace))
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			managementNamespace, err := runMgmtKubectl("get", "namespace", name, "--ignore-not-found", "-o", "name")
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(managementNamespace))).To(BeEmpty())

			By("adding a test finalizer to the remote namespace so deletion can be observed")
			Expect(updateRemoteNamespaceFinalizer(
				remoteKubeconfigPath, name, corev1.FinalizerName(namespaceHoldFinalizer), true,
			)).To(Succeed())

			By("deleting the Tenant and verifying its namespace stays until the other finalizer is removed")
			Expect(deleteMgmtObjectByUID(
				"osac.openshift.io/v1alpha1", "Tenant", operatorNamespace, name, tenantUID,
			)).To(Succeed())
			Eventually(func(g Gomega) string {
				out, err := runMgmtKubectl(
					"get", "tenant", name, "-n", operatorNamespace, "-o", "jsonpath={.metadata.deletionTimestamp}",
				)
				g.Expect(err).NotTo(HaveOccurred())
				return strings.TrimSpace(string(out))
			}, 30*time.Second, time.Second).ShouldNot(BeEmpty())
			namespaceNameOutput, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath, "get", "namespace", name, "--ignore-not-found", "-o", "name",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(namespaceNameOutput))).NotTo(BeEmpty())

			By("releasing the test finalizer and waiting for namespace and Tenant deletion")
			tenantJSON, err := runMgmtKubectl("get", "tenant", name, "-n", operatorNamespace, "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			var tenant struct {
				Metadata struct {
					Finalizers []string `json:"finalizers"`
				} `json:"metadata"`
			}
			Expect(json.Unmarshal(tenantJSON, &tenant)).To(Succeed())
			holdIndex := -1
			for i, finalizer := range tenant.Metadata.Finalizers {
				if finalizer == holdFinalizer {
					holdIndex = i
					break
				}
			}
			Expect(holdIndex).To(BeNumerically(">=", 0))
			patch := fmt.Sprintf(`[{"op":"remove","path":"/metadata/finalizers/%d"}]`, holdIndex)
			_, err = runMgmtKubectl("patch", "tenant", name, "-n", operatorNamespace, "--type=json", "-p", patch)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				remoteNamespaceJSON, err := runKubectlWithKubeconfig(
					remoteKubeconfigPath, "get", "namespace", name, "-o", "json",
				)
				g.Expect(err).NotTo(HaveOccurred())
				var remoteNamespace struct {
					Metadata struct {
						DeletionTimestamp string `json:"deletionTimestamp"`
					} `json:"metadata"`
					Spec struct {
						Finalizers []string `json:"finalizers"`
					} `json:"spec"`
				}
				g.Expect(json.Unmarshal(remoteNamespaceJSON, &remoteNamespace)).To(Succeed())
				g.Expect(remoteNamespace.Metadata.DeletionTimestamp).NotTo(BeEmpty())
				g.Expect(remoteNamespace.Spec.Finalizers).To(ContainElement(namespaceHoldFinalizer))
				g.Expect(fetchTenantStatus(g, name).Finalizers).To(ContainElement(tenantFinalizer))
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			By("releasing the remote namespace finalizer")
			Expect(updateRemoteNamespaceFinalizer(
				remoteKubeconfigPath, name, corev1.FinalizerName(namespaceHoldFinalizer), false,
			)).To(Succeed())
			Eventually(func(g Gomega) {
				remoteNamespace, err := runKubectlWithKubeconfig(
					remoteKubeconfigPath, "get", "namespace", name, "--ignore-not-found", "-o", "name",
				)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(remoteNamespace))).To(BeEmpty())
				managementTenant, err := runMgmtKubectl(
					"get", "tenant", name, "-n", operatorNamespace, "--ignore-not-found", "-o", "name",
				)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(managementTenant))).To(BeEmpty())
			}, 90*time.Second, 2*time.Second).Should(Succeed())
			delete(tenantUIDs, name)
		})
	})
}
