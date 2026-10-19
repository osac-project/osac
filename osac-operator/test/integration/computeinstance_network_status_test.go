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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

var _ = Describe("ComputeInstance network attachment status", func() {
	const computeInstanceName = "test-ci-network-status"

	AfterEach(func() {
		removeFinalizers("computeinstance", computeInstanceName, operatorNamespace)
		cmd := exec.Command("kubectl", "delete", "computeinstance", computeInstanceName,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
		_, _ = utils.Run(cmd)
		removeFinalizers("computeinstance", computeInstanceName, operatorNamespace)
	})

	It("persists the reported Subnet ID and IP through the installed status subresource", func() {
		By("creating a ComputeInstance through the installed CRD")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = createComputeInstanceYAML(computeInstanceName, operatorNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("writing one VMaaS attachment status through the Kubernetes status endpoint")
		cmd = exec.Command("kubectl", "patch", "computeinstance", computeInstanceName,
			"-n", operatorNamespace, "--subresource=status", "--type=merge",
			"-p", `{"status":{"computeNetworkAttachmentStatuses":[{"subnetRef":"subnet-id-123","ipAddress":"10.0.0.42"}]}}`)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("verifying the installed CRD retains both values in status")
		Eventually(func() error {
			cmd := exec.Command("kubectl", "get", "computeinstance", computeInstanceName,
				"-n", operatorNamespace, "-o", "json")
			output, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			var resource struct {
				Status struct {
					ComputeNetworkAttachmentStatuses []struct {
						SubnetRef string `json:"subnetRef"`
						IPAddress string `json:"ipAddress"`
					} `json:"computeNetworkAttachmentStatuses"`
				} `json:"status"`
			}
			if err := json.Unmarshal(output, &resource); err != nil {
				return fmt.Errorf("decode ComputeInstance status: %w", err)
			}
			statuses := resource.Status.ComputeNetworkAttachmentStatuses
			if len(statuses) != 1 || statuses[0].SubnetRef != "subnet-id-123" || statuses[0].IPAddress != "10.0.0.42" {
				return fmt.Errorf("unexpected network attachment statuses: %+v", statuses)
			}
			return nil
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})
