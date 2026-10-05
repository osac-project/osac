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
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/test/testhelpers"
)

const networkCleanupTestFinalizer = "test.osac.openshift.io/provider-cleanup"

type cleanupWorkload struct {
	kind, idLabel, targetField, finalizer string
	spec                                  map[string]any
}

// NET-CLEAN-07 catches missing deployed watches/RBAC, premature finalizer
// removal, reversed cleanup order, and deletion of resources owned by others.
// The installed manager reconciles all objects. Unmanaged children and test
// finalizers simulate a provider that has not yet completed detach/release.
var _ = Describe("Deployed workload networking cleanup", Ordered, func() {
	const agentCRDName = "agents.agent-install.openshift.io"
	const hostedClusterCRDName = "hostedclusters.hypershift.openshift.io"
	var createdAgentCRD, createdHostedClusterCRD bool

	BeforeAll(func() {
		// ClusterOrder teardown lists Agents even when this workload allocated none.
		// Kind has no MCE: expose only the namespaced API needed for an empty list.
		// This minimal schema does not model Agent validation or provider behavior.
		output, err := exec.Command("kubectl", "get", "crd", agentCRDName,
			"--ignore-not-found", "-o", "name").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		if len(bytes.TrimSpace(output)) == 0 {
			cmd := exec.Command("kubectl", "create", "-f", "-")
			cmd.Stdin = strings.NewReader(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agents.agent-install.openshift.io
spec:
  group: agent-install.openshift.io
  scope: Namespaced
  names:
    kind: Agent
    listKind: AgentList
    plural: agents
    singular: agent
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
`)
			output, err = cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			createdAgentCRD = true
		}
		output, err = exec.Command("kubectl", "wait", "crd/"+agentCRDName,
			"--for=condition=Established", "--timeout=60s").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		// Kind does not always install HyperShift's HostedCluster API. ClusterOrder
		// teardown lists HostedClusters before deleting its namespace, so provide
		// only the namespaced list API needed for an empty result when absent.
		output, err = exec.Command("kubectl", "get", "crd", hostedClusterCRDName,
			"--ignore-not-found", "-o", "name").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		if len(bytes.TrimSpace(output)) == 0 {
			cmd := exec.Command("kubectl", "create", "-f", "-")
			cmd.Stdin = strings.NewReader(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: hostedclusters.hypershift.openshift.io
spec:
  group: hypershift.openshift.io
  scope: Namespaced
  names:
    kind: HostedCluster
    listKind: HostedClusterList
    plural: hostedclusters
    singular: hostedcluster
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
`)
			output, err = cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			createdHostedClusterCRD = true
		}
		output, err = exec.Command("kubectl", "wait", "crd/"+hostedClusterCRDName,
			"--for=condition=Established", "--timeout=60s").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
	})

	AfterAll(func() {
		if createdHostedClusterCRD {
			output, err := exec.Command("kubectl", "delete", "crd", hostedClusterCRDName,
				"--ignore-not-found", "--timeout=60s").CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
		}
		if createdAgentCRD {
			output, err := exec.Command("kubectl", "delete", "crd", agentCRDName,
				"--ignore-not-found", "--timeout=60s").CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
		}
	})
	for _, workload := range []cleanupWorkload{
		{"ClusterOrder", "clusterorder-uuid", "cluster", "osac.openshift.io/finalizer", map[string]any{"templateID": "noop"}},
		{"ComputeInstance", "computeinstance-uuid", "computeInstance", "osac.openshift.io/computeinstance", map[string]any{
			"templateID": "noop", "image": map[string]any{
				"sourceType": "registry", "sourceRef": "quay.io/fedora/fedora-coreos:stable",
			}, "vcpus": 1, "memoryGiB": 1,
			"bootDisk": map[string]any{"sizeGiB": 1, "storageTier": "test"}, "runStrategy": "Always",
		}},
		{"BareMetalInstance", "baremetalinstance-uuid", "baremetalInstance", "osac.openshift.io/baremetalinstance-cleanup",
			map[string]any{
				"templateID": "noop", "externalHostID": "",
				"selector": map[string]any{"hostSelector": map[string]any{"hostType": "test-cleanup"}},
			}},
	} {
		for _, marker := range []string{"auto-created", "auto-provisioned"} {
			It(fmt.Sprintf("cleans %s with %s ownership in dependency order", workload.kind, marker), func() {
				name := fmt.Sprintf("net-clean-%d", time.Now().UnixNano())
				ownerID := name + "-id"
				parent := cleanupObject(workload.kind, name, workload.spec)
				parent.SetLabels(map[string]string{"osac.openshift.io/" + workload.idLabel: ownerID})
				// ClusterOrder and ComputeInstance fixtures represent existing workloads.
				// Unmanaged avoids creating infrastructure before deletion; Manual then
				// bypasses the external provisioning boundary during teardown.
				if workload.kind != "BareMetalInstance" {
					parent.SetFinalizers([]string{networkCleanupTestFinalizer, workload.finalizer})
				} else {
					parent.SetFinalizers([]string{networkCleanupTestFinalizer})
					annotations := parent.GetAnnotations()
					// Use a managed instance so the controller arms its finalizer
					// before any networking child is projected.
					annotations["osac.openshift.io/management-state"] = "manual"
					parent.SetAnnotations(annotations)
				}
				DeferCleanup(func() { cleanupDelete(parent) })
				cleanupCreate(parent)
				if workload.kind == "BareMetalInstance" {
					By("waiting for the controller to arm networking cleanup before any child IP exists")
					Eventually(func(g Gomega) {
						current, err := cleanupGet(parent)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(current.GetFinalizers()).To(ContainElement(workload.finalizer))
					}, 2*time.Minute, time.Second).Should(Succeed())
				}

				var owned, preserved []*unstructured.Unstructured
				for _, ownership := range []string{"owned", "manual", "foreign"} {
					targetID := ownerID
					if ownership == "foreign" {
						targetID = ownerID + "-other"
					}
					labels := map[string]string{"osac.openshift.io/" + marker + "-for": targetID}
					if ownership != "manual" {
						labels["osac.openshift.io/"+marker] = "true"
					}
					ipName := name + "-" + ownership + "-ip"
					ip := cleanupObject("ExternalIP", ipName, map[string]any{"pool": "test-pool"})
					ip.SetLabels(labels)
					attachmentSpec := map[string]any{"externalIP": ipName, workload.targetField: targetID}
					if workload.kind == "ClusterOrder" {
						attachmentSpec["targetEndpoint"] = "API"
					}
					attachment := cleanupObject("ExternalIPAttachment", name+"-"+ownership+"-attachment", attachmentSpec)
					attachment.SetLabels(labels)
					for _, object := range []*unstructured.Unstructured{ip, attachment} {
						cleanupCreate(object)
						DeferCleanup(func(object *unstructured.Unstructured) func() {
							return func() { cleanupDelete(object) }
						}(object))
						if ownership == "owned" {
							owned = append(owned, object)
						} else {
							preserved = append(preserved, object)
						}
					}
				}
				ip, attachment := owned[0], owned[1]

				By("waiting for the deployed controller's parent finalizer")
				Eventually(func(g Gomega) {
					current, err := cleanupGet(parent)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(current.GetFinalizers()).To(ContainElement(workload.finalizer))
				}, 2*time.Minute, time.Second).Should(Succeed())
				if workload.kind != "BareMetalInstance" {
					By("confirming the unmanaged fixture did not start provisioning")
					current, err := cleanupGet(parent)
					Expect(err).NotTo(HaveOccurred())
					Expect(current.GetAnnotations()["osac.openshift.io/management-state"]).To(Equal("unmanaged"))
					Expect(current.Object["status"]).To(BeNil())

					// Switch to Manual before requesting deletion. If the controller
					// observes deletion while still Unmanaged, it can start provider
					// deprovisioning before this test's annotation update arrives.
					annotateOutput, err := exec.Command("kubectl", "annotate", workload.kind, name,
						"-n", operatorNamespace,
						"osac.openshift.io/management-state=manual", "--overwrite").CombinedOutput()
					Expect(err).NotTo(HaveOccurred(), string(annotateOutput))
				}

				By("deleting the parent through Kubernetes")
				output, err := exec.Command("kubectl", "delete", workload.kind, name,
					"-n", operatorNamespace, "--wait=false").CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), string(output))
				Eventually(func(g Gomega) {
					current, getErr := cleanupGet(attachment)
					g.Expect(getErr).NotTo(HaveOccurred())
					g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
				}, 2*time.Minute, time.Second).Should(Succeed())

				assertParentHeld := func(g Gomega) {
					current, getErr := cleanupGet(parent)
					g.Expect(getErr).NotTo(HaveOccurred())
					g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
					g.Expect(current.GetFinalizers()).To(ContainElement(workload.finalizer))
				}
				assertPreserved := func(g Gomega) {
					for _, original := range preserved {
						current, getErr := cleanupGet(original)
						g.Expect(getErr).NotTo(HaveOccurred())
						g.Expect(current.GetUID()).To(Equal(original.GetUID()))
						g.Expect(current.GetDeletionTimestamp()).To(BeNil(), "%s/%s must be preserved",
							original.GetKind(), original.GetName())
						g.Expect(current.GetFinalizers()).To(ContainElement(networkCleanupTestFinalizer))
					}
				}
				By("holding attachment cleanup across the controller's polling interval")
				Consistently(func(g Gomega) {
					current, getErr := cleanupGet(ip)
					g.Expect(getErr).NotTo(HaveOccurred())
					g.Expect(current.GetDeletionTimestamp()).To(BeNil(), "EIP deletion must wait for EIA removal")
					assertParentHeld(g)
					assertPreserved(g)
				}, 35*time.Second, 2*time.Second).Should(Succeed())

				By("finishing attachment provider cleanup and observing EIP deletion")
				cleanupRelease(attachment)
				Eventually(func() (bool, error) { return cleanupAbsent(attachment) }, time.Minute, time.Second).Should(BeTrue())
				Eventually(func(g Gomega) {
					current, getErr := cleanupGet(ip)
					g.Expect(getErr).NotTo(HaveOccurred())
					g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
					assertParentHeld(g)
				}, 2*time.Minute, time.Second).Should(Succeed())
				Consistently(func(g Gomega) {
					assertParentHeld(g)
					assertPreserved(g)
				}, 35*time.Second, 2*time.Second).Should(Succeed())

				By("finishing EIP provider cleanup before the parent finalizer is released")
				cleanupRelease(ip)
				Eventually(func() (bool, error) { return cleanupAbsent(ip) }, time.Minute, time.Second).Should(BeTrue())
				Eventually(func(g Gomega) {
					current, getErr := cleanupGet(parent)
					g.Expect(getErr).NotTo(HaveOccurred())
					g.Expect(current.GetFinalizers()).NotTo(ContainElement(workload.finalizer))
					g.Expect(current.GetFinalizers()).To(ContainElement(networkCleanupTestFinalizer))
					assertPreserved(g)
				}, 2*time.Minute, time.Second).Should(Succeed())
			})
		}
	}
})

func cleanupObject(kind, name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "osac.openshift.io/v1alpha1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": operatorNamespace,
			"annotations": map[string]any{
				"osac.openshift.io/management-state": "unmanaged",
				"osac.openshift.io/tenant":           "network-cleanup-test",
				"osac.openshift.io/owner-reference":  name,
			},
			"finalizers": []any{networkCleanupTestFinalizer}},
		"spec": spec,
	}}
}

func cleanupCreate(object *unstructured.Unstructured) {
	data, err := object.MarshalJSON()
	Expect(err).NotTo(HaveOccurred())
	cmd := exec.Command("kubectl", "create", "-f", "-", "-o", "json")
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	Expect(object.UnmarshalJSON(output)).To(Succeed())
}

func cleanupGet(object *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return testhelpers.Get(operatorNamespace, object.GetKind(), object.GetName())
}

func cleanupAbsent(object *unstructured.Unstructured) (bool, error) {
	return testhelpers.Absent(operatorNamespace, object.GetKind(), object.GetName())
}

func cleanupDelete(object *unstructured.Unstructured) {
	testhelpers.DeleteAndWaitForCleanup(operatorNamespace, object.GetKind(), object.GetName())
}

// Release only the test's finalizer. A resourceVersion precondition makes a
// concurrent controller update retryable without overwriting its finalizers.
func cleanupRelease(object *unstructured.Unstructured) {
	testhelpers.ReleaseFinalizer(operatorNamespace, object.GetKind(), object.GetName(), networkCleanupTestFinalizer)
}
