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

	"github.com/osac-project/osac-operator/test/utils"
)

var _ = Describe("Networking Resources", Ordered, func() {
	AfterAll(func() {
		By("cleaning up test resources")
		cmd := exec.Command("kubectl", "delete", "subnet", "--all", "-n", operatorNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		cmd = exec.Command("kubectl", "delete", "virtualnetwork", "--all", "-n", operatorNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	Context("VirtualNetwork", func() {
		const (
			virtualNetworkName = "test-vnet"
		)

		It("should create a VirtualNetwork successfully", func() {
			By("creating a VirtualNetwork")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createVirtualNetworkYAML(
				virtualNetworkName, operatorNamespace, "cudn-net", "us-west-1", "10.0.0.0/16", "cudn-net")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying VirtualNetwork exists")
			verifyResourceExists := func() error {
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
			}
			Eventually(verifyResourceExists, 30*time.Second, time.Second).Should(Succeed())
		})

		It("should have correct spec fields", func() {
			By("verifying region")
			cmd := exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.region}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("us-west-1"))

			By("verifying IPv4 CIDR")
			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.ipv4Cidr}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("10.0.0.0/16"))

			By("verifying networkClass reference")
			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.networkClass}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("cudn-net"))

			By("verifying implementationStrategy")
			cmd = exec.Command("kubectl", "get", "virtualnetwork", virtualNetworkName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.implementationStrategy}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("cudn-net"))
		})

		It("should be listable with shortname", func() {
			By("listing VirtualNetworks using shortname 'vnet'")
			cmd := exec.Command("kubectl", "get", "vnet", "-n", operatorNamespace)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring(virtualNetworkName))
		})

		It("should have a finalizer added by controller", func() {
			Skip("Skipping finalizer test - requires controller to be running")
			By("checking for finalizer")
			verifyFinalizer := func() error {
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
			}
			Eventually(verifyFinalizer, 60*time.Second, time.Second).Should(Succeed())
		})
	})

	Context("Subnet", func() {
		const (
			virtualNetworkName = "test-vnet"
			subnetName         = "test-subnet"
		)

		It("should create a Subnet successfully", func() {
			By("creating a Subnet")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = createSubnetYAML(subnetName, operatorNamespace, virtualNetworkName, "10.0.1.0/24")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying Subnet exists")
			verifyResourceExists := func() error {
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
			}
			Eventually(verifyResourceExists, 30*time.Second, time.Second).Should(Succeed())
		})

		It("should have correct spec fields", func() {
			By("verifying virtualNetwork reference")
			cmd := exec.Command("kubectl", "get", "subnet", subnetName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.virtualNetwork}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal(virtualNetworkName))

			By("verifying IPv4 CIDR")
			cmd = exec.Command("kubectl", "get", "subnet", subnetName,
				"-n", operatorNamespace, "-o", "jsonpath={.spec.ipv4Cidr}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("10.0.1.0/24"))
		})

		It("should be listable with shortname", func() {
			By("listing Subnets using shortname 'subnet'")
			cmd := exec.Command("kubectl", "get", "subnet", "-n", operatorNamespace)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring(subnetName))
		})

		It("should have a finalizer added by controller", func() {
			Skip("Skipping finalizer test - requires controller to be running")
			By("checking for finalizer")
			verifyFinalizer := func() error {
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
			}
			Eventually(verifyFinalizer, 60*time.Second, time.Second).Should(Succeed())
		})
	})

	Context("Resource Deletion", func() {
		It("should delete Subnet successfully", func() {
			By("deleting the Subnet")
			cmd := exec.Command("kubectl", "delete", "subnet", "test-subnet",
				"-n", operatorNamespace, "--timeout=60s")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying Subnet is deleted")
			verifyDeleted := func() error {
				cmd := exec.Command("kubectl", "get", "subnet", "test-subnet",
					"-n", operatorNamespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("Subnet still exists")
				}
				return nil
			}
			Eventually(verifyDeleted, 60*time.Second, time.Second).Should(Succeed())
		})

		It("should delete VirtualNetwork successfully", func() {
			By("deleting the VirtualNetwork")
			cmd := exec.Command("kubectl", "delete", "virtualnetwork", "test-vnet",
				"-n", operatorNamespace, "--timeout=60s")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying VirtualNetwork is deleted")
			verifyDeleted := func() error {
				cmd := exec.Command("kubectl", "get", "virtualnetwork", "test-vnet",
					"-n", operatorNamespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("VirtualNetwork still exists")
				}
				return nil
			}
			Eventually(verifyDeleted, 60*time.Second, time.Second).Should(Succeed())
		})

	})
})

// createVirtualNetworkYAML returns a Reader with VirtualNetwork YAML
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

// createSubnetYAML returns a Reader with Subnet YAML
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
