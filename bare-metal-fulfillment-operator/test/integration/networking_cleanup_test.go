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
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/test/testhelpers"
)

const (
	osacNetworkCleanupFinalizer = "osac.openshift.io/baremetalinstance-cleanup"
	bmfCleanupMetadataKey       = "metadata"
	bmfCleanupFinalizersKey     = "finalizers"
)

// NET-CLEAN-08 exercises the installed BMF manager. Only Metal3 hardware
// status is simulated; host assignment, teardown and finalizers are real.
var _ = Describe("BMF networking cleanup ownership", func() {
	It("waits for OSAC cleanup before host teardown and leaves external resources untouched", func() {
		name := fmt.Sprintf("net-clean-bmf-%d", time.Now().UnixNano())
		hostName := name + "-host"
		ipName, attachmentName := name+"-ip", name+"-attachment"
		DeferCleanup(func() { cleanupBMFDelete("BareMetalHost", hostName) })
		createAvailableBareMetalHost(hostName, namespace, name)
		cmd := exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = createBareMetalInstanceYAML(name, namespace, name, "Always")
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		DeferCleanup(func() { cleanupBMFDelete("BareMetalInstance", name) })
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", name, namespace, "{.spec.externalHostID}")
		}, 2*time.Minute, 2*time.Second).Should(Equal(namespace + "/" + hostName))
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalhost", hostName, namespace, "{.spec.online}")
		}, time.Minute, time.Second).Should(Equal("true"))
		patchBareMetalHostStatus(hostName, namespace, `{"status":{"poweredOn":true}}`)
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", name, namespace, "{.status.phase}")
		}, 2*time.Minute, time.Second).Should(Equal("Ready"))

		By("recording that BMF owns both inventory and management teardown")
		parent, err := bmfCleanupGet("BareMetalInstance", name)
		Expect(err).NotTo(HaveOccurred())
		Expect(parent.GetFinalizers()).To(ContainElements(
			"osac.openshift.io/inventory", "osac.openshift.io/baremetalinstance"))
		host, err := bmfCleanupGet("BareMetalHost", hostName)
		Expect(err).NotTo(HaveOccurred())
		consumer, _, err := unstructured.NestedString(host.Object, "spec", "consumerRef", "name")
		Expect(err).NotTo(HaveOccurred())
		Expect(consumer).To(Equal(string(parent.GetUID())))

		By("installing the finalizer owned by the absent OSAC manager")
		finalizers := append(parent.GetFinalizers(), osacNetworkCleanupFinalizer)
		patch, err := json.Marshal(map[string]any{bmfCleanupMetadataKey: map[string]any{
			"resourceVersion": parent.GetResourceVersion(), bmfCleanupFinalizersKey: finalizers,
			"labels":      map[string]string{"osac.openshift.io/baremetalinstance-uuid": name},
			"annotations": map[string]string{"osac.openshift.io/tenant": name, "osac.openshift.io/owner-reference": name},
		}})
		Expect(err).NotTo(HaveOccurred())
		output, err = exec.Command("kubectl", "patch", "baremetalinstance", name,
			"-n", namespace, "--type=merge", "-p", string(patch)).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		for _, resource := range [][2]string{{"ExternalIPAttachment", attachmentName}, {"ExternalIP", ipName}} {
			spec := map[string]any{"pool": "test-pool"}
			if resource[0] == "ExternalIPAttachment" {
				spec = map[string]any{"externalIP": ipName, "baremetalInstance": name}
			}
			object := map[string]any{
				"apiVersion": "osac.openshift.io/v1alpha1", "kind": resource[0],
				bmfCleanupMetadataKey: map[string]any{"name": resource[1], "namespace": namespace,
					"labels": map[string]string{"osac.openshift.io/test-resource": "manual"},
					"annotations": map[string]string{
						"osac.openshift.io/tenant":          name,
						"osac.openshift.io/owner-reference": name,
					},
					bmfCleanupFinalizersKey: []string{"test.osac.openshift.io/provider-cleanup"}},
				"spec": spec,
			}
			data, marshalErr := json.Marshal(object)
			Expect(marshalErr).NotTo(HaveOccurred())
			cmd = exec.Command("kubectl", "create", "-f", "-")
			cmd.Stdin = bytes.NewReader(data)
			output, err = cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			resourceKind, resourceName := resource[0], resource[1]
			DeferCleanup(func() { cleanupBMFDelete(resourceKind, resourceName) })
		}
		ipBefore, err := bmfCleanupGet("ExternalIP", ipName)
		Expect(err).NotTo(HaveOccurred())
		attachmentBefore, err := bmfCleanupGet("ExternalIPAttachment", attachmentName)
		Expect(err).NotTo(HaveOccurred())

		By("deleting through Kubernetes and verifying BMF waits for OSAC cleanup")
		output, err = exec.Command("kubectl", "delete", "baremetalinstance", name,
			"-n", namespace, "--wait=false").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		Eventually(func(g Gomega) {
			current, getErr := bmfCleanupGet("BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
			g.Expect(current.GetFinalizers()).To(ContainElements(
				osacNetworkCleanupFinalizer, "osac.openshift.io/inventory", "osac.openshift.io/baremetalinstance"))
			phase, found, getErr := unstructured.NestedString(current.Object, "status", "phase")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found && phase == "Deleting").To(BeTrue(), "instance should report Deleting while OSAC cleanup is pending")
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(ref["name"]).To(Equal(string(parent.GetUID())), "host must stay assigned during network cleanup")
			poweredOn, found, getErr := unstructured.NestedBool(host.Object, "status", "poweredOn")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found && poweredOn).To(BeTrue(), "host teardown must wait for network cleanup")
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		By("proving BMF holds the host and leaves OSAC resources unchanged while the OSAC finalizer remains")
		Consistently(func(g Gomega) {
			current, getErr := bmfCleanupGet("BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetFinalizers()).To(ContainElements(
				osacNetworkCleanupFinalizer, "osac.openshift.io/inventory", "osac.openshift.io/baremetalinstance"))
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(ref["name"]).To(Equal(string(parent.GetUID())))
			poweredOn, found, getErr := unstructured.NestedBool(host.Object, "status", "poweredOn")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found && poweredOn).To(BeTrue())
			ip, getErr := bmfCleanupGet("ExternalIP", ipName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(ip.Object).To(Equal(ipBefore.Object))
			attachment, getErr := bmfCleanupGet("ExternalIPAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(attachment.Object).To(Equal(attachmentBefore.Object))
		}, 35*time.Second, 2*time.Second).Should(Succeed())

		By("simulating OSAC finishing network cleanup, then waiting for BMF host teardown")
		bmfCleanupReleaseOSACFinalizer(name)
		Eventually(func(g Gomega) {
			// BMF may finish teardown and Kubernetes may delete the BMI before this
			// poll observes the finalizer list. The host is the durable signal that
			// OSAC released teardown and BMF returned the instance to inventory.
			absent, getErr := testhelpers.Absent(namespace, "BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(absent).To(BeTrue(), "BareMetalInstance should be deleted after BMF teardown")
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(!found || len(ref) == 0).To(BeTrue(), "host should return to inventory after OSAC cleanup")
			ip, getErr := bmfCleanupGet("ExternalIP", ipName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(ip.Object).To(Equal(ipBefore.Object))
			attachment, getErr := bmfCleanupGet("ExternalIPAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(attachment.Object).To(Equal(attachmentBefore.Object))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})
})

func bmfCleanupReleaseOSACFinalizer(name string) {
	testhelpers.ReleaseFinalizer(namespace, "BareMetalInstance", name, osacNetworkCleanupFinalizer)
}

func bmfCleanupGet(kind, name string) (*unstructured.Unstructured, error) {
	return testhelpers.Get(namespace, kind, name)
}

func cleanupBMFDelete(kind, name string) {
	testhelpers.DeleteAndWaitForCleanup(namespace, kind, name)
}
