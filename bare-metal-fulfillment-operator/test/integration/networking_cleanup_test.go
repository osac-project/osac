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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/test/testhelpers"
)

const (
	osacNetworkCleanupFinalizer    = "osac.openshift.io/baremetalinstance-cleanup"
	osacNetworkAttachmentFinalizer = "osac.openshift.io/networkattachment"
	bmfNetworkAttachmentFinalizer  = "osac.openshift.io/baremetalinstance-networking"
	bmfCleanupMetadataKey          = "metadata"
	bmfCleanupFinalizersKey        = "finalizers"
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

	It("powers off before deleting the physical attachment and releasing the host", func() {
		name := fmt.Sprintf("net-clean-bmf-attachment-%d", time.Now().UnixNano())
		hostName := name + "-host"
		attachmentName := "baremetalinstance-" + name + "-eth0"
		DeferCleanup(func() {
			if _, err := bmfCleanupGet("NetworkAttachment", attachmentName); err == nil {
				testhelpers.ReleaseFinalizer(namespace, "NetworkAttachment", attachmentName,
					osacNetworkAttachmentFinalizer)
			}
			cleanupBMFDelete("BareMetalInstance", name)
			cleanupBMFDelete("NetworkAttachment", attachmentName)
			cleanupBMFDelete("BareMetalHost", hostName)
		})

		createAvailableBareMetalHost(hostName, namespace, name)
		cmd := exec.Command("kubectl", "annotate", "baremetalhost", hostName, "-n", namespace,
			`osac.openshift.io/interface-macs={"eth0":"52:54:00:12:34:56"}`, "--overwrite")
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		cmd = exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = createNetworkedBareMetalInstanceYAML(name, namespace, name)
		output, err = cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", name, namespace, "{.spec.externalHostID}")
		}, 2*time.Minute, 2*time.Second).Should(Equal(namespace + "/" + hostName))
		Eventually(func() (string, error) {
			return kubectlJSONPath("networkattachment", attachmentName, namespace, "{.metadata.name}")
		}, 2*time.Minute, 2*time.Second).Should(Equal(attachmentName))

		By("marking the NetworkAttachment ready so BMF starts the handoff power cycle")
		readyStatus, err := json.Marshal(map[string]any{"status": map[string]any{
			"conditions": []map[string]any{{
				"type": "Ready", "status": "True", "reason": "Succeeded",
				"message":            "NetworkAttachment is ready",
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			}},
		}})
		Expect(err).NotTo(HaveOccurred())
		output, err = exec.Command("kubectl", "patch", "networkattachment", attachmentName,
			"-n", namespace, "--subresource=status", "--type=merge", "-p", string(readyStatus)).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalhost", hostName, namespace, "{.spec.online}")
		}, time.Minute, time.Second).Should(Equal("true"), "host must be online before the offboarding assertion")

		By("simulating the management backend confirming power-on")
		patchBareMetalHostStatus(hostName, namespace, `{"status":{"poweredOn":true}}`)
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", name, namespace,
				`{.status.conditions[?(@.type=="NetworkHandoffComplete")].status}`)
		}, time.Minute, time.Second).Should(Equal("True"))

		attachment, err := bmfCleanupGet("NetworkAttachment", attachmentName)
		Expect(err).NotTo(HaveOccurred())
		Expect(attachment.GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", name))
		Expect(attachment.GetFinalizers()).To(ContainElement(bmfNetworkAttachmentFinalizer))
		subnet, found, err := unstructured.NestedString(attachment.Object, "spec", "subnet")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(subnet).To(Equal("test-subnet"))
		interfaceName, found, err := unstructured.NestedString(attachment.Object, "spec", "interface", "name")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(interfaceName).To(Equal("eth0"))
		mac, found, err := unstructured.NestedString(attachment.Object, "spec", "interface", "macAddress")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(mac).To(Equal("52:54:00:12:34:56"))

		By("rejecting an invalid selected-port MAC at the Kubernetes API boundary")
		invalidStatus := `{
		  "status": {
		    "networkAttachmentStatuses": [
		      {"interface": "eth0", "subnetRef": "test-subnet", "macAddress": "not-a-mac"}
		    ]
		  }
		}`
		output, err = exec.Command("kubectl", "patch", "baremetalinstance", name,
			"-n", namespace, "--subresource=status", "--type=merge", "-p", invalidStatus).CombinedOutput()
		Expect(err).To(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("match"))

		By("simulating the networking controller owning its cleanup finalizer")
		finalizers := append(attachment.GetFinalizers(), osacNetworkAttachmentFinalizer)
		patch, err := json.Marshal(map[string]any{bmfCleanupMetadataKey: map[string]any{
			"resourceVersion": attachment.GetResourceVersion(), bmfCleanupFinalizersKey: finalizers,
		}})
		Expect(err).NotTo(HaveOccurred())
		output, err = exec.Command("kubectl", "patch", "networkattachment", attachmentName,
			"-n", namespace, "--type=merge", "-p", string(patch)).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		patchBareMetalHostStatus(hostName, namespace, `{"status":{"poweredOn":true}}`)
		output, err = exec.Command("kubectl", "delete", "baremetalinstance", name,
			"-n", namespace, "--wait=false").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		By("waiting for BMF to request power-off before requesting attachment deletion")
		Eventually(func(g Gomega) {
			current, getErr := bmfCleanupGet("BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
			g.Expect(current.GetFinalizers()).To(ContainElements(
				bmfNetworkAttachmentFinalizer, "osac.openshift.io/inventory", "osac.openshift.io/baremetalinstance"))
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			online, found, getErr := unstructured.NestedBool(host.Object, "spec", "online")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found && !online).To(BeTrue(), "BMF should request host power-off")
			currentAttachment, getErr := bmfCleanupGet("NetworkAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(currentAttachment.GetDeletionTimestamp()).To(BeNil(),
				"attachment cleanup must wait for observed power-off")
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		patchBareMetalHostStatus(hostName, namespace, `{"status":{"poweredOn":false}}`)
		Eventually(func(g Gomega) {
			currentAttachment, getErr := bmfCleanupGet("NetworkAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(currentAttachment.GetDeletionTimestamp()).NotTo(BeNil())
			g.Expect(currentAttachment.GetFinalizers()).To(ContainElements(
				bmfNetworkAttachmentFinalizer, osacNetworkAttachmentFinalizer))
			current, getErr := bmfCleanupGet("BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetFinalizers()).To(ContainElement(bmfNetworkAttachmentFinalizer))
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(ref["name"]).NotTo(BeEmpty())
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		By("holding BMF ownership until the networking controller finishes cleanup")
		Consistently(func(g Gomega) {
			currentAttachment, getErr := bmfCleanupGet("NetworkAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(currentAttachment.GetFinalizers()).To(ContainElements(
				bmfNetworkAttachmentFinalizer, osacNetworkAttachmentFinalizer))
			current, getErr := bmfCleanupGet("BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetFinalizers()).To(ContainElement(bmfNetworkAttachmentFinalizer))
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(ref["name"]).NotTo(BeEmpty())
		}, 35*time.Second, 2*time.Second).Should(Succeed())

		By("releasing the simulated network cleanup finalizer")
		testhelpers.ReleaseFinalizer(namespace, "NetworkAttachment", attachmentName,
			osacNetworkAttachmentFinalizer)
		Eventually(func(g Gomega) {
			absent, getErr := testhelpers.Absent(namespace, "BareMetalInstance", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(absent).To(BeTrue())
			host, getErr := bmfCleanupGet("BareMetalHost", hostName)
			g.Expect(getErr).NotTo(HaveOccurred())
			ref, found, getErr := unstructured.NestedMap(host.Object, "spec", "consumerRef")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(!found || len(ref) == 0).To(BeTrue())
			attachmentAbsent, getErr := testhelpers.Absent(namespace, "NetworkAttachment", attachmentName)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(attachmentAbsent).To(BeTrue())
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})
})

func createNetworkedBareMetalInstanceYAML(name, ns, hostType string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/baremetalinstance-uuid: %s
  annotations:
    osac.openshift.io/tenant: %s
    osac.openshift.io/owner-reference: %s
spec:
  selector:
    hostSelector:
      hostType: %s
  externalHostID: ""
  templateID: noop
  runStrategy: Always
  networkAttachments:
    - subnetRef: test-subnet
      securityGroupRefs:
        - test-security-group
      interface: eth0
      primary: true
`, name, ns, name, name, name, hostType)
	return strings.NewReader(yaml)
}

func bmfCleanupReleaseOSACFinalizer(name string) {
	testhelpers.ReleaseFinalizer(namespace, "BareMetalInstance", name, osacNetworkCleanupFinalizer)
}

func bmfCleanupGet(kind, name string) (*unstructured.Unstructured, error) {
	return testhelpers.Get(namespace, kind, name)
}

func cleanupBMFDelete(kind, name string) {
	testhelpers.DeleteAndWaitForCleanup(namespace, kind, name)
}
