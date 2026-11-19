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
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/utils"
)

const (
	interfaceMACAnnotation  = `{"data-0":"aa:bb:cc:dd:ee:ff"}`
	selectedPortMAC         = "aa:bb:cc:dd:ee:ff"
	networkAttachmentSubnet = "subnet-kind-it"
)

var _ = Describe("BareMetalInstance networking", func() {
	It("persists the selected port MAC and creates its tenant NetworkAttachment", func() {
		id := fmt.Sprintf("net-it-%d", time.Now().UnixNano())
		hostName := id + "-host"
		hostType := id
		attachmentName := "baremetalinstance-" + id + "-data-0"

		createAvailableBareMetalHostWithInterfaceMACs(hostName, namespace, hostType, interfaceMACAnnotation)
		DeferCleanup(func() { deleteBareMetalHost(hostName, namespace) })

		By("creating a tenant BareMetalInstance requesting the fake host")
		cmd := exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = createNetworkBareMetalInstanceYAML(id, namespace, hostType)
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		DeferCleanup(func() {
			cleanupBMFDelete("BareMetalInstance", id)
			cleanupBMFDelete("NetworkAttachment", attachmentName)
		})

		By("waiting for BMF to assign the fake host")
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", id, namespace, "{.spec.externalHostID}")
		}).Should(Equal(namespace + "/" + hostName))

		By("waiting for BMF to persist the selected interface MAC")
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", id, namespace,
				"{.status.networkAttachmentStatuses[0].macAddress}")
		}).Should(Equal(selectedPortMAC))

		By("checking the installed CRD rejects non-canonical MAC addresses")
		patchMACStatus := func(mac string) *exec.Cmd {
			payload := fmt.Sprintf(
				`{"status":{"networkAttachmentStatuses":[{"subnetRef":%q,"interface":"data-0","macAddress":%q}]}}`,
				networkAttachmentSubnet, mac)
			return exec.Command("kubectl", "patch", "baremetalinstance", id, "-n", namespace,
				"--type=merge", "--subresource=status", "--dry-run=server", "-p", payload)
		}
		invalidOutput, invalidErr := patchMACStatus("AA:BB:CC:DD:EE:FF").CombinedOutput()
		Expect(invalidErr).To(HaveOccurred(), string(invalidOutput))
		validOutput, validErr := patchMACStatus(selectedPortMAC).CombinedOutput()
		Expect(validErr).NotTo(HaveOccurred(), string(validOutput))

		By("verifying the deployed controller created the attachment with the selected MAC")
		var raw string
		Eventually(func() error {
			cmd := exec.Command("kubectl", "get", "networkattachment", attachmentName,
				"-n", namespace, "-o", "json")
			var getErr error
			raw, getErr = utils.Run(cmd)
			return getErr
		}).Should(Succeed())

		var attachment struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Target struct {
					ID string `json:"id"`
				} `json:"target"`
				SecurityGroups []string `json:"securityGroups"`
				Interface      struct {
					Name       string `json:"name"`
					MACAddress string `json:"macAddress"`
				} `json:"interface"`
			} `json:"spec"`
		}
		Expect(json.Unmarshal([]byte(raw), &attachment)).To(Succeed())
		Expect(attachment.Metadata.Annotations["osac.openshift.io/tenant"]).To(Equal("tenant-kind-it"))
		Expect(attachment.Spec.Target.ID).To(Equal(id))
		Expect(attachment.Spec.SecurityGroups).To(Equal([]string{"sg-kind-it"}))
		Expect(attachment.Spec.Interface.Name).To(Equal("data-0"))
		Expect(attachment.Spec.Interface.MACAddress).To(Equal(selectedPortMAC))

		By("simulating the networking controller accepting the attachment and DHCP lease")
		naStatus := `{"status":{"conditions":[{"type":"Ready","status":"True","reason":"Succeeded",` +
			`"message":"test fixture","lastTransitionTime":"2026-01-01T00:00:00Z","observedGeneration":1}],` +
			`"dhcpLease":{"ipAddress":"192.0.2.25","macAddress":"` + selectedPortMAC + `"},` +
			`"jobs":[{"task":"query_dhcp_lease","manager":"fabric","attempt":1,` +
			`"state":"Succeeded","jobID":"job-kind-it"}]}}`
		naPatch := exec.Command("kubectl", "patch", "networkattachment", attachmentName,
			"-n", namespace, "--type=merge", "--subresource=status", "-p", naStatus)
		naOutput, naErr := naPatch.CombinedOutput()
		Expect(naErr).NotTo(HaveOccurred(), string(naOutput))

		By("simulating Metal3 completing the handoff power-on")
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalhost", hostName, namespace, "{.spec.online}")
		}).Should(Equal("true"))
		patchBareMetalHostStatus(hostName, namespace, `{"status":{"poweredOn":true}}`)

		By("waiting for BMF to expose the tenant lease")
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", id, namespace,
				"{.status.networkAttachmentStatuses[0].ipAddress}")
		}).Should(Equal("192.0.2.25"))
		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", id, namespace, "{.status.phase}")
		}).Should(Equal("Ready"))
	})
})

func createNetworkBareMetalInstanceYAML(name, ns, hostType string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/baremetalinstance-uuid: %s
  annotations:
    osac.openshift.io/tenant: tenant-kind-it
spec:
  selector:
    hostSelector:
      hostType: %s
  externalHostID: ""
  templateID: noop
  runStrategy: Always
  networkAttachments:
    - subnetRef: %s
      securityGroupRefs:
        - sg-kind-it
      interface: data-0
      primary: true
`, name, ns, name, hostType, networkAttachmentSubnet)
	return strings.NewReader(yaml)
}
