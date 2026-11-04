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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/utils"
)

var _ = Describe("BMaaS networking deployment contract", func() {
	It("wires enabled networking and scoped NetworkAttachment access", func() {
		cmd := exec.Command("kubectl", "get", "deployment", "bmf-operator-controller-manager",
			"-n", namespace, "-o", "json")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), output)

		var deployment appsv1.Deployment
		Expect(json.Unmarshal([]byte(output), &deployment)).To(Succeed())
		Expect(deployment.Spec.Template.Spec.Containers).NotTo(BeEmpty())
		env := make(map[string]corev1.EnvVar)
		for _, variable := range deployment.Spec.Template.Spec.Containers[0].Env {
			env[variable.Name] = variable
		}
		Expect(env["OSAC_ENABLE_NETWORKING_PROVISIONING"].Value).To(Equal("true"))
		Expect(env).NotTo(HaveKey("OSAC_NETWORKING_NAMESPACE"))

		serviceAccount := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, serviceAccountName)
		for _, verb := range []string{"create", "get", "patch", "delete"} {
			cmd = exec.Command("kubectl", "auth", "can-i", verb,
				"networkattachments.osac.openshift.io", "--as", serviceAccount, "-n", namespace)
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), output)
			Expect(strings.TrimSpace(output)).To(Equal("yes"), "expected %s access", verb)
		}
		for _, verb := range []string{"list", "watch", "update"} {
			cmd = exec.Command("kubectl", "auth", "can-i", verb,
				"networkattachments.osac.openshift.io", "--as", serviceAccount, "-n", namespace)
			deniedOutput, err := cmd.CombinedOutput()
			Expect(err).To(HaveOccurred(), "expected %s access to be denied", verb)
			Expect(strings.TrimSpace(string(deniedOutput))).To(Equal("no"), "unexpected %s access", verb)
		}
	})

	It("validates the NetworkAttachment MAC status through the installed CRD", func() {
		name := fmt.Sprintf("pr7091-mac-status-%d", time.Now().UnixNano())
		cmd := exec.Command("kubectl", "create", "-f", "-")
		cmd.Stdin = strings.NewReader(fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: default
spec:
  externalHostID: test-host
  selector:
    hostSelector:
      test: pr7091
  templateID: pr7091
`, name))
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), output)
		DeferCleanup(func() {
			cleanup := exec.Command("kubectl", "delete", "baremetalinstance", name,
				"-n", "default", "--ignore-not-found", "--wait=true")
			_, _ = utils.Run(cleanup)
		})

		cmd = exec.Command("kubectl", "patch", "baremetalinstance", name, "-n", "default",
			"--subresource=status", "--type=merge",
			"-p", `{"status":{"networkAttachmentStatuses":[{"macAddress":"aa:bb:cc:dd:ee:ff"}]}}`)
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), output)

		cmd = exec.Command("kubectl", "patch", "baremetalinstance", name, "-n", "default",
			"--subresource=status", "--type=merge",
			"-p", `{"status":{"networkAttachmentStatuses":[{"macAddress":"not-a-mac"}]}}`)
		output, err = utils.Run(cmd)
		Expect(err).To(HaveOccurred(), "the installed CRD should reject an invalid MAC")
		Expect(output).To(ContainSubstring("macAddress"))
	})
})
