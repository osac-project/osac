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

func removeFinalizers(kind, name, namespace string) {
	cmd := exec.Command("kubectl", "patch", kind, name,
		"-n", namespace, "--type=merge",
		"-p", `{"metadata":{"finalizers":[]}}`)
	_, _ = utils.Run(cmd)
}

func removeAllFinalizers(kind, namespace string) {
	cmd := exec.Command("kubectl", "get", kind, "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	output, err := utils.Run(cmd)
	if err != nil {
		return
	}
	for name := range strings.SplitSeq(string(output), " ") {
		if name != "" {
			removeFinalizers(kind, name, namespace)
		}
	}
}

var _ = Describe("Networking Resources", Ordered, func() {
	AfterAll(func() {
		By("cleaning up test resources")
		removeAllFinalizers("subnet", operatorNamespace)
		cmd := exec.Command("kubectl", "delete", "subnet", "--all", "-n", operatorNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		removeAllFinalizers("virtualnetwork", operatorNamespace)
		cmd = exec.Command("kubectl", "delete", "virtualnetwork", "--all", "-n", operatorNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	Context("VirtualNetwork", func() {
		const virtualNetworkName = "test-vnet"

		It("should create a VirtualNetwork successfully", func() {
			By("creating a VirtualNetwork")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createVirtualNetworkYAML(
				virtualNetworkName, operatorNamespace, "cudn-net", "us-west-1", "10.0.0.0/16", "cudn-strategy")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying VirtualNetwork exists")
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
					"-n", operatorNamespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != virtualNetworkName {
					return fmt.Errorf("expected VirtualNetwork name %s, got %s", virtualNetworkName, string(output))
				}
				return nil
			}, 30*time.Second, time.Second).Should(Succeed())
		})

		It("should have correct spec fields", func() {
			cmd := exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.region}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("us-west-1"))

			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.ipv4Cidr}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("10.0.0.0/16"))

			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.networkClass}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("cudn-net"))

			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.implementationStrategy}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("cudn-strategy"))
		})

		It("should be listable with shortname", func() {
			cmd := exec.Command("kubectl", "get", "vnet", "-n", operatorNamespace)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring(virtualNetworkName))
		})

		It("should have a finalizer added by controller", func() {
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
					"-n", operatorNamespace, "-o", "jsonpath={.metadata.finalizers}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if len(string(output)) == 0 {
					return fmt.Errorf("no finalizers found on VirtualNetwork")
				}
				return nil
			}, 60*time.Second, time.Second).Should(Succeed())
		})
	})

	Context("Subnet", func() {
		const (
			virtualNetworkName = "test-vnet"
			subnetName         = "test-subnet"
		)

		It("should create a Subnet successfully", func() {
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createSubnetYAML(subnetName, operatorNamespace, virtualNetworkName, "10.0.1.0/24")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "subnet", subnetName,
					"-n", operatorNamespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != subnetName {
					return fmt.Errorf("expected Subnet name %s, got %s", subnetName, string(output))
				}
				return nil
			}, 30*time.Second, time.Second).Should(Succeed())
		})

		It("should have correct spec fields", func() {
			cmd := exec.Command("kubectl", "get", "subnet", subnetName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.virtualNetwork}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal(virtualNetworkName))

			cmd = exec.Command("kubectl", "get", "subnet", subnetName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.ipv4Cidr}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("10.0.1.0/24"))
		})

		It("should be listable with shortname", func() {
			cmd := exec.Command("kubectl", "get", "subnet", "-n", operatorNamespace)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring(subnetName))
		})

		It("should have a finalizer added by controller", func() {
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "subnet", subnetName,
					"-n", operatorNamespace, "-o", "jsonpath={.metadata.finalizers}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if len(string(output)) == 0 {
					return fmt.Errorf("no finalizers found on Subnet")
				}
				return nil
			}, 60*time.Second, time.Second).Should(Succeed())
		})
	})

	Context("Resource Deletion", func() {
		It("should delete Subnet successfully", func() {
			By("initiating deletion (non-blocking)")
			cmd := exec.Command("kubectl", "delete", "subnet", "test-subnet",
				"-n", operatorNamespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("removing finalizers so deletion can complete without AAP")
			removeFinalizers("subnet", "test-subnet", operatorNamespace)

			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "subnet", "test-subnet", "-n", operatorNamespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("Subnet still exists")
				}
				return nil
			}, 60*time.Second, time.Second).Should(Succeed())
		})

		It("should delete VirtualNetwork successfully", func() {
			By("initiating deletion (non-blocking)")
			cmd := exec.Command("kubectl", "delete", "virtualnetwork", "test-vnet",
				"-n", operatorNamespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("removing finalizers so deletion can complete without AAP")
			removeFinalizers("virtualnetwork", "test-vnet", operatorNamespace)

			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "virtualnetwork", "test-vnet", "-n", operatorNamespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("VirtualNetwork still exists")
				}
				return nil
			}, 60*time.Second, time.Second).Should(Succeed())
		})
	})
})

func createVirtualNetworkYAML(name, namespace, networkClass, region, ipv4CIDR, implStrategy string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: VirtualNetwork
metadata:
  name: %s
  namespace: %s
spec:
  region: %s
  ipv4Cidr: %s
  networkClass: %s
  implementationStrategy: %s
`, name, namespace, region, ipv4CIDR, networkClass, implStrategy)
	return strings.NewReader(yaml)
}

func createSubnetYAML(name, namespace, virtualNetwork, ipv4CIDR string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Subnet
metadata:
  name: %s
  namespace: %s
spec:
  virtualNetwork: %s
  ipv4Cidr: %s
`, name, namespace, virtualNetwork, ipv4CIDR)
	return strings.NewReader(yaml)
}
