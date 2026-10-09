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

package multicluster

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

var _ = Describe("VMaaS split-cluster reconciliation", Ordered, func() {
	const testCIName = "multicluster-test-ci"

	// testTenantName is the Tenant CR name. The ComputeInstance controller
	// requires an osac.openshift.io/tenant annotation pointing to a Ready
	// Tenant before it will discover VMs on the remote cluster. Using the
	// same value as operatorNamespace means computeInstanceTargetNamespace
	// returns operatorNamespace (since the tenant controller sets
	// status.namespace=""), matching the namespace where the test creates
	// the VirtualMachine on the workload cluster.
	const testTenantName = operatorNamespace

	AfterAll(func() {
		By("cleaning up ComputeInstance")
		removeFinalizers("computeinstance", testCIName, operatorNamespace)
		_, _ = runKubectlHub("delete", "computeinstance", testCIName,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
		removeFinalizers("computeinstance", testCIName, operatorNamespace)

		By("cleaning up Tenant")
		removeFinalizers("tenant", testTenantName, operatorNamespace)
		_, _ = runKubectlHub("delete", "tenant", testTenantName,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found")

		By("cleaning up VirtualMachine on workload cluster")
		_, _ = runKubectlWorkload("delete", "virtualmachines.kubevirt.io",
			testCIName, "-n", operatorNamespace, "--ignore-not-found")
	})

	It("should confirm operator is running with remote cluster kubeconfig", func() {
		out, err := runKubectlHub("get", "pods",
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-n", operatorNamespace,
			"-o", "jsonpath={.items[0].status.phase}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("Running"))

		// Verify the remote cluster kubeconfig env var is set.
		out, err = runKubectlHub("get", "deployment",
			"-l", "app.kubernetes.io/name=operator",
			"-n", operatorNamespace,
			"-o", `jsonpath={.items[0].spec.template.spec.containers[0].env[?(@.name=="OSAC_REMOTE_CLUSTER_KUBECONFIG")].value}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("/etc/osac/kubeconfig"))
	})

	It("should have KubeVirt CRDs on the workload cluster", func() {
		out, err := runKubectlWorkload("get", "crd", "virtualmachines.kubevirt.io",
			"-o", "jsonpath={.metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("virtualmachines.kubevirt.io"))

		out, err = runKubectlWorkload("get", "crd", "virtualmachineinstances.kubevirt.io",
			"-o", "jsonpath={.metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("virtualmachineinstances.kubevirt.io"))
	})

	It("should NOT have KubeVirt CRDs on the hub cluster", func() {
		out, err := runKubectlHub("get", "crd", "virtualmachines.kubevirt.io",
			"--ignore-not-found", "-o", "name")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(BeEmpty(),
			"VirtualMachine CRD should not exist on the hub cluster")
	})

	It("should have a ready Tenant for the ComputeInstance", func() {
		By("creating a Tenant on the hub cluster")
		cmd := kubectlHubCommand("apply", "-f", "-")
		cmd.Stdin = createTenantYAML(testTenantName, operatorNamespace)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "create Tenant: %s", out)

		By("waiting for the Tenant to reach Ready phase")
		Eventually(func() error {
			out, err := runKubectlHub("get", "tenant", testTenantName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.phase}")
			if err != nil {
				return err
			}
			phase := strings.TrimSpace(string(out))
			if phase != "Ready" {
				return fmt.Errorf("tenant phase is %q, want Ready", phase)
			}
			return nil
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should reconcile ComputeInstance on the hub cluster", func() {
		By("creating a ComputeInstance on the hub")
		cmd := kubectlHubCommand("apply", "-f", "-")
		cmd.Stdin = createComputeInstanceYAML(
			testCIName, operatorNamespace, testTenantName,
		)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "create ComputeInstance: %s", out)

		By("verifying the controller adds a finalizer to the ComputeInstance")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance", testCIName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.metadata.finalizers}")
			if err != nil {
				return err
			}
			if len(strings.TrimSpace(string(out))) == 0 {
				return fmt.Errorf("no finalizers found on ComputeInstance")
			}
			return nil
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should create a VirtualMachine on the workload cluster (simulating AAP)", func() {
		By("ensuring the target namespace exists on the workload cluster")
		nsCmd := kubectlWorkloadCommand("create", "namespace", operatorNamespace)
		_, _ = nsCmd.CombinedOutput() // ignore already-exists error

		By("creating a labeled VirtualMachine on the workload cluster")
		vmCmd := kubectlWorkloadCommand("apply", "-f", "-")
		vmCmd.Stdin = createVirtualMachineYAML(testCIName, operatorNamespace)
		out, err := vmCmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "create VM on workload: %s", out)

		By("verifying the VirtualMachine exists on the workload cluster")
		Eventually(func() error {
			out, err := runKubectlWorkload("get", "virtualmachines.kubevirt.io",
				testCIName, "-n", operatorNamespace,
				"-o", "jsonpath={.metadata.name}")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != testCIName {
				return fmt.Errorf("VirtualMachine %s not found on workload cluster", testCIName)
			}
			return nil
		}, 30*time.Second, time.Second).Should(Succeed())

		By("verifying the operator observes the VM via status.virtualMachineReference")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance", testCIName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.virtualMachineReference.kubeVirtVirtualMachineName}")
			if err != nil {
				return err
			}
			vmName := strings.TrimSpace(string(out))
			if vmName != testCIName {
				return fmt.Errorf("expected virtualMachineReference.kubeVirtVirtualMachineName=%q, got %q",
					testCIName, vmName)
			}
			return nil
		}, 2*time.Minute, 2*time.Second).Should(Succeed())
	})

	It("should NOT have the VirtualMachine on the hub cluster", func() {
		// The hub cluster does not have the VirtualMachine CRD, so this
		// kubectl command should fail or return empty.
		out, err := runKubectlHub("get", "virtualmachines.kubevirt.io",
			testCIName, "-n", operatorNamespace, "--ignore-not-found")
		if err == nil {
			Expect(strings.TrimSpace(string(out))).To(BeEmpty(),
				"VirtualMachine should not exist on the hub cluster")
		}
		// If the command errors (CRD not found), that also proves the
		// VirtualMachine is not on the hub.
	})
})

// createTenantYAML returns a reader with a minimal Tenant YAML.
// The Tenant controller sets status.phase=Ready automatically.
func createTenantYAML(name, namespace string) *strings.Reader {
	y := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Tenant
metadata:
  name: %s
  namespace: %s
spec: {}
`, name, namespace)
	return strings.NewReader(y)
}

// createComputeInstanceYAML returns a reader with a minimal ComputeInstance
// YAML that includes the osac.openshift.io/tenant annotation required by
// the ComputeInstance controller to discover VMs on the remote cluster.
func createComputeInstanceYAML(
	name, namespace, tenantName string,
) *strings.Reader {
	y := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ComputeInstance
metadata:
  name: %s
  namespace: %s
  annotations:
    osac.openshift.io/tenant: %s
spec:
  templateID: test_template
  image:
    sourceType: registry
    sourceRef: quay.io/fedora/fedora-coreos:stable
  vcpus: 2
  memoryGiB: 4
  bootDisk:
    sizeGiB: 20
    storageTier: standard
  runStrategy: Always
`, name, namespace, tenantName)
	return strings.NewReader(y)
}

// createVirtualMachineYAML returns a reader with a minimal VirtualMachine YAML
// labeled so the operator's mapObjectToComputeInstance can discover it.
func createVirtualMachineYAML(name, namespace string) *strings.Reader {
	yaml := fmt.Sprintf(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/computeinstance: %s
spec:
  running: false
`, name, namespace, name)
	return strings.NewReader(yaml)
}

// kubectlHubCommand creates an exec.Command for kubectl targeting the hub cluster.
func kubectlHubCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("kubectl", args...)
	cmd.Dir, _ = utils.GetProjectDir()
	return cmd
}

// kubectlWorkloadCommand creates an exec.Command for kubectl targeting the workload cluster.
func kubectlWorkloadCommand(args ...string) *exec.Cmd {
	fullArgs := append([]string{"--kubeconfig", workloadHostKubeconfig}, args...)
	cmd := exec.Command("kubectl", fullArgs...)
	cmd.Dir, _ = utils.GetProjectDir()
	return cmd
}
