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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	bmaasNetworkingLabel         = "bmaas-networking"
	bmaasNetworkingDisabledLabel = "bmaas-networking-disabled"
	bmaasFeedbackFinalizer       = "osac.openshift.io/baremetalinstance-feedback"
	bmaasNetworkingFinalizer     = "osac.openshift.io/baremetalinstance-networking"
	bmaasApplyTemplate           = "osac-apply-network-attachment"
	bmaasQueryTemplate           = "osac-query-dhcp-lease"
	bmaasDeleteTemplate          = "osac-delete-network-attachment"
	bmaasProvisionTemplate       = "osac-create-bare-metal-instance"
	bmaasDeprovisionTemplate     = "osac-delete-bare-metal-instance"
	bmaasSelectedMAC             = "aa:bb:cc:dd:ee:ff"
	bmaasTenant                  = "tenant-kind-it"
)

type bmaasFixture struct {
	id, host, virtualNetwork, subnet, attachment string
}

var _ = Describe("BMaaS NetworkAttachment handoff on deployed controllers",
	Ordered, Label(bmaasNetworkingLabel), func() {
		BeforeAll(func() {
			if os.Getenv("IT_TEST_SUITE") != "bmf" {
				Skip("requires the SUITE=bmf handoff phase")
			}
			Expect(operatorDeploymentEnv("OSAC_PROVISIONING_ENABLED")).To(Equal("true"),
				"the BMF handoff suite requires provider networking enabled")
			Expect(operatorDeploymentEnv("OSAC_AAP_URL")).To(ContainSubstring("osac-aap-recorder"),
				"the OSAC operator must use the test-controlled AAP recorder")
			Expect(bmfDeploymentEnv("OSAC_ENABLE_NETWORKING_PROVISIONING")).To(Equal("true"),
				"the BMF controller must use the same provider-networking setting")
			Expect(bmfDeploymentEnv("OSAC_AAP_URL")).To(ContainSubstring("osac-aap-recorder"),
				"the BMF controller must use the test-controlled AAP recorder")
			installAAPRecorder()
		})

		BeforeEach(func() { resetAAPRecorder() })

		It("provisions, applies, hands off, discovers the lease, then applies pending run and restart intent once", func() {
			fixture := newBMAASFixture("ordered", "test_template", "Halted")

			By("allocating a host and running its provisioning job before network handoff")
			waitForBMAASHostAssignment(fixture)
			provision := waitForAAPTemplate(bmaasProvisionTemplate)
			Consistently(func(g Gomega) {
				g.Expect(getAAPTemplates()).NotTo(ContainElement(bmaasApplyTemplate),
					"the Fabric apply must wait for BMF provisioning to finish")
			}, 5*time.Second, time.Second).Should(Succeed())
			completeAAPJob(provision, "successful", nil)

			By("waiting for BMF to create its physical NetworkAttachment")
			attachment := waitForBMAASAttachment(fixture)
			Expect(attachment.Object["spec"]).To(HaveKeyWithValue("target", map[string]any{
				"kind": "BareMetalInstance", "id": fixture.id,
			}))
			Expect(attachment.Object["spec"]).To(HaveKeyWithValue("subnet", fixture.subnet))
			macAddress, found, readErr := unstructured.NestedString(attachment.Object, "spec", "interface", "macAddress")
			Expect(readErr).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "spec.interface.macAddress must be present")
			Expect(macAddress).To(Equal(bmaasSelectedMAC))

			By("changing runStrategy and restartTrigger while the Fabric apply is still running")
			apply := waitForAAPTemplate(bmaasApplyTemplate)
			patchBMAASInstance(fixture.id, `{"spec":{"runStrategy":"Always","restartTrigger":1}}`)
			Consistently(func(g Gomega) {
				bmh := getBMAASHost(fixture.host)
				online, found, readErr := unstructured.NestedBool(bmh.Object, "spec", "online")
				g.Expect(readErr).NotTo(HaveOccurred())
				g.Expect(found).To(BeTrue(), "spec.online must be present")
				g.Expect(online).To(BeFalse(),
					"RunStrategy must not power on the host before network discovery")
				g.Expect(bmh.GetAnnotations()).NotTo(HaveKey("reboot.metal3.io"),
					"restartTrigger must not restart the host before network discovery")
			}, 5*time.Second, time.Second).Should(Succeed())

			By("completing the Fabric apply and waiting for BMF to power-cycle onto the tenant network")
			completeAAPJob(apply, "successful", nil)
			waitForBMAASHostOnline(fixture.host)
			Consistently(func(g Gomega) {
				g.Expect(getAAPTemplates()).NotTo(ContainElement(bmaasQueryTemplate),
					"the DHCP query must wait for NetworkHandoffComplete")
			}, 5*time.Second, time.Second).Should(Succeed())
			patchBMAASHostPower(fixture.host, true)

			By("querying DHCP only after BMF records NetworkHandoffComplete")
			query := waitForAAPTemplate(bmaasQueryTemplate)
			Eventually(func(g Gomega) {
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				condition := findNetworkingCondition(bmi, "NetworkHandoffComplete")
				g.Expect(condition).To(HaveKeyWithValue("status", "True"))
			}, readyTimeout, time.Second).Should(Succeed())
			completeAAPJob(query, "successful", map[string]any{
				"osac_result": map[string]any{"dhcp_lease": map[string]any{
					"ip_address": "192.0.2.25", "mac_address": bmaasSelectedMAC,
				}},
			})

			By("waiting until discovery completes before the single requested restart")
			waitForBMAASRebootAnnotation(fixture.host)
			bmi := getBMAASObject("BareMetalInstance", fixture.id)
			Expect(findNetworkingCondition(bmi, "IPDiscoveryComplete")).To(HaveKeyWithValue("status", "True"))
			Expect(findNetworkingCondition(bmi, "NetworkAttachmentsReady")).To(HaveKeyWithValue("status", "True"))
			Expect(findNetworkingCondition(bmi, "IPDiscoveryComplete")).To(HaveKeyWithValue("reason", "Succeeded"))
			patchBMAASHostRebootComplete(fixture.host)
			waitForBMAASPhase(fixture.id, "Ready")
			bmi = getBMAASObject("BareMetalInstance", fixture.id)
			restartTrigger, found, readErr := unstructured.NestedInt64(bmi.Object, "status", "restartTrigger")
			Expect(readErr).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "status.restartTrigger must be present")
			Expect(restartTrigger).To(Equal(int64(1)))
			statuses, found, statusErr := unstructured.NestedSlice(bmi.Object, "status", "networkAttachmentStatuses")
			Expect(statusErr).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "status.networkAttachmentStatuses must be present")
			Expect(statuses).To(HaveLen(1))
			status, ok := statuses[0].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(status).To(HaveKeyWithValue("ipAddress", "192.0.2.25"))
			Expect(getAAPTemplatesByName(bmaasApplyTemplate)).To(HaveLen(1))
			Expect(getAAPTemplatesByName(bmaasQueryTemplate)).To(HaveLen(1))
		})

		It("reports final Fabric apply failure on the BMF instance", func() {
			fixture := newBMAASFixture("apply-failure", "noop", "Always")
			waitForBMAASHostAssignment(fixture)
			waitForBMAASAttachment(fixture)
			apply := waitForAAPTemplate(bmaasApplyTemplate)
			completeAAPJob(apply, "failed", map[string]any{
				"osac_error": map[string]any{"message": "Fabric apply rejected", "retryable": false},
			})

			Eventually(func(g Gomega) {
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				condition := findNetworkingCondition(bmi, "NetworkAttachmentsReady")
				g.Expect(condition).To(HaveKeyWithValue("status", "False"))
				g.Expect(condition).To(HaveKeyWithValue("reason", "NetworkAttachmentFailed"))
				g.Expect(condition).To(HaveKeyWithValue("message", "Fabric apply rejected"))
				g.Expect(bmi.Object["status"]).To(HaveKeyWithValue("phase", "Failed"))
			}, readyTimeout, time.Second).Should(Succeed())
		})

		It("reports a terminal DHCP query failure while preserving NetworkAttachment readiness", func() {
			fixture := newBMAASFixture("query-failure", "noop", "Always")
			query := completeApplyAndWaitForQuery(fixture)
			completeAAPJob(query, "failed", map[string]any{
				"osac_error": map[string]any{"message": "DHCP query rejected", "retryable": false},
			})

			Eventually(func(g Gomega) {
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				networkReady := findNetworkingCondition(bmi, "NetworkAttachmentsReady")
				g.Expect(networkReady).To(HaveKeyWithValue("status", "True"))
				ipReady := findNetworkingCondition(bmi, "IPDiscoveryComplete")
				g.Expect(ipReady).To(HaveKeyWithValue("status", "False"))
				g.Expect(ipReady).To(HaveKeyWithValue("reason", "IPDiscoveryFailed"))
				g.Expect(ipReady).To(HaveKeyWithValue("message",
					fmt.Sprintf("DHCP query rejected; AAP job ID: %d", query.ID)))
				g.Expect(bmi.Object["status"]).To(HaveKeyWithValue("phase", "Failed"))
			}, readyTimeout, time.Second).Should(Succeed())
		})

		It("keeps a retryable DHCP query in LeasePending without clearing network readiness", func() {
			fixture := newBMAASFixture("query-retry", "noop", "Always")
			query := completeApplyAndWaitForQuery(fixture)
			completeAAPJob(query, "failed", map[string]any{
				"osac_error": map[string]any{"message": "DHCP service unavailable", "retryable": true},
			})

			Eventually(func(g Gomega) {
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				g.Expect(findNetworkingCondition(bmi, "NetworkAttachmentsReady")).To(HaveKeyWithValue("status", "True"))
				ipReady := findNetworkingCondition(bmi, "IPDiscoveryComplete")
				g.Expect(ipReady).To(HaveKeyWithValue("status", "False"))
				g.Expect(ipReady).To(HaveKeyWithValue("reason", "LeasePending"))
				g.Expect(ipReady["message"]).To(ContainSubstring("DHCP service unavailable"))
				g.Expect(bmi.Object["status"]).To(HaveKeyWithValue("phase", "Progressing"))
			}, readyTimeout, time.Second).Should(Succeed())
		})

		It("keeps BMF and OSAC cleanup finalizers while a Fabric delete fails", func() {
			fixture := newBMAASFixture("delete-failure", "noop", "Always")
			completeApplyAndLease(fixture)
			appendBMAASFinalizer(fixture.id, bmaasFeedbackFinalizer)
			_, err := kubectl("", "delete", resourceOf("BareMetalInstance"), fixture.id,
				"-n", operatorNamespace, "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			By("powering off the host before starting network cleanup")
			waitForBMAASHostOffline(fixture.host)
			patchBMAASHostPower(fixture.host, false)
			deleteJob := waitForAAPTemplate(bmaasDeleteTemplate)
			completeAAPJob(deleteJob, "failed", map[string]any{
				"osac_error": map[string]any{"message": "Fabric cleanup unavailable", "retryable": false},
			})

			Eventually(func(g Gomega) {
				attachment := getBMAASObject("NetworkAttachment", fixture.attachment)
				g.Expect(attachment.GetDeletionTimestamp()).NotTo(BeNil())
				g.Expect(attachment.GetFinalizers()).To(ContainElements(
					"osac.openshift.io/networkattachment", bmaasNetworkingFinalizer,
				))
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				g.Expect(bmi.GetFinalizers()).To(ContainElement(bmaasNetworkingFinalizer))
				g.Expect(bmi.GetFinalizers()[len(bmi.GetFinalizers())-1]).To(Equal(bmaasFeedbackFinalizer))
				bmh := getBMAASHost(fixture.host)
				consumer, found, mapErr := unstructured.NestedMap(bmh.Object, "spec", "consumerRef")
				g.Expect(mapErr).NotTo(HaveOccurred())
				g.Expect(found).To(BeTrue())
				g.Expect(consumer).To(HaveKeyWithValue("name", string(bmi.GetUID())))
			}, readyTimeout, time.Second).Should(Succeed())
		})

		It("removes network resources before leaving the feedback finalizer last", func() {
			fixture := newBMAASFixture("delete-order", "noop", "Always")
			completeApplyAndLease(fixture)
			appendBMAASFinalizer(fixture.id, bmaasFeedbackFinalizer)
			_, err := kubectl("", "delete", resourceOf("BareMetalInstance"), fixture.id,
				"-n", operatorNamespace, "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			waitForBMAASHostOffline(fixture.host)
			patchBMAASHostPower(fixture.host, false)
			deleteJob := waitForAAPTemplate(bmaasDeleteTemplate)
			completeAAPJob(deleteJob, "successful", nil)
			waitForObjectAbsent("NetworkAttachment", fixture.attachment)

			Eventually(func(g Gomega) {
				bmi := getBMAASObject("BareMetalInstance", fixture.id)
				g.Expect(bmi.GetFinalizers()).To(Equal([]string{bmaasFeedbackFinalizer}))
			}, readyTimeout, time.Second).Should(Succeed())
			Expect(getBMAASHost(fixture.host).Object["spec"]).NotTo(HaveKey("consumerRef"))
		})
	})

var _ = Describe("BMaaS networking disabled on deployed controllers",
	Ordered, Label(bmaasNetworkingDisabledLabel), func() {
		BeforeAll(func() {
			if os.Getenv("IT_TEST_SUITE") != "bmf" {
				Skip("requires the SUITE=bmf handoff phase")
			}
			Expect(operatorDeploymentEnv("OSAC_PROVISIONING_ENABLED")).To(Equal("false"),
				"the disabled case requires a fresh provider-networking-disabled install")
			Expect(bmfDeploymentEnv("OSAC_ENABLE_NETWORKING_PROVISIONING")).To(Equal("false"),
				"BMF and OSAC operator must share the disabled setting")
			Expect(operatorDeploymentEnv("OSAC_AAP_URL")).To(ContainSubstring("osac-aap-recorder"))
			installAAPRecorder()
		})

		It("runs host provisioning without network tasks and deprovisions without power-off", func() {
			resetAAPRecorder()
			fixture := newBMAASFixture("disabled", "test_template", "Always")
			waitForBMAASHostAssignment(fixture)
			provision := waitForAAPTemplate(bmaasProvisionTemplate)
			completeAAPJob(provision, "successful", nil)
			waitForBMAASHostOnline(fixture.host)
			patchBMAASHostPower(fixture.host, true)
			waitForBMAASPhase(fixture.id, "Ready")

			bmi := getBMAASObject("BareMetalInstance", fixture.id)
			condition := findNetworkingCondition(bmi, "NetworkAttachmentsReady")
			Expect(condition).To(HaveKeyWithValue("status", "True"))
			Expect(condition).To(HaveKeyWithValue("reason", "Skipped"))
			Expect(condition).To(HaveKeyWithValue("message", "Networking provisioning disabled"))
			// The omitempty status list may be absent when it has no entries.
			statuses, _, readErr := unstructured.NestedSlice(bmi.Object, "status", "networkAttachmentStatuses")
			Expect(readErr).NotTo(HaveOccurred())
			Expect(statuses).To(BeEmpty())
			absent, err := objectAbsent("NetworkAttachment", fixture.attachment)
			Expect(err).NotTo(HaveOccurred())
			Expect(absent).To(BeTrue())
			Expect(getAAPTemplatesByName(bmaasApplyTemplate)).To(BeEmpty())
			Expect(getAAPTemplatesByName(bmaasQueryTemplate)).To(BeEmpty())

			_, err = kubectl("", "delete", resourceOf("BareMetalInstance"), fixture.id,
				"-n", operatorNamespace, "--wait=false")
			Expect(err).NotTo(HaveOccurred())
			deprovision := waitForAAPTemplate(bmaasDeprovisionTemplate)
			Expect(getBMAASHost(fixture.host).Object["spec"]).To(HaveKeyWithValue("online", true),
				"disabled networking must not power off the host")
			completeAAPJob(deprovision, "successful", nil)
			waitForObjectAbsent("BareMetalInstance", fixture.id)
			Expect(getBMAASHost(fixture.host).Object["spec"]).To(HaveKeyWithValue("online", true))
			Expect(getAAPTemplatesByName(bmaasApplyTemplate)).To(BeEmpty())
			Expect(getAAPTemplatesByName(bmaasQueryTemplate)).To(BeEmpty())
		})
	})

func newBMAASFixture(prefix, templateID, runStrategy string) bmaasFixture {
	suffix := uniqueSuffix()
	id := "kind-" + prefix + "-" + suffix
	fixture := bmaasFixture{
		id:             id,
		host:           "host-" + suffix,
		virtualNetwork: "vn-" + suffix,
		subnet:         "subnet-" + suffix,
		attachment:     "baremetalinstance-" + id + "-data-0",
	}
	DeferCleanup(func() { cleanupBMAASFixture(fixture) })
	createReadyPhysicalNetwork(fixture)
	createBMAASHost(fixture)
	createBMAASInstance(fixture, templateID, runStrategy)
	return fixture
}

func createReadyPhysicalNetwork(fixture bmaasFixture) {
	virtualNetwork := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: VirtualNetwork
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/virtualnetwork-uuid: %s
  annotations:
    osac.openshift.io/tenant: %s
    osac.openshift.io/management-state: unmanaged
spec:
  ipv4Cidr: 192.0.2.0/24
`, fixture.virtualNetwork, operatorNamespace, fixture.virtualNetwork, bmaasTenant)
	_, err := kubectl(virtualNetwork, "create", "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	patchFixtureStatus("VirtualNetwork", fixture.virtualNetwork, `{
		"conditions": [{
			"type": "Ready",
			"status": "True",
			"reason": "Succeeded",
			"message": "fixture",
			"lastTransitionTime": "2026-01-01T00:00:00Z",
			"observedGeneration": 1
		}]
	}`)

	subnet := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Subnet
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/subnet-uuid: %s
  annotations:
    osac.openshift.io/tenant: %s
    osac.openshift.io/management-state: unmanaged
spec:
  virtualNetwork: %s
  ipv4Cidr: 192.0.2.0/24
  addressPlan:
    overlay: 0
    fabric: 16
    lbVip: 0
`, fixture.subnet, operatorNamespace, fixture.subnet, bmaasTenant, fixture.virtualNetwork)
	_, err = kubectl(subnet, "create", "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	patchFixtureStatus("Subnet", fixture.subnet, `{
		"conditions": [{
			"type": "Ready",
			"status": "True",
			"reason": "Succeeded",
			"message": "fixture",
			"lastTransitionTime": "2026-01-01T00:00:00Z",
			"observedGeneration": 1
		}],
		"networkManagers": {
			"fabric": {"name": "netris", "ansibleRole": "osac.templates.netris"}
		},
		"networkType": "evpn",
		"ipam": {
			"gatewayIpv4": "192.0.2.1",
			"physicalIpv4Cidrs": ["192.0.2.0/24"]
		}
	}`)
}

func patchFixtureStatus(kind, name, status string) {
	_, err := kubectl("", "patch", resourceOf(kind), name, "-n", operatorNamespace,
		"--type=merge", "--subresource=status", "-p", `{"status":`+status+`}`)
	Expect(err).NotTo(HaveOccurred())
}

func createBMAASHost(fixture bmaasFixture) {
	manifest := fmt.Sprintf(`apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/host-type: %s
    osac.openshift.io/managed-by: baremetal
  annotations:
    osac.openshift.io/interface-macs: '{"data-0":"%s"}'
spec:
  online: false
`, fixture.host, operatorNamespace, fixture.id, bmaasSelectedMAC)
	_, err := kubectl(manifest, "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	status := fmt.Sprintf(`{
		"operationalStatus": "OK",
		"poweredOn": false,
		"errorCount": 0,
		"errorMessage": "",
		"provisioning": {"ID": "", "state": "available"},
		"hardware": {"nics": [{
			"name": "data-0",
			"model": "virtio",
			"mac": %q,
			"speedGbps": 10,
			"pxe": true
		}]}
	}`, bmaasSelectedMAC)
	patchBMAASHostStatus(fixture.host, status)
}

func createBMAASInstance(fixture bmaasFixture, templateID, runStrategy string) {
	manifest := fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: BareMetalInstance
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/baremetalinstance-uuid: %s
  annotations:
    osac.openshift.io/tenant: %s
spec:
  selector:
    hostSelector:
      hostType: %s
  externalHostID: ""
  templateID: %s
  runStrategy: %s
  restartTrigger: 0
  networkAttachments:
    - subnetRef: %s
      securityGroupRefs:
        - sg-kind-it
      interface: data-0
      primary: true
`, fixture.id, operatorNamespace, fixture.id, bmaasTenant, fixture.id, templateID, runStrategy, fixture.subnet)
	_, err := kubectl(manifest, "create", "-f", "-")
	Expect(err).NotTo(HaveOccurred())
}

func patchBMAASHostStatus(name, status string) {
	_, err := kubectl("", "patch", "baremetalhost", name, "-n", operatorNamespace,
		"--type=merge", "--subresource=status", "-p", `{"status":`+status+`}`)
	Expect(err).NotTo(HaveOccurred())
}

func patchBMAASHostPower(name string, poweredOn bool) {
	patchBMAASHostStatus(name, fmt.Sprintf(`{"poweredOn":%t}`, poweredOn))
}

func patchBMAASHostRebootComplete(name string) {
	_, err := kubectl("", "patch", "baremetalhost", name, "-n", operatorNamespace,
		"--type=merge", "-p", `{"metadata":{"annotations":{"reboot.metal3.io":null}}}`)
	Expect(err).NotTo(HaveOccurred())
}

func patchBMAASInstance(id, patch string) {
	_, err := kubectl("", "patch", resourceOf("BareMetalInstance"), id, "-n", operatorNamespace,
		"--type=merge", "-p", patch)
	Expect(err).NotTo(HaveOccurred())
}

func appendBMAASFinalizer(id, finalizer string) {
	_, err := kubectl("", "patch", resourceOf("BareMetalInstance"), id, "-n", operatorNamespace,
		"--type=json", "-p", fmt.Sprintf(`[{"op":"add","path":"/metadata/finalizers/-","value":%q}]`, finalizer))
	Expect(err).NotTo(HaveOccurred())
}

func waitForBMAASHostAssignment(fixture bmaasFixture) {
	Eventually(func(g Gomega) {
		bmi := getBMAASObject("BareMetalInstance", fixture.id)
		hostID, found, err := unstructured.NestedString(bmi.Object, "spec", "externalHostID")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue(), "spec.externalHostID must be present")
		g.Expect(hostID).To(Equal(operatorNamespace + "/" + fixture.host))
	}, readyTimeout, time.Second).Should(Succeed())
}

func waitForBMAASAttachment(fixture bmaasFixture) *unstructured.Unstructured {
	var attachment *unstructured.Unstructured
	Eventually(func(g Gomega) {
		current, err := getObject("NetworkAttachment", fixture.attachment)
		g.Expect(err).NotTo(HaveOccurred())
		attachment = current
	}, readyTimeout, time.Second).Should(Succeed())
	return attachment
}

func waitForBMAASHostOnline(name string) {
	Eventually(func(g Gomega) {
		online, found, err := unstructured.NestedBool(getBMAASHost(name).Object, "spec", "online")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue(), "spec.online must be present")
		g.Expect(online).To(BeTrue())
	}, readyTimeout, time.Second).Should(Succeed())
}

func waitForBMAASHostOffline(name string) {
	Eventually(func(g Gomega) {
		online, found, err := unstructured.NestedBool(getBMAASHost(name).Object, "spec", "online")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue(), "spec.online must be present")
		g.Expect(online).To(BeFalse())
	}, readyTimeout, time.Second).Should(Succeed())
}

func waitForBMAASRebootAnnotation(name string) {
	Eventually(func(g Gomega) {
		g.Expect(getBMAASHost(name).GetAnnotations()).To(HaveKey("reboot.metal3.io"))
	}, readyTimeout, time.Second).Should(Succeed())
}

func waitForBMAASPhase(id, phase string) {
	Eventually(func(g Gomega) {
		bmi := getBMAASObject("BareMetalInstance", id)
		currentPhase, found, err := unstructured.NestedString(bmi.Object, "status", "phase")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(found).To(BeTrue(), "status.phase must be present")
		g.Expect(currentPhase).To(Equal(phase))
	}, readyTimeout, time.Second).Should(Succeed())
}

func completeApplyAndWaitForQuery(fixture bmaasFixture) aapCapture {
	waitForBMAASHostAssignment(fixture)
	waitForBMAASAttachment(fixture)
	apply := waitForAAPTemplate(bmaasApplyTemplate)
	completeAAPJob(apply, "successful", nil)
	waitForBMAASHostOnline(fixture.host)
	patchBMAASHostPower(fixture.host, true)
	return waitForAAPTemplate(bmaasQueryTemplate)
}

func completeApplyAndLease(fixture bmaasFixture) {
	query := completeApplyAndWaitForQuery(fixture)
	completeAAPJob(query, "successful", map[string]any{
		"osac_result": map[string]any{"dhcp_lease": map[string]any{
			"ip_address": "192.0.2.25", "mac_address": bmaasSelectedMAC,
		}},
	})
	waitForBMAASPhase(fixture.id, "Ready")
}

func getBMAASHost(name string) *unstructured.Unstructured {
	output, err := runKubectl("get", "baremetalhost", name, "-n", operatorNamespace, "-o", "json")
	Expect(err).NotTo(HaveOccurred())
	obj := &unstructured.Unstructured{}
	Expect(json.Unmarshal(output, &obj.Object)).To(Succeed())
	return obj
}

func getBMAASObject(kind, name string) *unstructured.Unstructured {
	obj, err := getObject(kind, name)
	Expect(err).NotTo(HaveOccurred())
	return obj
}

func waitForObjectAbsent(kind, name string) {
	Eventually(func(g Gomega) {
		absent, err := objectAbsent(kind, name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(absent).To(BeTrue())
	}, readyTimeout, time.Second).Should(Succeed())
}

func cleanupBMAASFixture(fixture bmaasFixture) {
	for _, kindName := range [][2]string{
		{"BareMetalInstance", fixture.id},
		{"NetworkAttachment", fixture.attachment},
	} {
		resource, name := kindName[0], kindName[1]
		_, _ = kubectl("", "delete", resourceOf(resource), name, "-n", operatorNamespace,
			"--ignore-not-found", "--wait=false")
		// Set deletionTimestamp before bypassing finalizers. Clearing them first
		// lets the active controller reconcile the still-live object and add them
		// again before the delete arrives.
		_, _ = kubectl("", "patch", resourceOf(resource), name, "-n", operatorNamespace,
			"--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
		waitForObjectAbsent(resource, name)
	}
	_, err := kubectl(
		"", "delete", "baremetalhost", fixture.host, "-n", operatorNamespace,
		"--ignore-not-found", "--wait=true",
	)
	Expect(err).NotTo(HaveOccurred(), "delete BareMetalHost fixture %s", fixture.host)
	_, err = kubectl(
		"", "delete", resourceOf("Subnet"), fixture.subnet, "-n", operatorNamespace,
		"--ignore-not-found", "--wait=true",
	)
	Expect(err).NotTo(HaveOccurred(), "delete Subnet fixture %s", fixture.subnet)
	_, err = kubectl(
		"", "delete", resourceOf("VirtualNetwork"), fixture.virtualNetwork,
		"-n", operatorNamespace, "--ignore-not-found", "--wait=true",
	)
	Expect(err).NotTo(HaveOccurred(), "delete VirtualNetwork fixture %s", fixture.virtualNetwork)
	resetAAPRecorder()
}

func bmfDeploymentEnv(name string) string {
	output, err := runKubectl("get", "deployment", "bmf-operator-controller-manager", "-n", operatorNamespace,
		"-o", "json")
	Expect(err).NotTo(HaveOccurred())
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Env []struct {
							Name, Value string
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	Expect(json.Unmarshal(output, &deployment)).To(Succeed())
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, variable := range container.Env {
			if variable.Name == name {
				return variable.Value
			}
		}
	}
	return ""
}

func resetAAPRecorder() {
	_, err := postAAPRecorder("reset", []byte(`{}`))
	Expect(err).NotTo(HaveOccurred(), "reset the test AAP recorder")
}

func waitForAAPTemplate(template string) aapCapture {
	return waitForAAPCapture(func(job aapCapture) bool {
		return job.Template == template
	}, "the operator must call only supported AAP endpoints")
}

func completeAAPJob(job aapCapture, state string, artifacts map[string]any) {
	if artifacts == nil {
		artifacts = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"status": state, "artifacts": artifacts})
	Expect(err).NotTo(HaveOccurred())
	output, err := postAAPRecorder(fmt.Sprintf("complete/%d", job.ID), body)
	Expect(err).NotTo(HaveOccurred(), "complete the captured AAP job: %s", output)
}

func postAAPRecorder(path string, body []byte) ([]byte, error) {
	script := `
import sys
import urllib.request

request = urllib.request.Request(
	"http://127.0.0.1:8080/" + sys.argv[1],
	data=sys.stdin.buffer.read(),
	headers={"Content-Type": "application/json"},
	method="POST",
)
print(urllib.request.urlopen(request).read().decode())
`
	return runRecorderKubectl(string(body), "exec", "-i", "-n", operatorNamespace, aapRecorderName,
		"--", "python3", "-c", script, path)
}

func getAAPTemplates() []string {
	captures, err := readAAPCaptures()
	Expect(err).NotTo(HaveOccurred())
	templates := make([]string, 0, len(captures.Jobs))
	for _, job := range captures.Jobs {
		templates = append(templates, job.Template)
	}
	return templates
}

func getAAPTemplatesByName(template string) []aapCapture {
	captures, err := readAAPCaptures()
	Expect(err).NotTo(HaveOccurred())
	var matches []aapCapture
	for _, job := range captures.Jobs {
		if job.Template == template {
			matches = append(matches, job)
		}
	}
	return matches
}
