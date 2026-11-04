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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

// The installer's ValidatingAdmissionPolicy (osac chart,
// templates/networking/admission-policy.yaml; decision 18) on a real API
// server. The chart renders it only with the fulfillment service, which the
// operator leg disables, so `make -C osac-installer test SUITE=operator`
// renders it and passes the file in OSAC_IT_ADMISSION_POLICY for the focused
// admission-policy run. This spec applies it, checks it, and removes it
// again so the other specs (which write networking CRs as cluster-admin) are
// unaffected. The specs run serially.

const admissionPolicyEnv = "OSAC_IT_ADMISSION_POLICY"

const bmfNetworkingFinalizer = "osac.openshift.io/baremetalinstance-networking"
const bmfCreateDeniedMessage = "BMF may only create physical BareMetalInstance NetworkAttachments"
const bmfDeleteDeniedMessage = "BMF may only delete its physical BareMetalInstance NetworkAttachments"

// networkingKinds are the kinds the policy protects for every write.
var networkingKinds = []string{
	"NetworkClass", "ExternalIPPool", "VirtualNetwork", "Subnet", "SecurityGroup",
	"ExternalIP", "ExternalIPAttachment", "NATGateway", "FabricDomain", "NetworkAttachment",
}

// sampleSpec returns a valid spec for each networking kind.
func sampleSpec(kind string) map[string]any {
	switch kind {
	case "NetworkClass":
		return validNetworkClassSpec()
	case "ExternalIPPool":
		return map[string]any{"cidrs": []any{"192.0.2.0/24"}, "ipFamily": "IPv4"}
	case "VirtualNetwork":
		return map[string]any{"ipv4Cidr": "10.240.0.0/16"}
	case "Subnet":
		return map[string]any{"virtualNetwork": "vn", "ipv4Cidr": "10.240.1.0/24", "addressPlan": validAddressPlan()}
	case "SecurityGroup":
		return map[string]any{"virtualNetwork": "vn"}
	case "ExternalIP":
		return map[string]any{"pool": "pool"}
	case "ExternalIPAttachment":
		return map[string]any{"externalIP": "eip", "computeInstance": "ci"}
	case "NATGateway":
		return map[string]any{"virtualNetwork": "vn", "externalIP": "eip"}
	case "FabricDomain":
		return map[string]any{"type": "ethernet_ew", "servers": []any{"bmi"}, "virtualNetwork": "vn"}
	case "NetworkAttachment":
		return validNetworkAttachmentSpec("ComputeInstance", "overlay")
	}
	Fail("no sample spec for " + kind)
	return nil
}

func expectDenied(output string, err error, what string) {
	Expect(err).To(HaveOccurred(), "the admission policy must deny %s", what)
	Expect(
		output,
	).To(ContainSubstring(admissionDeniedMessage), "%s must be denied by the OSAC policy, not by RBAC or validation", what)
}

func expectAllowed(output string, err error, what string) {
	Expect(err).NotTo(HaveOccurred(), "the admission policy must allow %s: %s", what, output)
}

func expectBMFDenied(output string, err error, message string) {
	Expect(err).To(HaveOccurred())
	Expect(output).To(ContainSubstring(message))
}

var _ = Describe("Installer ValidatingAdmissionPolicy "+
	"on OSAC-managed resources", Ordered, Serial, Label("networking", "admission"), func() {
	var (
		policyFile, operator, writer, sfx string
		policyName, workloadName          string
	)

	BeforeAll(func() {
		policyFile = os.Getenv(admissionPolicyEnv)
		if policyFile == "" {
			Skip(admissionPolicyEnv + " is not set; `make -C osac-installer test SUITE=operator` renders the policy and sets it")
		}
		sfx = uniqueSuffix()
		operator = operatorIdentity()

		By("reading the rendered policy " + policyFile)
		data, err := os.ReadFile(policyFile)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fmt.Fprintf(GinkgoWriter, "rendered policy:\n%s\n", data)
		Expect(string(data)).To(ContainSubstring("kind: ValidatingAdmissionPolicy"))
		Expect(string(data)).To(ContainSubstring(operator), "the policy must allow the deployed operator's service account")
		policyName = operatorNamespace + "-osac-managed-resources"

		By("creating a ServiceAccount whose RBAC allows every write to OSAC resources")
		writerSA := "osac-it-writer-" + sfx
		writer = fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, writerSA)
		rbac := fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
  namespace: %[2]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: %[1]s
  namespace: %[2]s
rules:
- apiGroups: ["osac.openshift.io"]
  resources: ["*"]
  verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[1]s
  namespace: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: %[1]s
subjects:
- kind: ServiceAccount
  name: %[1]s
  namespace: %[2]s
`, writerSA, operatorNamespace)
		output, err := kubectl(rbac, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred(), output)
		DeferCleanup(func() {
			_, _ = kubectl(rbac, "delete", "-f", "-", "--ignore-not-found")
		})

		By("creating a ComputeInstance with an OSAC finalizer before the policy exists")
		workloadName = "vap-ci-" + sfx
		ci := unmanaged(newObject("ComputeInstance", workloadName, map[string]any{
			"templateID":  "noop",
			"image":       map[string]any{"sourceType": "registry", "sourceRef": "quay.io/fedora/fedora-coreos:stable"},
			"vcpus":       int64(1),
			"memoryGiB":   int64(1),
			"bootDisk":    map[string]any{"sizeGiB": int64(1), "storageTier": "test"},
			"runStrategy": "Always",
		}))
		ci.SetFinalizers([]string{"osac.openshift.io/computeinstance", networkCleanupTestFinalizer})
		// Registered before the policy cleanup, so it runs after the policy is gone.
		mustCreate(ci)

		existing, err := kubectl("", "get", "validatingadmissionpolicy", policyName, "--ignore-not-found", "-o", "name")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(existing)).To(BeEmpty(),
			"the operator leg deploys without the fulfillment service, so the chart must not have installed the policy")

		By("applying the installer's policy and binding")
		output, err = kubectl("", "apply", "-f", policyFile)
		Expect(err).NotTo(HaveOccurred(), output)
		DeferCleanup(func() {
			By("removing the policy and waiting until cluster-admin writes are admitted again")
			_, err := kubectl("", "delete", "-f", policyFile, "--ignore-not-found")
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := createAs(
					"",
					newObject("VirtualNetwork", "vap-probe-"+sfx, sampleSpec("VirtualNetwork")),
					"--dry-run=server",
				)
				return err
			}, time.Minute, time.Second).Should(Succeed(), "the removed policy must stop denying writes")
		})

		By("waiting until the API server enforces the policy")
		Eventually(func(g Gomega) {
			output, err := createAs(
				"",
				newObject("VirtualNetwork", "vap-probe-"+sfx, sampleSpec("VirtualNetwork")),
				"--dry-run=server",
			)
			g.Expect(err).To(HaveOccurred())
			g.Expect(output).To(ContainSubstring(admissionDeniedMessage))
		}, time.Minute, time.Second).Should(Succeed(), "the policy was not enforced within a minute")
	})

	It("denies every networking CR create to a cluster admin and to an RBAC-authorized non-OSAC ServiceAccount", func() {
		for _, kind := range networkingKinds {
			obj := newObject(kind, fmt.Sprintf("vap-%s-%s", strings.ToLower(kind), sfx), sampleSpec(kind))
			By("checking that RBAC allows the test ServiceAccount to create " + kind)
			allowed, reason := subjectAccessReview(writer, operatorNamespace, "osac.openshift.io", pluralOf(kind), "", "create")
			Expect(allowed).To(BeTrue(), "RBAC must allow %s so that the denial comes from the policy: %s", writer, reason)

			By("creating " + kind + " as cluster-admin")
			output, err := createAs("", obj, "--dry-run=server")
			expectDenied(output, err, "a cluster-admin create of "+kind)
			By("creating " + kind + " as " + writer)
			output, err = createAs(writer, obj, "--dry-run=server")
			expectDenied(output, err, "a non-OSAC ServiceAccount create of "+kind)
		}
	})

	It("admits the operator's own create, finalizer, status and delete writes and denies them to others", func() {
		na := newObject("NetworkAttachment", "computeinstance-vap-"+sfx, sampleSpec("NetworkAttachment"))
		By("creating a NetworkAttachment as the operator's ServiceAccount " + operator)
		output, err := createAs(operator, na)
		expectAllowed(output, err, "the operator's create of a NetworkAttachment")
		DeferCleanup(func() {
			// The policy is still active here; the operator identity deletes it.
			_, _ = kubectl("", "delete", resourceOf("NetworkAttachment"), na.GetName(), "-n", operatorNamespace,
				"--ignore-not-found", "--wait=false", "--as="+operator)
			Eventually(
				func() (bool, error) { return objectAbsent("NetworkAttachment", na.GetName()) },
				readyTimeout,
				time.Second,
			).Should(BeTrue())
		})

		By("the deployed operator adds its finalizer and writes status under the policy")
		eventuallyReady("NetworkAttachment", na.GetName(), "False", "Failed", networkingDisabledMessage)
		current, err := getObject("NetworkAttachment", na.GetName())
		Expect(err).NotTo(HaveOccurred())
		Expect(current.GetFinalizers()).To(ContainElement(finalizerNetworkAttachment))

		By("denying a label update, a status write, a finalizer removal and a delete " +
			"to cluster-admin and the test ServiceAccount")
		for _, identity := range []string{"", writer} {
			who := identity
			if who == "" {
				who = "cluster-admin"
			}
			output, err = patchAs(identity, "NetworkAttachment", na.GetName(), "merge", `{"metadata":{"labels":{"it":"x"}}}`, "")
			expectDenied(output, err, who+" updating a NetworkAttachment")
			output, err = patchAs(identity, "NetworkAttachment", na.GetName(), "merge",
				`{"status":{"conditions":[]}}`, "status")
			expectDenied(output, err, who+" writing NetworkAttachment status")
			output, err = patchAs(identity, "NetworkAttachment", na.GetName(), "merge", `{"metadata":{"finalizers":[]}}`, "")
			expectDenied(output, err, who+" removing the NetworkAttachment finalizer")
			args := []string{"delete", resourceOf("NetworkAttachment"), na.GetName(), "-n", operatorNamespace, "--wait=false"}
			if identity != "" {
				args = append(args, "--as="+identity)
			}
			output, err := kubectl("", args...)
			expectDenied(output, err, who+" deleting a NetworkAttachment")
		}

		By("admitting a label update by the operator's identity")
		output, err = patchAs(
			operator,
			"NetworkAttachment",
			na.GetName(),
			"merge",
			`{"metadata":{"labels":{"it":"operator"}}}`,
			"",
		)
		expectAllowed(output, err, "the operator's update")

		By("admitting the operator identity's delete; the deployed operator then releases its finalizer under the policy")
		output, err = kubectl("", "delete", resourceOf("NetworkAttachment"), na.GetName(), "-n", operatorNamespace,
			"--wait=false", "--as="+operator)
		expectAllowed(output, err, "the operator's delete")
		Eventually(
			func() (bool, error) { return objectAbsent("NetworkAttachment", na.GetName()) },
			readyTimeout,
			time.Second,
		).Should(BeTrue(),
			"the operator must remove its finalizer while the policy is enforced")
	})

	It("limits BMF NetworkAttachment patches to removing its finalizer after cleanup", func() {
		// The umbrella chart defaults the BMF ServiceAccount name to bmf-operator.
		bmf := fmt.Sprintf("system:serviceaccount:%s:bmf-operator", operatorNamespace)
		bmfRole := "osac-it-bmf-finalizer-" + sfx
		rbac := fmt.Sprintf(`apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: %[1]s
  namespace: %[2]s
rules:
- apiGroups: ["osac.openshift.io"]
  resources: ["networkattachments"]
  verbs: ["create", "delete", "get", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[1]s
  namespace: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: %[1]s
subjects:
- kind: ServiceAccount
  name: %[3]s
  namespace: %[2]s
`, bmfRole, operatorNamespace, "bmf-operator")
		output, err := kubectl(rbac, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred(), output)
		DeferCleanup(func() {
			_, _ = kubectl(rbac, "delete", "-f", "-", "--ignore-not-found")
		})

		for _, verb := range []string{"create", "delete", "get", "patch"} {
			allowed, reason := subjectAccessReview(bmf, operatorNamespace,
				"osac.openshift.io", "networkattachments", "", verb)
			Expect(allowed).To(BeTrue(), "RBAC must allow %s to %s NetworkAttachments: %s", bmf, verb, reason)
		}

		By("denying BMF creation of a ComputeInstance overlay attachment")
		computeID := "vap-bmf-compute-" + sfx
		computeSpec := validNetworkAttachmentSpec("ComputeInstance", "overlay")
		computeSpec["target"].(map[string]any)["id"] = computeID
		computeNA := newObject("NetworkAttachment", "computeinstance-"+computeID, computeSpec)
		output, err = createAs(bmf, computeNA)
		expectBMFDenied(output, err, bmfCreateDeniedMessage)

		By("denying BMF deletion of a ComputeInstance attachment")
		deleteID := "vap-bmf-delete-" + sfx
		deleteNAName := "computeinstance-" + deleteID
		deleteSpec := validNetworkAttachmentSpec("ComputeInstance", "overlay")
		deleteSpec["target"].(map[string]any)["id"] = deleteID
		deleteNA := unmanaged(newObject("NetworkAttachment", deleteNAName, deleteSpec))
		output, err = createAs(operator, deleteNA)
		expectAllowed(output, err, "the operator creating a NetworkAttachment used for the BMF delete check")
		DeferCleanup(func() {
			_, _ = kubectl("", "delete", resourceOf("NetworkAttachment"), deleteNAName,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+operator)
		})
		output, err = kubectl("", "delete", resourceOf("NetworkAttachment"), deleteNAName,
			"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+bmf)
		expectBMFDenied(output, err, bmfDeleteDeniedMessage)
		_, err = getObject("NetworkAttachment", deleteNAName)
		Expect(err).NotTo(HaveOccurred(), "a denied BMF delete must leave the attachment in place")

		By("denying BMF deletion of a physical attachment without its finalizer")
		unownedID := "vap-bmf-unowned-" + sfx
		unownedName := "baremetalinstance-" + unownedID
		unownedSpec := validNetworkAttachmentSpec("BareMetalInstance", "physical")
		unownedSpec["target"].(map[string]any)["id"] = unownedID
		unownedNA := unmanaged(newObject("NetworkAttachment", unownedName, unownedSpec))
		output, err = createAs(operator, unownedNA)
		expectAllowed(output, err, "the operator creating an unmanaged physical attachment")
		DeferCleanup(func() {
			_, _ = kubectl("", "delete", resourceOf("NetworkAttachment"), unownedName,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+operator)
		})
		output, err = kubectl("", "delete", resourceOf("NetworkAttachment"), unownedName,
			"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+bmf)
		expectBMFDenied(output, err, bmfDeleteDeniedMessage)
		_, err = getObject("NetworkAttachment", unownedName)
		Expect(err).NotTo(HaveOccurred(), "BMF must not delete an attachment it does not own")

		By("allowing BMF deletion of a physical attachment with its finalizer")
		ownedDeleteID := "vap-bmf-owned-delete-" + sfx
		ownedDeleteName := "baremetalinstance-" + ownedDeleteID
		ownedDeleteSpec := validNetworkAttachmentSpec("BareMetalInstance", "physical")
		ownedDeleteSpec["target"].(map[string]any)["id"] = ownedDeleteID
		ownedDeleteNA := unmanaged(newObject("NetworkAttachment", ownedDeleteName, ownedDeleteSpec))
		ownedDeleteNA.SetFinalizers([]string{bmfNetworkingFinalizer})
		output, err = createAs(bmf, ownedDeleteNA)
		expectAllowed(output, err, "BMF creating an attachment it owns")
		DeferCleanup(func() {
			_, _ = patchAs(operator, "NetworkAttachment", ownedDeleteName, "merge",
				`{"metadata":{"finalizers":[]}}`, "")
			_, _ = kubectl("", "delete", resourceOf("NetworkAttachment"), ownedDeleteName,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+operator)
		})
		output, err = kubectl("", "delete", resourceOf("NetworkAttachment"), ownedDeleteName,
			"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+bmf)
		expectAllowed(output, err, "BMF deleting an attachment with its finalizer")

		By("allowing BMF to create a physical BareMetalInstance attachment")
		bmiID := "vap-bmf-finalizer-" + sfx
		name := "baremetalinstance-" + bmiID
		bmiSpec := validNetworkAttachmentSpec("BareMetalInstance", "physical")
		bmiSpec["target"].(map[string]any)["id"] = bmiID
		na := newObject("NetworkAttachment", name, bmiSpec)
		na.SetAnnotations(map[string]string{"osac.openshift.io/tenant": "networking-it"})
		na.SetFinalizers([]string{bmfNetworkingFinalizer})
		output, err = createAs(bmf, na)
		expectAllowed(output, err, "BMF creating a physical BareMetalInstance NetworkAttachment")
		DeferCleanup(func() {
			_, _ = patchAs(operator, "NetworkAttachment", name, "merge",
				`{"metadata":{"finalizers":[]}}`, "")
			_, _ = kubectl("", "delete", resourceOf("NetworkAttachment"), name,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false", "--as="+operator)
		})
		Eventually(func(g Gomega) {
			current, getErr := getObject("NetworkAttachment", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetFinalizers()).To(ContainElement(finalizerNetworkAttachment))
		}, readyTimeout, time.Second).Should(Succeed())
		output, err = patchAs(operator, "NetworkAttachment", name, "merge",
			fmt.Sprintf(`{"metadata":{"finalizers":[%q,%q,%q]}}`, finalizerNetworkAttachment,
				bmfNetworkingFinalizer, networkCleanupTestFinalizer), "")
		expectAllowed(output, err, "the operator adding the test cleanup finalizer")

		By("denying BMF metadata changes even though RBAC grants patch")
		output, err = patchAs(bmf, "NetworkAttachment", name, "merge",
			`{"metadata":{"annotations":{"osac.openshift.io/tenant":"other-tenant"}}}`, "")
		Expect(err).To(HaveOccurred())
		Expect(output).To(ContainSubstring("BMF may only remove its own finalizer"))

		By("starting cleanup as the networking controller")
		output, err = kubectl("", "delete", resourceOf("NetworkAttachment"), name,
			"-n", operatorNamespace, "--wait=false", "--as="+operator)
		expectAllowed(output, err, "the operator's NetworkAttachment delete")
		Eventually(func(g Gomega) {
			current, getErr := getObject("NetworkAttachment", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
			g.Expect(current.GetFinalizers()).To(ConsistOf(bmfNetworkingFinalizer, networkCleanupTestFinalizer))
		}, readyTimeout, time.Second).Should(Succeed())

		By("denying BMF removal of the unrelated remaining finalizer")
		output, err = patchAs(bmf, "NetworkAttachment", name, "merge",
			`{"metadata":{"finalizers":[]}}`, "")
		Expect(err).To(HaveOccurred())
		Expect(output).To(ContainSubstring("BMF may only remove its own finalizer"))

		By("allowing the operator to remove its cleanup finalizer")
		output, err = patchAs(operator, "NetworkAttachment", name, "merge",
			fmt.Sprintf(`{"metadata":{"finalizers":[%q]}}`, bmfNetworkingFinalizer), "")
		expectAllowed(output, err, "the operator's cleanup finalizer removal")

		By("allowing BMF to remove its own finalizer after cleanup")
		Eventually(func(g Gomega) {
			current, getErr := getObject("NetworkAttachment", name)
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(current.GetFinalizers()).To(ConsistOf(bmfNetworkingFinalizer))
		}, readyTimeout, time.Second).Should(Succeed())
		output, err = patchAs(bmf, "NetworkAttachment", name, "merge",
			`{"metadata":{"finalizers":[]}}`, "")
		expectAllowed(output, err, "BMF removing its own finalizer after cleanup")
		Eventually(func() (bool, error) { return objectAbsent("NetworkAttachment", name) }, readyTimeout, time.Second).
			Should(BeTrue(), "the BMF finalizer removal must allow deletion to complete")
	})

	It("denies workload creates, updates and deletes to non-OSAC identities", func() {
		workloads := []struct {
			kind string
			spec map[string]any
		}{
			{"ComputeInstance", map[string]any{
				"templateID": "noop",
				"image":      map[string]any{"sourceType": "registry", "sourceRef": "quay.io/fedora/fedora-coreos:stable"},
				"vcpus":      int64(1), "memoryGiB": int64(1),
				"bootDisk": map[string]any{"sizeGiB": int64(1), "storageTier": "test"}, "runStrategy": "Always",
			}},
			{"BareMetalInstance", map[string]any{
				"templateID": "noop", "externalHostID": "",
				"selector": map[string]any{"hostSelector": map[string]any{"hostType": "test-cleanup"}},
			}},
			{"ClusterOrder", map[string]any{"templateID": "noop"}},
		}
		for _, workload := range workloads {
			obj := newObject(workload.kind, "vap-create-"+strings.ToLower(workload.kind)+"-"+sfx, workload.spec)
			allowed, reason := subjectAccessReview(
				writer, operatorNamespace, "osac.openshift.io", pluralOf(workload.kind), "", "create")
			Expect(allowed).To(BeTrue(),
				"RBAC must allow %s to create %s so the policy is tested: %s", writer, workload.kind, reason)
			for _, identity := range []string{"", writer} {
				output, err := createAs(identity, obj, "--dry-run=server")
				expectDenied(output, err, "creating a "+workload.kind+" as "+identity)
			}
		}

		finalizers := func() []string {
			ci, err := getObject("ComputeInstance", workloadName)
			Expect(err).NotTo(HaveOccurred())
			return ci.GetFinalizers()
		}
		By("denying workload updates to cluster-admin and the test ServiceAccount")
		for _, identity := range []string{"", writer} {
			output, err := patchAs(
				identity, "ComputeInstance", workloadName, "merge", `{"metadata":{"labels":{"it":"admin"}}}`, "")
			expectDenied(output, err, "updating a ComputeInstance as "+identity)
		}

		By("denying the removal of osac.openshift.io/computeinstance")
		for _, identity := range []string{"", writer} {
			output, err := patchAs(identity, "ComputeInstance", workloadName, "merge",
				fmt.Sprintf(`{"metadata":{"finalizers":[%q]}}`, networkCleanupTestFinalizer), "")
			expectDenied(output, err, "removing an OSAC finalizer as "+identity)
		}
		Expect(finalizers()).To(ContainElement("osac.openshift.io/computeinstance"))

		By("denying a workload delete to cluster-admin and the test ServiceAccount")
		for _, identity := range []string{"", writer} {
			args := []string{"delete", resourceOf("ComputeInstance"), workloadName, "-n", operatorNamespace, "--wait=false"}
			if identity != "" {
				args = append(args, "--as="+identity)
			}
			output, err := kubectl("", args...)
			expectDenied(output, err, "deleting a ComputeInstance as "+identity)
		}
		ci, err := getObject("ComputeInstance", workloadName)
		Expect(err).NotTo(HaveOccurred())
		Expect(ci.GetDeletionTimestamp()).To(BeNil(), "a denied delete must not mark the workload")

		By("admitting the operator identity's finalizer removal")
		output, err := patchAs(operator, "ComputeInstance", workloadName, "merge", `{"metadata":{"finalizers":[]}}`, "")
		expectAllowed(output, err, "the operator removing its finalizer")
		Expect(finalizers()).To(BeEmpty())
	})
})
