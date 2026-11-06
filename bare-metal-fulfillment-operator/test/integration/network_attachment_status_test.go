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
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("BareMetalInstance network attachment status schema", func() {
	It("accepts a normalized selected-port MAC and rejects malformed values", func() {
		name := fmt.Sprintf("bmi-mac-status-%d", time.Now().UnixNano())
		manifest := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/tenant: %s
    osac.openshift.io/owner-reference: %s
spec:
  selector:
    hostSelector:
      hostType: %s
  externalHostID: ""
  templateID: noop
  networkAttachments:
    - subnetRef: status-schema-test
      interface: data-0
      primary: true
`, name, namespace, name, name, name)
		cmd := exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		DeferCleanup(func() { cleanupBMFDelete("BareMetalInstance", name) })

		validStatus := `{"status":{"networkAttachmentStatuses":[{"interface":"data-0","macAddress":"52:54:00:aa:bb:cc"}]}}`
		cmd = exec.Command("kubectl", "patch", "baremetalinstance", name,
			"-n", namespace, "--type=merge", "--subresource=status", "-p", validStatus)
		output, err = cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))

		Eventually(func() (string, error) {
			return kubectlJSONPath("baremetalinstance", name, namespace,
				"{.status.networkAttachmentStatuses[0].macAddress}")
		}, time.Minute, time.Second).Should(Equal("52:54:00:aa:bb:cc"))

		invalidStatus := `{"status":{"networkAttachmentStatuses":[{"interface":"data-0","macAddress":"not-a-mac"}]}}`
		cmd = exec.Command("kubectl", "patch", "baremetalinstance", name,
			"-n", namespace, "--type=merge", "--subresource=status", "-p", invalidStatus)
		output, err = cmd.CombinedOutput()
		Expect(err).To(HaveOccurred(), string(output))
		Expect(string(output)).To(ContainSubstring("should match"))
	})
})
