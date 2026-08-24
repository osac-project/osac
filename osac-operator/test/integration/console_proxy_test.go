/*
Copyright 2025.

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

	"github.com/osac-project/osac/osac-operator/test/utils"
)

var _ = Describe("Console Proxy", Ordered, func() {
	BeforeAll(func() {
		By("waiting for the console-proxy deployment to be ready")
		verifyDeployment := func() error {
			cmd := exec.Command("kubectl", "rollout", "status",
				"deployment/osac-operator-console-proxy",
				"-n", operatorNamespace,
				"--timeout=10s",
			)
			_, err := utils.Run(cmd)
			return err
		}
		Eventually(verifyDeployment, 2*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should register the APIService", func() {
		verifyAPIService := func() error {
			cmd := exec.Command("kubectl", "get", "apiservice",
				"v1alpha1.console.osac.openshift.io",
				"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}",
			)
			output, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			if string(output) != "True" {
				return fmt.Errorf("APIService not available, status: %s", output)
			}
			return nil
		}
		Eventually(verifyAPIService, 2*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should serve API discovery", func() {
		cmd := exec.Command("kubectl", "get", "--raw", "/apis/console.osac.openshift.io")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("console.osac.openshift.io"))
	})

	It("should serve API resource list", func() {
		cmd := exec.Command("kubectl", "get", "--raw", "/apis/console.osac.openshift.io/v1alpha1")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("computeinstances/console"))
		Expect(string(output)).To(ContainSubstring("computeinstances/vnc"))
	})

	DescribeTable("should return an error for a nonexistent compute instance",
		func(subresource string) {
			path := fmt.Sprintf(
				"/apis/console.osac.openshift.io/v1alpha1/namespaces/default/computeinstances/nonexistent/%s",
				subresource,
			)
			cmd := exec.Command("kubectl", "get", "--raw", path)
			_, err := utils.Run(cmd)
			Expect(err).To(HaveOccurred())
		},
		Entry("console", "console"),
		Entry("vnc", "vnc"),
	)

	It("should return an error for a compute instance without VM reference", func() {
		By("creating a ComputeInstance without a VM reference")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = createComputeInstanceYAML("test-ci-no-vm", operatorNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("verifying it's listable via the 'ci' shortname")
		cmd = exec.Command("kubectl", "get", "ci", "test-ci-no-vm", "-n", operatorNamespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("attempting console access")
		cmd = exec.Command("kubectl", "get", "--raw",
			fmt.Sprintf("/apis/console.osac.openshift.io/v1alpha1/namespaces/%s/computeinstances/test-ci-no-vm/console",
				operatorNamespace),
		)
		_, err = utils.Run(cmd)
		Expect(err).To(HaveOccurred())

		By("attempting VNC access")
		cmd = exec.Command("kubectl", "get", "--raw",
			fmt.Sprintf("/apis/console.osac.openshift.io/v1alpha1/namespaces/%s/computeinstances/test-ci-no-vm/vnc",
				operatorNamespace),
		)
		_, err = utils.Run(cmd)
		Expect(err).To(HaveOccurred())

		By("cleaning up the ComputeInstance")
		removeFinalizers("computeinstance", "test-ci-no-vm", operatorNamespace)
		cmd = exec.Command("kubectl", "delete", "computeinstance", "test-ci-no-vm",
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
		_, _ = utils.Run(cmd)
		removeFinalizers("computeinstance", "test-ci-no-vm", operatorNamespace)
	})
})

func createComputeInstanceYAML(name, namespace string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ComputeInstance
metadata:
  name: %s
  namespace: %s
spec:
  templateID: test_template
  image:
    sourceType: registry
    sourceRef: quay.io/fedora/fedora-coreos:stable
  cores: 2
  memoryGiB: 4
  bootDisk:
    sizeGiB: 20
    storageTier: standard
  runStrategy: Always
`, name, namespace)
	return strings.NewReader(yaml)
}
