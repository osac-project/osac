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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("VMaaS split-cluster reconciliation", Ordered, func() {
	// testCIName includes the per-run ID so that parallel or repeated runs
	// do not collide on fixed resource names.
	testCIName := "mc-test-" + testRunID

	// testTenantName is the Tenant CR name.
	testTenantName := operatorNamespace

	AfterAll(func() {
		labelSelector := testRunLabelKey + "=" + testRunID

		// Capture diagnostics BEFORE deleting resources so that the
		// ComputeInstance status and AWX job stdout are available in
		// CI output. In Ginkgo Ordered containers, DeferCleanup from
		// an It runs after AfterAll, so diagnostics registered there
		// would find the ComputeInstance already gone.
		By("capturing provisioning diagnostics before cleanup")
		dumpProvisioningDiagnostics(testCIName, operatorNamespace)

		By("cleaning up ComputeInstance")
		removeFinalizersByLabel("computeinstance", operatorNamespace, labelSelector)
		if out, err := runKubectlHub("delete", "computeinstance", "-l", labelSelector,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found"); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: delete computeinstance failed: %v\n%s\n", err, out)
		}
		removeFinalizersByLabel("computeinstance", operatorNamespace, labelSelector)

		By("cleaning up Subnet")
		removeFinalizersByLabel("subnet", operatorNamespace, labelSelector)
		if out, err := runKubectlHub("delete", "subnet", "-l", labelSelector,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found"); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: delete subnet failed: %v\n%s\n", err, out)
		}

		By("cleaning up Tenant")
		removeFinalizersByLabel("tenant", operatorNamespace, labelSelector)
		if out, err := runKubectlHub("delete", "tenant", "-l", labelSelector,
			"-n", operatorNamespace, "--wait=false", "--ignore-not-found"); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: delete tenant failed: %v\n%s\n", err, out)
		}

		By("cleaning up VirtualMachine on workload cluster")
		if out, err := runKubectlWorkload("delete", "virtualmachines.kubevirt.io",
			testCIName, "-n", testSubnetName, "--ignore-not-found"); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: delete VM on workload cluster failed: %v\n%s\n", err, out)
		}
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
		Expect(strings.TrimSpace(string(out))).To(Equal(
			"/var/run/secrets/remote/kubeconfig"))
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

	It("should have a Subnet for ComputeInstance placement", func() {
		By("creating a Subnet CR on the hub cluster for subnet-target-namespace resolution")
		out, err := createOnlyYAML(
			createSubnetYAML(testSubnetName, operatorNamespace),
			runKubectlHub,
		)
		if err != nil && (strings.Contains(string(out), "AlreadyExists") ||
			strings.Contains(err.Error(), "AlreadyExists")) {
			Fail(fmt.Sprintf(
				"Subnet %q already exists — refusing to adopt: %s",
				testSubnetName, out))
		}
		Expect(err).NotTo(HaveOccurred(), "create Subnet: %s", out)

		By("verifying the Subnet CR exists")
		out, err = runKubectlHub("get", "subnet", testSubnetName,
			"-n", operatorNamespace,
			"-o", "jsonpath={.metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal(testSubnetName))
	})

	It("should have a ready Tenant for the ComputeInstance", func() {
		By("creating a Tenant on the hub cluster (create-only)")
		out, err := createOnlyYAML(
			createTenantYAML(testTenantName, operatorNamespace),
			runKubectlHub,
		)
		if err != nil && (strings.Contains(string(out), "AlreadyExists") ||
			strings.Contains(err.Error(), "AlreadyExists")) {
			Fail(fmt.Sprintf(
				"Tenant %q already exists — refusing to adopt: %s",
				testTenantName, out))
		}
		Expect(err).NotTo(HaveOccurred(), "create Tenant: %s", out)

		// The real tenant controller is enabled (the workload Kind
		// cluster has the fake OVN UserDefinedNetwork CRD installed
		// in BeforeSuite, so the remote informer syncs).  The
		// controller sets Phase=Ready on reconciliation.
		By("waiting for the tenant controller to set Phase=Ready")
		Eventually(func() string {
			out, err := runKubectlHub("get", "tenant", testTenantName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.phase}")
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}, 2*time.Minute, 2*time.Second).Should(Equal("Ready"),
			"the tenant controller should reconcile the Tenant to Ready")
	})

	It("should have OSAC/AAP-created tenant StorageClass on workload cluster", func() {
		// The real storage path: the register-local-storage Helm hook
		// created a "local" StorageBackend+Tier in the fulfillment
		// service.  The storage reconciler detects the Ready Tenant,
		// queries the Tier API for definitions, finds no tenant-labeled
		// StorageClass on the workload cluster, and dispatches the
		// osac-create-tenant-cluster-storage AWX template.  The
		// playbook creates a tenant-labeled StorageClass via the LVMS
		// storage provider role.
		By("waiting for storage reconciler to provision tenant StorageClass via AAP")
		waitForTenantStorageClass(testTenantName)
	})

	It("should reconcile ComputeInstance on the hub cluster", func() {
		By("creating a ComputeInstance on the hub")
		out, err := applyYAML(
			createComputeInstanceYAML(testCIName, operatorNamespace, testTenantName, testSubnetName),
			runKubectlHub,
		)
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

		By("verifying the controller resolved the subnet-target-namespace annotation from the Subnet CR")
		Eventually(func() string {
			out, err := runKubectlHub("get", "computeinstance", testCIName,
				"-n", operatorNamespace,
				"-o", `jsonpath={.metadata.annotations.osac\.openshift\.io/subnet-target-namespace}`)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}, 30*time.Second, 2*time.Second).Should(Equal(testSubnetName),
			"the controller should resolve subnet-target-namespace from the Subnet CR name")
	})

	It("should provision a VirtualMachine on the workload cluster via AAP", func() {
		// Dump AAP/operator diagnostics on failure so CI logs are
		// actionable without re-running or inspecting artifacts.
		DeferCleanup(func() {
			if CurrentSpecReport().Failed() {
				dumpProvisioningDiagnostics(
					testCIName, operatorNamespace)
			}
		})

		By("waiting for AAP to create the VirtualMachine on the workload cluster")
		Eventually(func() error {
			out, err := runKubectlWorkload("get",
				"virtualmachines.kubevirt.io",
				testCIName, "-n", testSubnetName,
				"-o", "jsonpath={.metadata.name}")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != testCIName {
				return fmt.Errorf(
					"VirtualMachine %s not found on workload cluster in namespace %s",
					testCIName, testSubnetName)
			}
			return nil
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			"the operator should trigger AAP provisioning "+
				"which creates the VM on the workload cluster "+
				"in the subnet-derived namespace")

		By("verifying the VirtualMachine has the correct ownership label")
		out, err := runKubectlWorkload("get", "virtualmachines.kubevirt.io",
			testCIName, "-n", testSubnetName,
			"-o", "jsonpath={.metadata.labels.osac\\.openshift\\.io/computeinstance}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal(testCIName),
			"VM should have osac.openshift.io/computeinstance label matching the ComputeInstance")

		By("verifying the VM uses runStrategy Halted (no readiness wait)")
		out, err = runKubectlWorkload("get", "virtualmachines.kubevirt.io",
			testCIName, "-n", testSubnetName,
			"-o", "jsonpath={.spec.runStrategy}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(Equal("Halted"),
			"VM should use runStrategy Halted to avoid readiness wait on fake CRDs")

		By("verifying the operator records a successful provisioning job")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance", testCIName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.provisioningJobs}")
			if err != nil {
				return err
			}
			jobs := strings.TrimSpace(string(out))
			if jobs == "" || jobs == "[]" {
				return fmt.Errorf("no provisioning jobs recorded in status")
			}
			return nil
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying the operator reconciles the VM reference from the workload cluster")
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
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should NOT have the VirtualMachine on the hub cluster", func() {
		// The specific test VM must not exist on the hub regardless of
		// whether the hub has KubeVirt CRDs installed.
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

// createSubnetYAML returns a minimal Subnet CR YAML string.
// The networking controller is disabled in this CI profile
// (operator.controllers.networking=false), so the Subnet is not
// reconciled.  It exists solely so the ComputeInstance controller can
// resolve the subnet-target-namespace annotation via
// resolveSubnetTargetNamespace() — which reads subnet.Name and stamps
// it as the workload-cluster namespace where AAP creates the VM.
//
// Using a test-specific Subnet name (mc-subnet-<runID>) rather than
// operatorNamespace proves that the controller performs real Subnet CR
// resolution instead of falling back to the tenant name.
func createSubnetYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Subnet
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
spec:
  virtualNetwork: integration-test-vnet
  ipv4Cidr: 10.100.0.0/24
`, name, namespace, testRunLabelKey, testRunID)
}

// createTenantYAML returns a minimal Tenant YAML string.
// The real tenant controller is enabled and reconciles this to Ready
// once the remote informer cache has synced (the workload Kind cluster
// has the fake OVN UserDefinedNetwork CRD installed in BeforeSuite).
func createTenantYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Tenant
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
spec: {}
`, name, namespace, testRunLabelKey, testRunID)
}

// createComputeInstanceYAML returns a minimal ComputeInstance YAML string
// that includes the osac.openshift.io/tenant annotation required by
// the ComputeInstance controller to discover VMs on the remote cluster.
//
// networkAttachments[0].subnetRef references a real Subnet CR created
// earlier in the test. The ComputeInstance controller resolves the
// Subnet CR via resolveSubnetTargetNamespace() and stamps the
// osac.openshift.io/subnet-target-namespace annotation automatically.
// This exercises the real subnet resolution path instead of pre-seeding
// the annotation.
//
// runStrategy is set to Halted so the AAP playbook creates the
// VirtualMachine CR without waiting for VM readiness. The playbook's
// wait task (create_wait_annotate.yaml) skips when
// vm_run_strategy != "Halted", allowing the test to exercise real
// operator→AAP→Ansible creation against fake KubeVirt CRDs that
// never produce a running VirtualMachineInstance.
//
// storageTier is set to "local" to match the fulfillment service's
// registered StorageTier (created by the register-local-storage Helm
// hook) and the tenant-labeled StorageClass created by the storage
// reconciler via the osac-create-tenant-cluster-storage AAP template.
func createComputeInstanceYAML(name, namespace, tenantName, subnetRef string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: ComputeInstance
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
  annotations:
    osac.openshift.io/tenant: %s
spec:
  templateID: osac.templates.ocp_virt_vm
  image:
    sourceType: registry
    sourceRef: quay.io/fedora/fedora-coreos:stable
  vcpus: 2
  memoryGiB: 4
  bootDisk:
    sizeGiB: 20
    storageTier: local
  runStrategy: Halted
  networkAttachments:
    - subnetRef: %s
`, name, namespace, testRunLabelKey, testRunID,
		tenantName, subnetRef)
}
