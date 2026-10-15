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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

var _ = Describe("VMaaS split-cluster reconciliation", Ordered, func() {
	// testCIName is the metadata.name sent in the ComputeInstances/Create
	// request.  Note: the fulfillment reconciler creates the K8s CR with
	// GenerateName: "vm-", so the actual CR name will differ.
	testCIName := "mc-test-" + testRunID

	// testTenantName is unique per test run so that parallel or
	// repeated runs do not collide with each other or with the real
	// "osac" namespace.
	testTenantName := "mc-tenant-" + testRunID

	// onboardedSubnetName is resolved from the auto-provisioned Subnet
	// CR after tenant onboarding completes.  Set by the subnet
	// discovery step and used by subsequent assertions.
	var onboardedSubnetName string

	// tenantID is the fulfillment-assigned ID returned by the
	// Tenants/Create gRPC API.  Used for API-based delete.
	var tenantID string

	// tenantCreated is set after the Tenants/Create gRPC call
	// succeeds.  All cleanup is gated on this flag to avoid
	// adopting or deleting pre-existing resources.
	var tenantCreated bool

	// computeInstanceID is the fulfillment-assigned ID returned by the
	// ComputeInstances/Create gRPC API.  Used for API-based delete.
	var computeInstanceID string

	// actualCRName is the K8s CR name of the ComputeInstance on the hub.
	// The fulfillment reconciler uses GenerateName: "vm-", so this
	// differs from testCIName. Discovered by label query after creation.
	var actualCRName string

	// computeInstanceCreated is set after the ComputeInstances/Create
	// gRPC call succeeds.  Cleanup is gated on this flag.
	var computeInstanceCreated bool

	AfterAll(func() {
		labelSelector := testRunLabelKey + "=" + testRunID

		// Capture diagnostics BEFORE deleting resources so that the
		// ComputeInstance status and AWX job stdout are available in
		// CI output. In Ginkgo Ordered containers, DeferCleanup from
		// an It runs after AfterAll, so diagnostics registered there
		// would find the ComputeInstance already gone.
		By("capturing provisioning diagnostics before cleanup")
		dumpProvisioningDiagnostics(testCIName, operatorNamespace)

		// --- ComputeInstance cleanup (gated on positive creation) ---
		if computeInstanceCreated {
			By("cleaning up ComputeInstance via fulfillment gRPC API")
			deleteComputeInstanceViaAPI(
				computeInstanceID, operatorNamespace)

			// Poll up to 30s for the CR to be deleted.  On
			// timeout, fall through to emergency cleanup.
			By("waiting for ComputeInstance CR deletion")
			ciGone := false
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				var checkName string
				if actualCRName != "" {
					checkName = actualCRName
				}
				var out []byte
				var err error
				if checkName != "" {
					out, err = runKubectlHub("get",
						"computeinstance", checkName,
						"-n", operatorNamespace,
						"--ignore-not-found",
						"-o", "name")
				} else {
					out, err = runKubectlHub("get",
						"computeinstance",
						"-l", labelSelector,
						"-n", operatorNamespace,
						"-o",
						"jsonpath={.items[*].metadata.name}")
				}
				if err == nil &&
					strings.TrimSpace(string(out)) == "" {
					ciGone = true
					break
				}
				time.Sleep(2 * time.Second)
			}

			if !ciGone {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"WARNING: ComputeInstance CR not "+
						"deleted after 30s; running "+
						"emergency cleanup\n")
				removeFinalizersByLabel("computeinstance",
					operatorNamespace, labelSelector)
				if out, err := runKubectlHub("delete",
					"computeinstance", "-l", labelSelector,
					"-n", operatorNamespace,
					"--wait=false",
					"--ignore-not-found"); err != nil {
					_, _ = fmt.Fprintf(GinkgoWriter,
						"WARNING: delete computeinstance "+
							"failed: %v\n%s\n", err, out)
				}
			}
		}

		// --- Tenant cleanup (gated on positive creation) ---
		if tenantCreated {
			By("deleting tenant via fulfillment gRPC API")
			deleteTenantViaAPI(tenantID, operatorNamespace)

			// Poll up to 30s for the Tenant CR to be removed.
			// On timeout, fall through to emergency cleanup
			// instead of failing the test.
			By("waiting for Tenant CR deletion")
			tenantGone := false
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				out, err := runKubectlHub("get",
					"tenant", testTenantName,
					"-n", operatorNamespace,
					"--ignore-not-found", "-o", "name")
				if err == nil &&
					strings.TrimSpace(string(out)) == "" {
					tenantGone = true
					break
				}
				time.Sleep(2 * time.Second)
			}

			if !tenantGone {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"WARNING: Tenant CR %s not deleted "+
						"after 30s; running emergency "+
						"cleanup\n", testTenantName)
				for _, kind := range []string{
					"subnet", "virtualnetwork",
					"securitygroup", "tenant",
				} {
					removeFinalizersByLabel(kind,
						operatorNamespace, labelSelector)
					if out, err := runKubectlHub("delete",
						kind, "-l", labelSelector,
						"-n", operatorNamespace,
						"--wait=false",
						"--ignore-not-found"); err != nil {
						_, _ = fmt.Fprintf(GinkgoWriter,
							"WARNING: delete %s failed: "+
								"%v\n%s\n", kind, err, out)
					}
				}
			}
		}

		// --- Workload-cluster VM cleanup (gated on creation) ---
		// AAP names the VM after the K8s CR name (actualCRName), not
		// the API metadata.name (testCIName).
		if computeInstanceCreated && onboardedSubnetName != "" &&
			actualCRName != "" {
			By("cleaning up VirtualMachine on workload cluster")
			if out, err := runKubectlWorkload("delete",
				"virtualmachines.kubevirt.io",
				actualCRName, "-n", onboardedSubnetName,
				"--ignore-not-found"); err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"WARNING: delete VM on workload cluster "+
						"failed: %v\n%s\n", err, out)
			}
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
		out, err := runKubectlWorkload("get", "crd",
			"virtualmachines.kubevirt.io",
			"-o", "jsonpath={.metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(
			Equal("virtualmachines.kubevirt.io"))

		out, err = runKubectlWorkload("get", "crd",
			"virtualmachineinstances.kubevirt.io",
			"-o", "jsonpath={.metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(
			Equal("virtualmachineinstances.kubevirt.io"))
	})

	It("should create a tenant via the fulfillment gRPC API", func() {
		// Create the DB tenant via the private gRPC Tenants API,
		// using runFulfillmentGRPC (initContainer token pattern).
		// The token never appears in pod logs, Go memory, or
		// assertions.  AlreadyExists is treated as failure (the
		// unique testTenantName prevents collisions).
		By("calling Tenants/Create via grpcurl pod")
		tenantID = createTenantViaAPI(
			testTenantName, operatorNamespace)
		tenantCreated = true
	})

	It("should have an auto-provisioned Subnet CR from tenant onboarding", func() {
		// The fulfillment onboarding reconciler creates a Tenant CR
		// on the hub and auto-provisions default VirtualNetwork,
		// Subnet, and SecurityGroup.  Wait for the Subnet CR to
		// appear with the tenant annotation.
		//
		// Use JSON output and parse in Go for reliable selection
		// of the single subnet owned by this tenant.
		By("waiting for auto-provisioned Subnet CR to appear")
		Eventually(func() (string, error) {
			name, err := findSubnetByTenantAnnotation(
				operatorNamespace, testTenantName)
			if err != nil {
				return "", err
			}
			return name, nil
		}, 5*time.Minute, 5*time.Second).ShouldNot(BeEmpty(),
			"onboarding should auto-provision a Subnet CR "+
				"annotated with tenant %q", testTenantName)

		// Resolve the auto-provisioned Subnet name for later steps.
		var err error
		onboardedSubnetName, err = findSubnetByTenantAnnotation(
			operatorNamespace, testTenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(onboardedSubnetName).NotTo(BeEmpty(),
			"at least one Subnet CR annotated with tenant %q"+
				" must exist", testTenantName)
		_, _ = fmt.Fprintf(GinkgoWriter,
			"resolved auto-provisioned Subnet: %s\n",
			onboardedSubnetName)

		// Label the auto-provisioned resources with the test run ID
		// so that emergency cleanup in AfterAll can find them.
		// Use the subnet label to find networking resources owned
		// by this tenant.
		for _, kind := range []string{
			"subnet", "virtualnetwork", "securitygroup",
		} {
			out, err := runKubectlHub("get", kind,
				"-n", operatorNamespace,
				"-o", "jsonpath={.items[*].metadata.name}")
			if err != nil {
				continue
			}
			for name := range strings.SplitSeq(
				strings.TrimSpace(string(out)), " ") {
				if name == "" {
					continue
				}
				// Check tenant annotation on each resource.
				ann, _ := runKubectlHub("get", kind, name,
					"-n", operatorNamespace,
					"-o",
					`jsonpath={.metadata.annotations.osac\.openshift\.io/tenant}`)
				if strings.TrimSpace(string(ann)) ==
					testTenantName {
					_, _ = runKubectlHub("label",
						kind, name,
						"-n", operatorNamespace,
						testRunLabelKey+"="+testRunID,
						"--overwrite")
				}
			}
		}
		// Also label the Tenant CR itself.
		_, _ = runKubectlHub("label", "tenant", testTenantName,
			"-n", operatorNamespace,
			testRunLabelKey+"="+testRunID,
			"--overwrite")
	})

	It("should have a ready Tenant reconciled by the controller", func() {
		// The real tenant controller is enabled (the workload Kind
		// cluster has the fake OVN UserDefinedNetwork CRD installed
		// in BeforeSuite, so the remote informer syncs).  The
		// controller sets Phase=Ready on reconciliation.
		By("waiting for the tenant controller to set Phase=Ready")
		Eventually(func() string {
			out, err := runKubectlHub("get", "tenant",
				testTenantName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.phase}")
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}, 2*time.Minute, 2*time.Second).Should(Equal("Ready"),
			"the tenant controller should reconcile the "+
				"Tenant to Ready")
	})

	It("should have OSAC/AAP-created tenant StorageClass on workload cluster", func() {
		// The real storage path: the register-local-storage Helm hook
		// created a "local" StorageBackend+Tier in the fulfillment
		// service.  The storage reconciler detects the Ready Tenant,
		// queries the Tier API for definitions, finds no
		// tenant-labeled StorageClass on the workload cluster, and
		// dispatches the osac-create-tenant-cluster-storage AWX
		// template.  The playbook creates a tenant-labeled
		// StorageClass via the LVMS storage provider role.
		By("waiting for storage reconciler to provision " +
			"tenant StorageClass via AAP")
		waitForTenantStorageClass(testTenantName)
	})

	It("should have AAP-provisioned namespace and CUDN on workload cluster", func() {
		// The networking controller (enabled in this CI profile)
		// detects the auto-provisioned Subnet CR, dispatches the
		// osac-create-subnet AAP template which runs the
		// cudn_net.create_subnet role.  That role creates both the
		// namespace (with k8s.ovn.org/primary-user-defined-network
		// and osac.openshift.io/subnet-id labels) and the
		// ClusterUserDefinedNetwork on the workload cluster.
		//
		// Assert both exist before ComputeInstance provisioning.
		Expect(onboardedSubnetName).NotTo(BeEmpty(),
			"onboarded subnet name must be resolved before "+
				"checking workload-cluster resources")

		By("waiting for AAP to create the subnet namespace " +
			"on the workload cluster with exact label values")
		Eventually(func() error {
			out, err := runKubectlWorkload("get", "namespace",
				onboardedSubnetName,
				"-o", "json")
			if err != nil {
				return fmt.Errorf(
					"namespace %s not found on workload "+
						"cluster: %w",
					onboardedSubnetName, err)
			}
			var ns struct {
				Metadata struct {
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal(out, &ns); err != nil {
				return fmt.Errorf("parse namespace JSON: %w",
					err)
			}
			// Resolve the subnet-id that the Ansible role
			// derives: it uses
			// subnet.metadata.labels['osac.openshift.io/
			// subnet-uuid'] defaulting to
			// subnet.metadata.name.
			subnetID := onboardedSubnetName
			sidOut, sidErr := runKubectlHub("get", "subnet",
				onboardedSubnetName,
				"-n", operatorNamespace,
				"-o", `jsonpath={.metadata.labels.osac\.openshift\.io/subnet-uuid}`)
			if sidErr == nil {
				if v := strings.TrimSpace(
					string(sidOut)); v != "" {
					subnetID = v
				}
			}

			expected := nsLabelAssertions{
				PrimaryUDN: "",
				SubnetID:   subnetID,
				TenantID:   testTenantName,
			}
			mm := assertNSLabels(ns.Metadata.Labels, expected)
			if len(mm) > 0 {
				return fmt.Errorf(
					"namespace %s label mismatches: %v"+
						" (actual labels: %v)",
					onboardedSubnetName, mm,
					ns.Metadata.Labels)
			}
			return nil
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			"AAP cudn_net.create_subnet should create the "+
				"subnet namespace with exact label values "+
				"on the workload cluster")

		By("verifying AAP created the ClusterUserDefinedNetwork " +
			"with correct namespaceSelector on workload cluster")
		Eventually(func() error {
			out, err := runKubectlWorkload("get",
				"clusteruserdefinednetwork",
				onboardedSubnetName,
				"-o", "json")
			if err != nil {
				return fmt.Errorf(
					"CUDN %s not found on workload "+
						"cluster: %w",
					onboardedSubnetName, err)
			}

			var cudn struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					NamespaceSelector struct {
						MatchLabels map[string]string `json:"matchLabels"`
					} `json:"namespaceSelector"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(out, &cudn); err != nil {
				return fmt.Errorf("parse CUDN JSON: %w",
					err)
			}
			if cudn.Metadata.Name != onboardedSubnetName {
				return fmt.Errorf(
					"CUDN name mismatch: got %q, "+
						"want %q",
					cudn.Metadata.Name,
					onboardedSubnetName)
			}

			// Resolve subnet-id the same way as above.
			subnetID := onboardedSubnetName
			sidOut, sidErr := runKubectlHub("get", "subnet",
				onboardedSubnetName,
				"-n", operatorNamespace,
				"-o", `jsonpath={.metadata.labels.osac\.openshift\.io/subnet-uuid}`)
			if sidErr == nil {
				if v := strings.TrimSpace(
					string(sidOut)); v != "" {
					subnetID = v
				}
			}

			mm := assertCUDNSelector(
				cudn.Spec.NamespaceSelector.MatchLabels,
				subnetID)
			if len(mm) > 0 {
				return fmt.Errorf(
					"CUDN %s selector mismatches: %v",
					onboardedSubnetName, mm)
			}
			return nil
		}, 2*time.Minute, 5*time.Second).Should(Succeed(),
			"AAP cudn_net.create_subnet should create the "+
				"ClusterUserDefinedNetwork with "+
				"osac.openshift.io/subnet-id selector "+
				"on the workload cluster")
	})

	It("should reconcile ComputeInstance on the hub cluster", func() {
		Expect(onboardedSubnetName).NotTo(BeEmpty(),
			"subnet name must be resolved before creating "+
				"ComputeInstance")

		By("creating a ComputeInstance via the fulfillment gRPC API")
		computeInstanceID = createComputeInstanceViaAPI(
			testCIName, operatorNamespace,
			testTenantName, onboardedSubnetName)
		Expect(computeInstanceID).NotTo(BeEmpty(),
			"fulfillment API must return ComputeInstance ID")
		computeInstanceCreated = true

		// The fulfillment reconciler creates the K8s CR with
		// GenerateName: "vm-", so the actual CR name differs from
		// testCIName. Discover it by the computeinstance-uuid label
		// which is set to the fulfillment API ID.
		By("discovering the actual ComputeInstance CR name " +
			"by label")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance",
				"-l",
				"osac.openshift.io/computeinstance-uuid="+
					computeInstanceID,
				"-n", operatorNamespace,
				"-o", "jsonpath={.items[0].metadata.name}")
			if err != nil {
				return err
			}
			name := strings.TrimSpace(string(out))
			if name == "" {
				return fmt.Errorf(
					"no ComputeInstance CR found with "+
						"label computeinstance-uuid=%s",
					computeInstanceID)
			}
			actualCRName = name
			return nil
		}, 2*time.Minute, 2*time.Second).Should(Succeed(),
			"ComputeInstance CR should appear on the hub "+
				"with label computeinstance-uuid=%s",
			computeInstanceID)
		_, _ = fmt.Fprintf(GinkgoWriter,
			"discovered ComputeInstance CR name: %s "+
				"(API name: %s, API ID: %s)\n",
			actualCRName, testCIName, computeInstanceID)

		By("verifying the controller adds a finalizer to " +
			"the ComputeInstance")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance",
				actualCRName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.metadata.finalizers}")
			if err != nil {
				return err
			}
			if len(strings.TrimSpace(string(out))) == 0 {
				return fmt.Errorf(
					"no finalizers found on ComputeInstance")
			}
			return nil
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		By("verifying the controller resolved the " +
			"subnet-target-namespace annotation from the " +
			"Subnet CR")
		Eventually(func() string {
			out, err := runKubectlHub("get", "computeinstance",
				actualCRName,
				"-n", operatorNamespace,
				"-o", `jsonpath={.metadata.annotations.osac\.openshift\.io/subnet-target-namespace}`)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}, 30*time.Second, 2*time.Second).Should(
			Equal(onboardedSubnetName),
			"the controller should resolve "+
				"subnet-target-namespace from the "+
				"auto-provisioned Subnet CR name")
	})

	It("should provision a VirtualMachine on the workload cluster via AAP", func() {
		Expect(onboardedSubnetName).NotTo(BeEmpty(),
			"subnet name must be resolved before VM assertion")
		Expect(actualCRName).NotTo(BeEmpty(),
			"actual CR name must be discovered before VM "+
				"assertion")

		// Dump AAP/operator diagnostics on failure so CI logs are
		// actionable without re-running or inspecting artifacts.
		DeferCleanup(func() {
			if CurrentSpecReport().Failed() {
				dumpProvisioningDiagnostics(
					actualCRName, operatorNamespace)
			}
		})

		// AAP sets the VM name to the K8s CR name (actualCRName),
		// not the API metadata.name (testCIName). The operator
		// discovers VMs by label osac.openshift.io/computeinstance
		// = CR name.
		By("waiting for AAP to create the VirtualMachine " +
			"on the workload cluster")
		Eventually(func() error {
			out, err := runKubectlWorkload("get",
				"virtualmachines.kubevirt.io",
				actualCRName, "-n", onboardedSubnetName,
				"-o", "jsonpath={.metadata.name}")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != actualCRName {
				return fmt.Errorf(
					"VirtualMachine %s not found on "+
						"workload cluster in namespace %s",
					actualCRName, onboardedSubnetName)
			}
			return nil
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			"the operator should trigger AAP provisioning "+
				"which creates the VM on the workload cluster "+
				"in the subnet-derived namespace")

		By("verifying the VirtualMachine has the correct " +
			"ownership label")
		// The operator uses label osac.openshift.io/computeinstance
		// = instance.GetName() where instance is the K8s CR.
		out, err := runKubectlWorkload("get",
			"virtualmachines.kubevirt.io",
			actualCRName, "-n", onboardedSubnetName,
			"-o", `jsonpath={.metadata.labels.osac\.openshift\.io/computeinstance}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(
			Equal(actualCRName),
			"VM should have "+
				"osac.openshift.io/computeinstance label "+
				"matching the K8s CR name")

		By("verifying the VM uses runStrategy Halted " +
			"(no readiness wait)")
		out, err = runKubectlWorkload("get",
			"virtualmachines.kubevirt.io",
			actualCRName, "-n", onboardedSubnetName,
			"-o", "jsonpath={.spec.runStrategy}")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(out))).To(
			Equal("Halted"),
			"VM should use runStrategy Halted to avoid "+
				"readiness wait on fake CRDs")

		By("verifying the operator records a successful " +
			"provisioning job")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance",
				actualCRName,
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.provisioningJobs}")
			if err != nil {
				return err
			}
			jobs := strings.TrimSpace(string(out))
			if jobs == "" || jobs == "[]" {
				return fmt.Errorf(
					"no provisioning jobs recorded in status")
			}
			return nil
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying the operator reconciles the VM " +
			"reference from the workload cluster")
		Eventually(func() error {
			out, err := runKubectlHub("get", "computeinstance",
				actualCRName,
				"-n", operatorNamespace,
				"-o", `jsonpath={.status.virtualMachineReference.kubeVirtVirtualMachineName}`)
			if err != nil {
				return err
			}
			vmName := strings.TrimSpace(string(out))
			if vmName != actualCRName {
				return fmt.Errorf(
					"expected "+
						"virtualMachineReference."+
						"kubeVirtVirtualMachineName=%q, "+
						"got %q",
					actualCRName, vmName)
			}
			return nil
		}, 2*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should NOT have the VirtualMachine on the hub cluster", func() {
		Expect(actualCRName).NotTo(BeEmpty(),
			"actual CR name must be discovered before hub "+
				"VM assertion")

		// The specific test VM must not exist on the hub regardless
		// of whether the hub has KubeVirt CRDs installed.  Search
		// across all namespaces with a field-selector on the exact
		// CR name (AAP uses the CR name for the VM); only treat
		// explicit empty output or a CRD-not-found error as proof
		// of absence.
		out, err := runKubectlHub("get",
			"virtualmachines.kubevirt.io",
			"--all-namespaces",
			"--field-selector=metadata.name="+actualCRName,
			"--ignore-not-found",
			"-o", "jsonpath={.items[*].metadata.name}")
		if err != nil {
			// CRD not installed on the hub — VM cannot exist.
			errStr := string(out)
			Expect(errStr).To(
				Or(ContainSubstring("the server doesn't have a resource type"),
					ContainSubstring("NotFound"),
					ContainSubstring("no matches for kind")),
				"unexpected error checking for hub VM: %v\n%s",
				err, errStr)
		} else {
			Expect(strings.TrimSpace(string(out))).To(BeEmpty(),
				"VirtualMachine %q should not exist on the "+
					"hub cluster in any namespace",
				actualCRName)
		}
	})
})

// createComputeInstanceViaAPI creates a ComputeInstance through the
// fulfillment private gRPC ComputeInstances/Create API. Both Tenants and
// ComputeInstances are registered as gRPC services (privatev1.Register-
// TenantsServer, privatev1.RegisterComputeInstancesServer), so we use
// gRPC for both, via runFulfillmentGRPC (initContainer token pattern).
//
// runStrategy is set to HALTED so the AAP playbook creates the
// VirtualMachine CR without waiting for VM readiness, allowing the
// test to exercise real operator->AAP->Ansible creation against fake
// KubeVirt CRDs that never produce a running VirtualMachineInstance.
//
// No POST retry -- a retry on error can duplicate ComputeInstance creation.
// No TLS bypass (curl -k).  Token never appears in pod logs, Go memory,
// or assertion messages.
//
// The returned string is the fulfillment-assigned ComputeInstance ID,
// used for subsequent Delete calls.
func createComputeInstanceViaAPI(
	name, namespace, tenantName, subnetName string,
) string {
	grpcPayload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s","tenant":"%s"},`+
			`"spec":{"template":{"name":"osac.templates.ocp_virt_vm"},`+
			`"boot_disk":{"size_gib":20,`+
			`"storage_tier":{"name":"local"}},`+
			`"run_strategy":"COMPUTE_INSTANCE_RUN_STRATEGY_HALTED",`+
			`"network_attachments":[{"subnet":{"name":"%s"}}]}}}`,
		name, tenantName, subnetName)

	output, phase := runFulfillmentGRPC(namespace,
		"mc-test-create-ci",
		"osac.private.v1.ComputeInstances/Create",
		grpcPayload)

	outStr := strings.TrimSpace(string(output))
	Expect(phase).To(Equal("Succeeded"),
		"ComputeInstances/Create gRPC call must succeed:\n%s",
		outStr)

	// Parse the gRPC JSON response. ComputeInstancesCreateResponse
	// wraps the ComputeInstance in an "object" envelope:
	//   {"object":{"id":"...","metadata":{"name":"..."},...}}
	var resp struct {
		Object struct {
			ID       string `json:"id"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"object"`
	}
	Expect(json.Unmarshal(output, &resp)).To(Succeed(),
		"parse ComputeInstance create response")
	Expect(resp.Object.ID).NotTo(BeEmpty(),
		"ComputeInstance create response must contain "+
			"object.id")
	_, _ = fmt.Fprintf(GinkgoWriter,
		"ComputeInstance %q created via gRPC API (id=%s)\n",
		name, resp.Object.ID)
	return resp.Object.ID
}

// deleteComputeInstanceViaAPI deletes a ComputeInstance through the
// fulfillment private gRPC ComputeInstances/Delete API.  Errors are
// logged but not fatal -- the emergency cleanup in AfterAll handles
// residual CRs via finalizer removal.
func deleteComputeInstanceViaAPI(ciID, namespace string) {
	if ciID == "" {
		return
	}
	grpcPayload := fmt.Sprintf(`{"id":"%s"}`, ciID)

	output, phase := runFulfillmentGRPC(namespace,
		"mc-test-delete-ci",
		"osac.private.v1.ComputeInstances/Delete",
		grpcPayload)

	outStr := strings.TrimSpace(string(output))
	if phase != "Succeeded" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: delete ComputeInstance %q via gRPC "+
				"failed (phase=%s):\n%s\n", ciID, phase, outStr)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter,
		"ComputeInstance %q delete initiated via gRPC API\n", ciID)
}

// createTenantViaAPI creates a tenant through the fulfillment private
// gRPC Tenants/Create API using runFulfillmentGRPC (initContainer token
// pattern). The token never appears in pod logs, Go memory, or
// assertions.
//
// AlreadyExists is treated as FAILURE -- the unique testTenantName
// (mc-tenant-<runID>) prevents collisions with other test runs or
// pre-existing resources. Accepting AlreadyExists would silently adopt
// a foreign tenant and delete it in AfterAll.
//
// Returns the fulfillment-assigned tenant ID from the gRPC response.
func createTenantViaAPI(
	tenantName, namespace string,
) string {
	grpcPayload := fmt.Sprintf(
		`{"object":{"metadata":{"name":"%s"}}}`, tenantName)

	output, phase := runFulfillmentGRPC(namespace,
		"mc-test-create-tenant",
		"osac.private.v1.Tenants/Create",
		grpcPayload)

	outStr := strings.TrimSpace(string(output))
	Expect(phase).To(Equal("Succeeded"),
		"Tenants/Create gRPC call must succeed "+
			"(AlreadyExists is NOT accepted for unique "+
			"tenant name %q):\n%s", tenantName, outStr)

	// Parse the gRPC JSON response. TenantsCreateResponse wraps
	// the Tenant in an "object" envelope:
	//   {"object":{"id":"...","metadata":{"name":"..."},...}}
	var resp struct {
		Object struct {
			ID       string `json:"id"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"object"`
	}
	Expect(json.Unmarshal(output, &resp)).To(Succeed(),
		"parse Tenant create response")
	Expect(resp.Object.ID).NotTo(BeEmpty(),
		"Tenant create response must contain object.id")
	_, _ = fmt.Fprintf(GinkgoWriter,
		"tenant %q created via gRPC API (id=%s)\n",
		tenantName, resp.Object.ID)
	return resp.Object.ID
}

// deleteTenantViaAPI deletes a tenant through the fulfillment private
// gRPC Tenants/Delete API.  Takes the fulfillment-assigned tenant ID
// (not the name).  Errors are logged but not fatal -- the emergency
// cleanup in AfterAll handles residual CRs via finalizer removal.
func deleteTenantViaAPI(tID, namespace string) {
	if tID == "" {
		return
	}
	grpcPayload := fmt.Sprintf(`{"id":"%s"}`, tID)

	output, phase := runFulfillmentGRPC(namespace,
		"mc-test-delete-tenant",
		"osac.private.v1.Tenants/Delete",
		grpcPayload)

	outStr := strings.TrimSpace(string(output))
	if phase != "Succeeded" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: delete tenant %q via gRPC "+
				"failed (phase=%s):\n%s\n", tID, phase, outStr)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter,
		"tenant %q delete initiated via gRPC API\n", tID)
}

// findSubnetByTenantAnnotation returns the name of the first Subnet CR
// in the given namespace that has the annotation
// osac.openshift.io/tenant set to tenantName.  Uses JSON output for
// reliable parsing instead of brittle JSONPath range expressions.
func findSubnetByTenantAnnotation(
	namespace, tenantName string,
) (string, error) {
	out, err := runKubectlHub("get", "subnets",
		"-n", namespace, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("list subnets: %w", err)
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("parse subnet list: %w", err)
	}

	for _, item := range list.Items {
		if item.Metadata.Annotations["osac.openshift.io/tenant"] ==
			tenantName {
			return item.Metadata.Name, nil
		}
	}
	return "", nil
}
