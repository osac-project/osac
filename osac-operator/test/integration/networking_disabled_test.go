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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The networking controllers of the deployed operator with provider
// networking disabled (global.networking.provisioningEnabled=false, rendered
// as OSAC_PROVISIONING_ENABLED=false; what CI deploys). The NetworkClass is
// Ready without manager resolution. Unified design, "Provider networking
// disabled": VirtualNetwork, Subnet, SecurityGroup,
// ExternalIPPool and FabricDomain become Ready=True ProvisioningDisabled
// without any job, ExternalIP, ExternalIPAttachment, NATGateway and
// NetworkAttachment never exist in that mode and fail, and deletes still
// follow the dependency order and release their finalizers.
//
// The enabled path (status.jobs, AAP launches, retries, accepted results) is
// covered by envtest with an in-memory AAP (internal/controller/networking_*_test.go
// and internal/networktask); switching the deployed operator to enabled mode
// would need a reachable AAP, so this suite does not do it.

const (
	finalizerVirtualNetwork       = "osac.openshift.io/virtualnetwork-finalizer"
	finalizerSubnet               = "osac.openshift.io/subnet-finalizer"
	finalizerSecurityGroup        = "osac.openshift.io/securitygroup-finalizer"
	finalizerExternalIPPool       = "osac.openshift.io/externalippool-finalizer"
	finalizerExternalIP           = "osac.openshift.io/externalip-finalizer"
	finalizerExternalIPAttachment = "osac.openshift.io/externalipattachment-finalizer"
	finalizerNATGateway           = "osac.openshift.io/natgateway-finalizer"
	finalizerFabricDomain         = "osac.openshift.io/fabricdomain-finalizer"
	finalizerNetworkAttachment    = "osac.openshift.io/networkattachment"
	networkClassDisabledMessage   = "Provider networking is disabled; manager resolution was skipped."
)

func disabledMessage(task string) string { return fmt.Sprintf(disabledMessageFormat, task) }

var _ = Describe("Provider networking disabled "+
	"on the deployed operator", Ordered, Label("networking", "disabled"), func() {
	var (
		sfx string
	)

	// managed builds a networking CR with its API ID label (name = API ID).
	managed := func(kind, name string, spec map[string]any) *unstructured.Unstructured {
		return withAPIID(newObject(kind, name, spec), name)
	}
	virtualNetwork := func(name, cidr string) *unstructured.Unstructured {
		return managed("VirtualNetwork", name, map[string]any{"ipv4Cidr": cidr})
	}
	subnet := func(name, vn, cidr string, plan map[string]any) *unstructured.Unstructured {
		return managed("Subnet", name, map[string]any{"virtualNetwork": vn, "ipv4Cidr": cidr, "addressPlan": plan})
	}
	securityGroup := func(name, vn string) *unstructured.Unstructured {
		return managed("SecurityGroup", name, map[string]any{
			"virtualNetwork": vn,
			"ingressRules": []any{
				map[string]any{"protocol": "tcp", "portFrom": int64(22), "portTo": int64(22), "sourceCidr": "10.0.0.0/8"},
			},
			"egressRules": []any{map[string]any{"protocol": "all", "destinationCidr": "0.0.0.0/0"}},
		})
	}
	fabricDomain := func(name, vn string) *unstructured.Unstructured {
		return managed(
			"FabricDomain",
			name,
			map[string]any{"type": "ethernet_ew", "servers": []any{"bmi-" + sfx}, "virtualNetwork": vn},
		)
	}
	networkAttachment := func(subnetID, sgID string) *unstructured.Unstructured {
		id := "ci-" + uniqueSuffix()
		return newObject("NetworkAttachment", "computeinstance-"+id, map[string]any{
			"target":         map[string]any{"kind": "ComputeInstance", "id": id},
			"subnet":         subnetID,
			"interface":      map[string]any{"type": "overlay"},
			"securityGroups": []any{sgID},
		})
	}

	// expectManagers checks status.networkManagers and status.networkType.
	expectManagers := func(obj *unstructured.Unstructured, managers map[string]any, networkType string) {
		got, _, _ := unstructured.NestedMap(obj.Object, "status", "networkManagers")
		if managers == nil {
			Expect(got).To(BeEmpty(), "%s %s must not resolve manager roles", obj.GetKind(), obj.GetName())
		} else {
			Expect(got).To(Equal(managers), "%s %s manager status", obj.GetKind(), obj.GetName())
		}
		gotType, _, _ := unstructured.NestedString(obj.Object, "status", "networkType")
		Expect(gotType).To(Equal(networkType), "%s %s network type", obj.GetKind(), obj.GetName())
	}
	// expectDisabledReady waits for Ready=True ProvisioningDisabled and checks
	// that no job ran, the finalizer is set and the task input was stored.
	expectDisabledReady := func(kind, name, task, finalizer string) *unstructured.Unstructured {
		obj := eventuallyReady(kind, name, "True", "ProvisioningDisabled", disabledMessage(task))
		expectNoJobs(obj)
		Expect(obj.GetFinalizers()).To(ContainElement(finalizer))
		input := taskInput(obj)
		_, _ = fmt.Fprintf(GinkgoWriter, "%s %s taskInput: %s\n", kind, name, mustJSON(input))
		resource, _, _ := unstructured.NestedMap(input, "resource")
		Expect(resource).To(HaveKeyWithValue("kind", kind))
		Expect(resource).To(HaveKeyWithValue("id", name), "the snapshot carries the API ID label")
		return obj
	}
	// expectDisabledFailed waits for Ready=False Failed "provider networking is
	// disabled" and checks that nothing was stored or run.
	expectDisabledFailed := func(kind, name, finalizer string) {
		obj := eventuallyReady(kind, name, "False", "Failed", networkingDisabledMessage)
		expectNoJobs(obj)
		Expect(obj.GetFinalizers()).To(ContainElement(finalizer))
		status, _, _ := unstructured.NestedMap(obj.Object, "status")
		Expect(status).NotTo(HaveKey("taskInput"), "a CR that never exists in this mode stores no input: %v", status)
	}

	BeforeAll(func() {
		sfx = uniqueSuffix()
		By("checking that the deployed operator runs with provider networking disabled")
		Expect(operatorDeploymentEnv("OSAC_PROVISIONING_ENABLED")).To(Equal("false"),
			"CI deploys global.networking.provisioningEnabled=false (values/dev/kind-instance.yaml)")
		Expect(listNames("NetworkClass")).To(BeEmpty(),
			"the networking controllers need exactly one Ready NetworkClass; leftover NetworkClasses break this suite")

		By("creating a VirtualNetwork before any NetworkClass exists: it waits")
		early := "vn-early-" + sfx
		mustCreate(virtualNetwork(early, "10.199.0.0/16"))
		eventuallyReady("VirtualNetwork", early, "False", "Progressing", "waiting for the NetworkClass to be READY")

		By("creating the deployment's NetworkClass without manager registrations")
		nc := "nc-disabled-" + sfx
		createNetworkClass(nc, "example_fabric", "example_k8s", false)
		networkClass := eventuallyReady(
			"NetworkClass", nc, "True", "ProvisioningDisabled", networkClassDisabledMessage,
		)
		managers, _, _ := unstructured.NestedMap(networkClass.Object, "status", "networkManagers")
		Expect(managers).To(BeEmpty(), "disabled provisioning must not resolve manager roles")
		networkType, _, _ := unstructured.NestedString(networkClass.Object, "status", "networkType")
		Expect(networkType).To(BeEmpty(), "disabled provisioning must not resolve a network type")
		expectNoJobs(networkClass)

		By("the waiting VirtualNetwork proceeds once the NetworkClass is Ready")
		vn := expectDisabledReady("VirtualNetwork", early, "create_virtual_network", finalizerVirtualNetwork)
		expectManagers(vn, nil, "")
		deleteAndWaitGone("VirtualNetwork", early)
	})

	It("TC-DISABLED-01: VirtualNetwork, Subnet and SecurityGroup "+
		"become Ready=True ProvisioningDisabled without a job", func() {
		vnID, subnetID, sgID := "vn-"+sfx+"-a", "sn-"+sfx+"-a", "sg-"+sfx+"-a"
		mustCreate(virtualNetwork(vnID, "10.200.0.0/16"))
		vn := expectDisabledReady("VirtualNetwork", vnID, "create_virtual_network", finalizerVirtualNetwork)
		expectManagers(vn, nil, "")
		Expect(taskInput(vn)).To(HaveKeyWithValue("tenant", networkingTestTenant))

		By("creating a Subnet with the plan 9/4/3 sixteenths (0.5625/0.25/0.1875) on a /24 (TC-IPAM-01)")
		mustCreate(subnet(subnetID, vnID, "10.200.1.0/24",
			map[string]any{"overlay": int64(9), "fabric": int64(4), "lbVip": int64(3)}))
		sn := expectDisabledReady("Subnet", subnetID, "create_subnet", finalizerSubnet)
		expectManagers(sn, nil, "")
		ipam, _, _ := unstructured.NestedMap(sn.Object, "status", "ipam")
		Expect(ipam).To(Equal(map[string]any{
			"gatewayIpv4":       "10.200.1.1",
			"overlayIpv4Cidrs":  []any{"10.200.1.0/25", "10.200.1.128/28"},
			"physicalIpv4Cidrs": []any{"10.200.1.144/28", "10.200.1.160/27", "10.200.1.192/28"},
			"lbVipIpv4Cidrs":    []any{"10.200.1.208/28", "10.200.1.224/27"},
		}), "status.ipam must follow contract §4.8 even with provisioning disabled")
		input := taskInput(sn)
		Expect(input).To(HaveKey("ipam"))
		Expect(input).To(HaveKey("virtual_network"))
		Expect(input["virtual_network"]).To(HaveKeyWithValue("id", vnID))

		By("creating a SecurityGroup")
		mustCreate(securityGroup(sgID, vnID))
		sg := expectDisabledReady("SecurityGroup", sgID, "create_security_group", finalizerSecurityGroup)
		expectManagers(sg, nil, "")

		By(
			"updating the SecurityGroup rules: the new rules are stored, no job runs and the reason stays ProvisioningDisabled",
		)
		expectPatchAccepted("SecurityGroup", sgID,
			`{"spec":{"ingressRules":[{"protocol":"udp","portFrom":53,"portTo":53,"sourceCidr":"192.168.77.0/24"}]}}`)
		Eventually(func(g Gomega) {
			current, err := getObject("SecurityGroup", sgID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(mustJSON(current.Object["status"])).To(ContainSubstring("192.168.77.0/24"),
				"status.taskInput must hold the new rules")
			ready := readyCondition(current)
			g.Expect(ready).To(HaveKeyWithValue("status", "True"))
			g.Expect(ready).To(HaveKeyWithValue("reason", "ProvisioningDisabled"))
			expectNoJobsG(g, current)
		}, readyTimeout, time.Second).Should(Succeed(), func() string {
			logObject("SecurityGroup", sgID)
			return "the SecurityGroup rule update was not stored"
		})

		deleteAndWaitGone("SecurityGroup", sgID)
		deleteAndWaitGone("Subnet", subnetID)
		deleteAndWaitGone("VirtualNetwork", vnID)
	})

	It("TC-DISABLED-02: a child waits for its parent VirtualNetwork and proceeds once it is Ready", func() {
		vnID, subnetID := "vn-"+sfx+"-b", "sn-"+sfx+"-b"
		mustCreate(subnet(subnetID, vnID, "10.201.1.0/24", validAddressPlan()))
		eventuallyReady(
			"Subnet",
			subnetID,
			"False",
			"Progressing",
			fmt.Sprintf("waiting for VirtualNetwork %s to be READY", vnID),
		)
		Consistently(func(g Gomega) {
			current, err := getObject("Subnet", subnetID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(readyCondition(current)).To(HaveKeyWithValue("reason", "Progressing"))
			g.Expect(current.Object["status"]).NotTo(HaveKey("taskInput"), "no input before the parent is Ready")
		}, 12*time.Second, 2*time.Second).Should(Succeed())

		mustCreate(virtualNetwork(vnID, "10.201.0.0/16"))
		expectDisabledReady("VirtualNetwork", vnID, "create_virtual_network", finalizerVirtualNetwork)
		sn := expectDisabledReady("Subnet", subnetID, "create_subnet", finalizerSubnet)
		expectManagers(sn, nil, "")

		deleteAndWaitGone("Subnet", subnetID)
		deleteAndWaitGone("VirtualNetwork", vnID)
	})

	It("TC-DISABLED-03: ExternalIPPool records only the Fabric Manager "+
		"and becomes Ready=True ProvisioningDisabled", func() {
		poolID := "pool-" + sfx
		mustCreate(managed("ExternalIPPool", poolID, map[string]any{"cidrs": []any{"192.0.2.0/24"}, "ipFamily": "IPv4"}))
		pool := expectDisabledReady("ExternalIPPool", poolID, "create_external_ip_pool", finalizerExternalIPPool)
		expectManagers(pool, nil, "")
		Expect(taskInput(pool)).NotTo(HaveKey("tenant"), "an ExternalIPPool is not tenant-owned")
		status, _, _ := unstructured.NestedMap(pool.Object, "status")
		for _, removed := range []string{"total", "allocated", "available"} {
			Expect(status).NotTo(HaveKey(removed), "pool usage counts are computed by fulfillment (decision 21)")
		}
		deleteAndWaitGone("ExternalIPPool", poolID)
	})

	It("TC-DISABLED-04: FabricDomain becomes Ready=True ProvisioningDisabled without an east-west check", func() {
		vnID, fdID := "vn-"+sfx+"-d", "fd-"+sfx
		mustCreate(virtualNetwork(vnID, "10.204.0.0/16"))
		expectDisabledReady("VirtualNetwork", vnID, "create_virtual_network", finalizerVirtualNetwork)
		mustCreate(fabricDomain(fdID, vnID))
		fd := expectDisabledReady("FabricDomain", fdID, "create_fabric_domain", finalizerFabricDomain)
		expectManagers(fd, nil, "")
		resource, _, _ := unstructured.NestedMap(taskInput(fd), "resource", "spec")
		Expect(resource).To(HaveKeyWithValue("servers", []any{"bmi-" + sfx}))
		deleteAndWaitGone("FabricDomain", fdID)
		deleteAndWaitGone("VirtualNetwork", vnID)
	})

	It("TC-DISABLED-05: ExternalIP, ExternalIPAttachment, NATGateway and NetworkAttachment fail and still delete", func() {
		eipID, eiaID, natID := "eip-"+sfx, "eia-"+sfx, "nat-"+sfx
		mustCreate(managed("ExternalIP", eipID, map[string]any{"pool": "pool-" + sfx}))
		mustCreate(
			managed("ExternalIPAttachment", eiaID, map[string]any{"externalIP": eipID, "computeInstance": "ci-" + sfx}),
		)
		mustCreate(managed("NATGateway", natID, map[string]any{"virtualNetwork": "vn-" + sfx, "externalIP": eipID}))
		na := networkAttachment("sn-"+sfx, "sg-"+sfx)
		mustCreate(na)

		expectDisabledFailed("ExternalIP", eipID, finalizerExternalIP)
		expectDisabledFailed("ExternalIPAttachment", eiaID, finalizerExternalIPAttachment)
		expectDisabledFailed("NATGateway", natID, finalizerNATGateway)
		expectDisabledFailed("NetworkAttachment", na.GetName(), finalizerNetworkAttachment)

		By("deleting the ExternalIP first: it waits for its attachment and NAT gateway")
		_, err := kubectl("", "delete", resourceOf("ExternalIP"), eipID, "-n", operatorNamespace, "--wait=false")
		Expect(err).NotTo(HaveOccurred())
		expectDeleteHeld("ExternalIP", eipID, finalizerExternalIP, 15*time.Second,
			"an ExternalIPAttachment and a NATGateway still use it")
		deleteAndWaitGone("ExternalIPAttachment", eiaID)
		expectDeleteHeld("ExternalIP", eipID, finalizerExternalIP, 12*time.Second, "a NATGateway still uses it")
		deleteAndWaitGone("NATGateway", natID)
		Eventually(
			func() (bool, error) { return objectAbsent("ExternalIP", eipID) },
			readyTimeout,
			time.Second,
		).Should(BeTrue(),
			"the ExternalIP delete completes once nothing uses it")
		deleteAndWaitGone("NetworkAttachment", na.GetName())
	})

	It("TC-DISABLED-06: deletes follow the dependency order and every finalizer is released without a job", func() {
		vnID, subnetID, sgID, fdID := "vn-"+sfx+"-f", "sn-"+sfx+"-f", "sg-"+sfx+"-f", "fd-"+sfx+"-f"
		mustCreate(virtualNetwork(vnID, "10.206.0.0/16"))
		expectDisabledReady("VirtualNetwork", vnID, "create_virtual_network", finalizerVirtualNetwork)
		mustCreate(subnet(subnetID, vnID, "10.206.1.0/24", validAddressPlan()))
		mustCreate(securityGroup(sgID, vnID))
		mustCreate(fabricDomain(fdID, vnID))
		expectDisabledReady("Subnet", subnetID, "create_subnet", finalizerSubnet)
		expectDisabledReady("SecurityGroup", sgID, "create_security_group", finalizerSecurityGroup)
		expectDisabledReady("FabricDomain", fdID, "create_fabric_domain", finalizerFabricDomain)
		na := networkAttachment(subnetID, sgID)
		mustCreate(na)
		expectDisabledFailed("NetworkAttachment", na.GetName(), finalizerNetworkAttachment)

		By("deleting the VirtualNetwork while its Subnet, SecurityGroup and FabricDomain exist")
		_, err := kubectl("", "delete", resourceOf("VirtualNetwork"), vnID, "-n", operatorNamespace, "--wait=false")
		Expect(err).NotTo(HaveOccurred())
		expectDeleteHeld("VirtualNetwork", vnID, finalizerVirtualNetwork, 25*time.Second, "children still exist")

		By("deleting the Subnet and SecurityGroup while a NetworkAttachment uses them")
		for _, kind := range []string{"Subnet", "SecurityGroup"} {
			id := map[string]string{"Subnet": subnetID, "SecurityGroup": sgID}[kind]
			_, err := kubectl("", "delete", resourceOf(kind), id, "-n", operatorNamespace, "--wait=false")
			Expect(err).NotTo(HaveOccurred())
		}
		expectDeleteHeld("Subnet", subnetID, finalizerSubnet, 15*time.Second, "a NetworkAttachment uses it")
		expectDeleteHeld("SecurityGroup", sgID, finalizerSecurityGroup, 1*time.Second, "a NetworkAttachment uses it")

		By("deleting the NetworkAttachment releases the Subnet and SecurityGroup")
		deleteAndWaitGone("NetworkAttachment", na.GetName())
		Eventually(
			func() (bool, error) { return objectAbsent("Subnet", subnetID) },
			readyTimeout,
			time.Second,
		).Should(BeTrue())
		Eventually(
			func() (bool, error) { return objectAbsent("SecurityGroup", sgID) },
			readyTimeout,
			time.Second,
		).Should(BeTrue())
		expectDeleteHeld("VirtualNetwork", vnID, finalizerVirtualNetwork, 12*time.Second, "the FabricDomain still exists")

		By("deleting the FabricDomain releases the VirtualNetwork")
		deleteAndWaitGone("FabricDomain", fdID)
		Eventually(
			func() (bool, error) { return objectAbsent("VirtualNetwork", vnID) },
			readyTimeout,
			time.Second,
		).Should(BeTrue(),
			func() string {
				logObject("VirtualNetwork", vnID)
				return "the VirtualNetwork delete completes once its children are gone"
			})
	})
})
