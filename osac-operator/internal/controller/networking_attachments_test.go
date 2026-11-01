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

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

func (h *netHarness) readyPool(id string) *v1alpha1.ExternalIPPool {
	pool := &v1alpha1.ExternalIPPool{ObjectMeta: h.meta(kindExternalIPPool, id),
		Spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"198.51.100.0/24"}, IPFamily: "IPv4"}}
	Expect(k8sClient.Create(ctx, pool)).To(Succeed())
	return markReady(pool, false, nil)
}

func (h *netHarness) allocatedIP(id, poolID, address string) *v1alpha1.ExternalIP {
	eip := &v1alpha1.ExternalIP{ObjectMeta: h.meta(kindExternalIP, id), Spec: v1alpha1.ExternalIPSpec{Pool: poolID}}
	Expect(k8sClient.Create(ctx, eip)).To(Succeed())
	return markReady(eip, false, func(e *v1alpha1.ExternalIP) { e.Status.Address = address })
}

// computeInstance creates a ComputeInstance with an API ID on Subnet subnetID.
func (h *netHarness) computeInstance(id, subnetID, address string) *v1alpha1.ComputeInstance {
	ci := &v1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{Name: crdTestName("ci"), Namespace: h.ns, Labels: map[string]string{osacComputeInstanceIDLabel: id}},
		Spec:       newTestComputeInstanceSpec("tpl"),
	}
	ci.Spec.NetworkAttachments = []v1alpha1.ComputeNetworkAttachment{{SubnetRef: subnetID, SecurityGroupRefs: []string{"sg-1"}}}
	Expect(k8sClient.Create(ctx, ci)).To(Succeed())
	if address != "" {
		h.setVMAddress(ci, subnetID, address)
	}
	return ci
}

func (h *netHarness) setVMAddress(ci *v1alpha1.ComputeInstance, subnetID, address string) {
	ci.Status.ComputeNetworkAttachmentStatuses = []v1alpha1.ComputeNetworkAttachmentStatus{{SubnetRef: subnetID, IPAddress: address}}
	Expect(k8sClient.Status().Update(ctx, ci)).To(Succeed())
}

func (h *netHarness) bareMetalInstance(id, hostName string) *bmfov1alpha1.BareMetalInstance {
	bmi := &bmfov1alpha1.BareMetalInstance{
		ObjectMeta: metav1.ObjectMeta{Name: crdTestName("bmi"), Namespace: h.ns, Labels: map[string]string{osacBareMetalInstanceIDLabel: id}},
		Spec: bmfov1alpha1.BareMetalInstanceSpec{
			ExternalHostID: "host-1", ExternalHostName: hostName, TemplateID: "tpl",
			Selector: bmfov1alpha1.HostSelectorSpec{HostSelector: map[string]string{"pool": "a"}},
		},
	}
	Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
	return bmi
}

var _ = Describe("ExternalIPPool and ExternalIP controllers", func() {
	var (
		h     *netHarness
		pools *ExternalIPPoolReconciler
		eips  *ExternalIPReconciler
	)
	BeforeEach(func() {
		h = newNetHarness(true)
		pools = &ExternalIPPoolReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		eips = &ExternalIPReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
	})

	It("creates a pool on the Fabric Manager only, without tenant, and deletes it after its ExternalIPs", func() {
		h.networkClass(true, false)
		pool := &v1alpha1.ExternalIPPool{ObjectMeta: h.meta(kindExternalIPPool, "pool-1"),
			Spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"198.51.100.0/24"}, IPFamily: "IPv4"}}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		reconcileNet(pools, pool)
		Expect(pool.Status.NetworkManagers).To(Equal(testManagers(false)))
		launch := h.aap.latest(networktask.CreateExternalIPPool, netFabric)
		Expect(launch.vars).NotTo(HaveKey("tenant"))
		Expect(path(launch.vars, "resource", "spec")).To(Equal(map[string]any{"cidrs": []any{"198.51.100.0/24"}}))
		h.aap.succeed(networktask.CreateExternalIPPool, netFabric)
		reconcileNet(pools, pool)
		expectReady(pool, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		eip := h.allocatedIP("eip-1", "pool-1", "198.51.100.10")
		deleteNet(pool)
		reconcileNet(pools, pool)
		Expect(h.aap.tasks("pool-1")).To(HaveLen(1))
		Expect(k8sClient.Delete(ctx, eip)).To(Succeed())
		reconcileNet(pools, pool)
		h.aap.succeed(networktask.DeleteExternalIPPool, netFabric)
		_, exists := reconcileNet(pools, pool)
		Expect(exists).To(BeFalse())
	})

	It("accepts an eligible address and records the state transition", func() {
		h.readyPool("pool-1")
		eip := &v1alpha1.ExternalIP{ObjectMeta: h.meta(kindExternalIP, "eip-1"), Spec: v1alpha1.ExternalIPSpec{Pool: "pool-1"}}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		reconcileNet(eips, eip)
		launch := h.aap.latest(networktask.CreateExternalIP, netFabric)
		Expect(path(launch.vars, "resource", "spec")).To(Equal(map[string]any{"pool": "pool-1"}))
		Expect(path(launch.vars, "external_ip_pool", "spec")).To(Equal(map[string]any{"cidrs": []any{"198.51.100.0/24"}}))
		Expect(eip.Status.StateTransitionTime).NotTo(BeNil())
		h.aap.finish(networktask.CreateExternalIP, netFabric, "successful", `{"osac_result": {"external_ip": {"address": "198.51.100.19"}}}`)
		reconcileNet(eips, eip)
		Expect(eip.Status.Address).To(Equal("198.51.100.19"))
		expectReady(eip, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})

	It("rejects the pool's broadcast address", func() {
		h.readyPool("pool-1")
		eip := &v1alpha1.ExternalIP{ObjectMeta: h.meta(kindExternalIP, "eip-1"), Spec: v1alpha1.ExternalIPSpec{Pool: "pool-1"}}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		reconcileNet(eips, eip)
		h.aap.finish(networktask.CreateExternalIP, netFabric, "successful", `{"osac_result": {"external_ip": {"address": "198.51.100.255"}}}`)
		reconcileNet(eips, eip)
		expectReady(eip, metav1.ConditionFalse, v1alpha1.ReasonFailed)
		Expect(eip.Status.Address).To(BeEmpty())
	})

	It("fails an ExternalIP with provider networking disabled and deletes it without a task", func() {
		h = newNetHarness(false)
		eips = &ExternalIPReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		eip := &v1alpha1.ExternalIP{ObjectMeta: h.meta(kindExternalIP, "eip-1"), Spec: v1alpha1.ExternalIPSpec{Pool: "pool-1"}}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		reconcileNet(eips, eip)
		Expect(expectReady(eip, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal("provider networking is disabled"))
		deleteNet(eip)
		_, exists := reconcileNet(eips, eip)
		Expect(exists).To(BeFalse())
	})
})

var _ = Describe("ExternalIPAttachment and NATGateway controllers", func() {
	var (
		h          *netHarness
		eipas      *ExternalIPAttachmentReconciler
		ciID, clID string
	)
	BeforeEach(func() {
		h = newNetHarness(true)
		ciID, clID = h.ns+"-ci", h.ns+"-cl"
		eipas = &ExternalIPAttachmentReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.readyPool("pool-1")
		h.allocatedIP("eip-1", "pool-1", "198.51.100.19")
		h.readyVirtualNetwork("vn-1", false)
		h.readySubnet("sn-1", "vn-1", false, testPlan)
	})

	attachment := func(mutate func(*v1alpha1.ExternalIPAttachmentSpec)) *v1alpha1.ExternalIPAttachment {
		eipa := &v1alpha1.ExternalIPAttachment{ObjectMeta: h.meta(kindExternalIPAttachment, "eipa-1"),
			Spec: v1alpha1.ExternalIPAttachmentSpec{ExternalIP: "eip-1"}}
		mutate(&eipa.Spec)
		Expect(k8sClient.Create(ctx, eipa)).To(Succeed())
		return eipa
	}

	It("waits for the VM address, then attaches to the VM on its VirtualNetwork", func() {
		ci := h.computeInstance(ciID, "sn-1", "")
		eipa := attachment(func(s *v1alpha1.ExternalIPAttachmentSpec) { s.ComputeInstance = ptr.To(ciID) })
		reconcileNet(eipas, eipa)
		Expect(expectReady(eipa, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal(msgWaitingForEndpoint))
		Expect(h.aap.tasks("")).To(BeEmpty())

		h.setVMAddress(ci, "sn-1", "10.0.1.17")
		reconcileNet(eipas, eipa)
		launch := h.aap.latest(networktask.AttachExternalIP, netFabric)
		Expect(launch.vars["destination"]).To(Equal(map[string]any{"address": "10.0.1.17",
			"routing_domain": map[string]any{"type": "tenant_virtual_network", "virtual_network_id": "vn-1", "subnet_id": "sn-1"}}))
		Expect(launch.vars["external_ip"]).To(Equal(map[string]any{"id": "eip-1", "address": "198.51.100.19"}))
		Expect(path(launch.vars, "resource", "spec")).To(Equal(map[string]any{}))
		Expect(eipa.Status.Destination).To(Equal(&v1alpha1.ExternalIPAttachmentDestination{Address: "10.0.1.17",
			RoutingDomain: v1alpha1.ExternalIPAttachmentRoutingDomain{Type: v1alpha1.RoutingDomainTenantVirtualNetwork, VirtualNetworkID: "vn-1", SubnetID: "sn-1"}}))
		h.aap.succeed(networktask.AttachExternalIP, netFabric)
		reconcileNet(eipas, eipa)
		expectReady(eipa, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		deleteNet(eipa)
		reconcileNet(eipas, eipa)
		Expect(h.aap.latest(networktask.DetachExternalIP, netFabric).vars["destination"]).To(Equal(launch.vars["destination"]))
		h.aap.succeed(networktask.DetachExternalIP, netFabric)
		_, exists := reconcileNet(eipas, eipa)
		Expect(exists).To(BeFalse())
	})

	It("attaches a Cluster API endpoint on the provider management network", func() {
		co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: crdTestName("co"), Namespace: h.ns,
			Labels: map[string]string{osacClusterOrderIDLabel: clID}}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: "tpl"}}
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		co.Status.ApiEndpoint = "192.168.10.5"
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())
		eipa := attachment(func(s *v1alpha1.ExternalIPAttachmentSpec) {
			s.Cluster = ptr.To(clID)
			s.TargetEndpoint = ptr.To(v1alpha1.ExternalIPAttachmentTargetEndpointAPI)
		})
		reconcileNet(eipas, eipa)
		Expect(h.aap.latest(networktask.AttachExternalIP, netFabric).vars["destination"]).To(Equal(map[string]any{
			"address": "192.168.10.5", "routing_domain": map[string]any{"type": "provider_management"}}))
	})

	It("deletes without a detach when the attach never started", func() {
		eipa := attachment(func(s *v1alpha1.ExternalIPAttachmentSpec) { s.ComputeInstance = ptr.To("ci-missing") })
		reconcileNet(eipas, eipa)
		deleteNet(eipa)
		_, exists := reconcileNet(eipas, eipa)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("creates a NATGateway with the VirtualNetwork and the ExternalIP address", func() {
		r := &NATGatewayReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		natgw := &v1alpha1.NATGateway{ObjectMeta: h.meta(kindNATGateway, "nat-1"),
			Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: "vn-1", ExternalIP: "eip-1"}}
		Expect(k8sClient.Create(ctx, natgw)).To(Succeed())
		reconcileNet(r, natgw)
		launch := h.aap.latest(networktask.CreateNATGateway, netFabric)
		Expect(launch.vars["external_ip"]).To(Equal(map[string]any{"id": "eip-1", "address": "198.51.100.19"}))
		Expect(path(launch.vars, "virtual_network", "spec", "ipv4Cidr")).To(Equal("10.0.0.0/16"))
		Expect(launch.vars).NotTo(HaveKey("network_type"))
		h.aap.succeed(networktask.CreateNATGateway, netFabric)
		reconcileNet(r, natgw)
		expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		Expect(natgw.Status.StateTransitionTime).NotTo(BeNil())
	})

	Context("dependency snapshot", func() {
		var (
			r     *NATGatewayReconciler
			natgw *v1alpha1.NATGateway
		)
		BeforeEach(func() {
			r = &NATGatewayReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
			natgw = &v1alpha1.NATGateway{ObjectMeta: h.meta(kindNATGateway, "nat-1"),
				Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: "vn-1", ExternalIP: "eip-1"}}
			Expect(k8sClient.Create(ctx, natgw)).To(Succeed())
		})

		It("gives the manager the current dependencies and reuses them for the delete", func() {
			vn, err := findVirtualNetwork(ctx, k8sClient, h.ns, "vn-1")
			Expect(err).NotTo(HaveOccurred())
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			reconcileNet(r, natgw)
			want := map[string]any{
				"external_ip": map[string]any{"name": eip.Name, "namespace": h.ns, "fulfillment_id": "eip-1",
					"kubernetes_uid": string(eip.UID), "tenant_id": "tenant-a", "address": "198.51.100.19",
					"state": "Allocated", "phase": "Ready"},
				"virtual_network": map[string]any{"name": vn.Name, "namespace": h.ns, "fulfillment_id": "vn-1",
					"kubernetes_uid": string(vn.UID), "tenant_id": "tenant-a", "ipv4_cidr": "10.0.0.0/16",
					"phase": "Ready"},
			}
			launch := h.aap.latest(networktask.CreateNATGateway, netFabric)
			Expect(launch.vars["nat_gateway_context"]).To(Equal(want))
			Expect(launch.vars["tenant"]).To(Equal("tenant-a"))
			Expect(path(launch.vars, "resource", "id")).To(Equal("nat-1"))
			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

			deleteNet(natgw)
			reconcileNet(r, natgw)
			Expect(h.aap.latest(networktask.DeleteNATGateway, netFabric).vars["nat_gateway_context"]).To(Equal(want))
			h.aap.finish(networktask.DeleteNATGateway, netFabric, "failed", `{"osac_error": {"message": "route withdrawal failed", "retryable": true}}`)
			_, exists := reconcileNet(r, natgw)
			Expect(exists).To(BeTrue(), "a failed delete keeps the finalizer")
		})

		It("stays unchanged when a Subnet of the VirtualNetwork comes and goes", func() {
			reconcileNet(r, natgw)
			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
			input := natgw.Status.TaskInput.DeepCopy()

			extra := h.readySubnet("sn-2", "vn-1", false, testPlan)
			reconcileNet(r, natgw)
			deleteNet(extra)
			reconcileNet(r, natgw)

			Expect(natgw.Status.TaskInput).To(Equal(input))
			Expect(h.aap.tasks("nat-1")).To(Equal([]string{networktask.CreateNATGateway + "/" + netFabric}))
			expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		})

		It("rejects the result when the ExternalIP changed during the job", func() {
			reconcileNet(r, natgw)
			h.aap.latest(networktask.CreateNATGateway, netFabric)
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			eip.Status.Address = "198.51.100.20"
			Expect(k8sClient.Status().Update(ctx, eip)).To(Succeed())

			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			Expect(expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring(
				"NATGateway dependencies changed while the provider operation was running"))
		})

		It("rejects the result when the VirtualNetwork is no longer Ready", func() {
			reconcileNet(r, natgw)
			vn, err := findVirtualNetwork(ctx, k8sClient, h.ns, "vn-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Delete(ctx, vn)).To(Succeed())

			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			Expect(expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring(
				"NATGateway dependencies are no longer Ready"))
		})

		It("fails without a job on an ExternalIP of another tenant", func() {
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			eip.Annotations[networktask.TenantAnnotation] = "tenant-b"
			Expect(k8sClient.Update(ctx, eip)).To(Succeed())

			reconcileNet(r, natgw)
			Expect(expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal(
				"NATGateway dependency tenant ownership does not match"))
			Expect(h.aap.tasks("nat-1")).To(BeEmpty())
		})

		It("runs delete_nat_gateway and releases the finalizer after a rejected result", func() {
			reconcileNet(r, natgw)
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			eip.Status.Address = "198.51.100.20"
			Expect(k8sClient.Status().Update(ctx, eip)).To(Succeed())
			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed)

			deleteNet(natgw)
			reconcileNet(r, natgw)
			Expect(h.aap.tasks("nat-1")).To(Equal([]string{
				networktask.CreateNATGateway + "/" + netFabric, networktask.DeleteNATGateway + "/" + netFabric}))
			h.aap.succeed(networktask.DeleteNATGateway, netFabric)
			_, exists := reconcileNet(r, natgw)
			Expect(exists).To(BeFalse(), "the finalizer is released once the delete task succeeded")
		})

		It("deletes without a job after a failure before any job", func() {
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			eip.Annotations[networktask.TenantAnnotation] = "tenant-b"
			Expect(k8sClient.Update(ctx, eip)).To(Succeed())
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed)
			Expect(natgw.Status.Jobs).To(BeEmpty())

			deleteNet(natgw)
			_, exists := reconcileNet(r, natgw)
			Expect(exists).To(BeFalse())
			Expect(h.aap.tasks("nat-1")).To(BeEmpty())
		})

		It("recovers from a failure before any job when the dependency is fixed", func() {
			eip, err := findExternalIP(ctx, k8sClient, h.ns, "eip-1")
			Expect(err).NotTo(HaveOccurred())
			eip.Annotations[networktask.TenantAnnotation] = "tenant-b"
			Expect(k8sClient.Update(ctx, eip)).To(Succeed())
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionFalse, v1alpha1.ReasonFailed)
			Expect(h.aap.tasks("nat-1")).To(BeEmpty())

			eip.Annotations[networktask.TenantAnnotation] = "tenant-a"
			Expect(k8sClient.Update(ctx, eip)).To(Succeed())
			reconcileNet(r, natgw)
			h.aap.latest(networktask.CreateNATGateway, netFabric)
			h.aap.succeed(networktask.CreateNATGateway, netFabric)
			reconcileNet(r, natgw)
			expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		})
	})
})

var _ = Describe("FabricDomain controller", func() {
	var (
		h *netHarness
		r *FabricDomainReconciler
	)
	newDomain := func() *v1alpha1.FabricDomain {
		fd := &v1alpha1.FabricDomain{ObjectMeta: h.meta(kindFabricDomain, "fd-1"),
			Spec: v1alpha1.FabricDomainSpec{Type: v1alpha1.FabricDomainTypeEthernetEW, Servers: []string{"host-a", "host-b"}, VirtualNetwork: "vn-1"}}
		Expect(k8sClient.Create(ctx, fd)).To(Succeed())
		return fd
	}
	BeforeEach(func() {
		h = newNetHarness(true)
		r = &FabricDomainReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.readyVirtualNetwork("vn-1", true)
	})

	It("fails without a job when east-west Ethernet is not enabled", func() {
		h.networkClass(true, false)
		fd := newDomain()
		reconcileNet(r, fd)
		Expect(expectReady(fd, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal(msgEastWestNotEnabled))
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("creates the domain and records backend_id", func() {
		h.networkClass(true, true)
		fd := newDomain()
		reconcileNet(r, fd)
		launch := h.aap.latest(networktask.CreateFabricDomain, netFabric)
		Expect(path(launch.vars, "resource", "spec")).To(Equal(map[string]any{"type": "ethernet_ew", "servers": []any{"host-a", "host-b"}}))
		Expect(launch.vars).NotTo(HaveKey("network_type"))
		h.aap.finish(networktask.CreateFabricDomain, netFabric, "successful", `{"osac_result": {"backend_id": "srv-cluster-4"}}`)
		reconcileNet(r, fd)
		Expect(fd.Status.BackendID).To(Equal("srv-cluster-4"))
		expectReady(fd, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})
})

var _ = Describe("NetworkAttachment controller", func() {
	var (
		h           *netHarness
		r           *NetworkAttachmentReconciler
		ciID, bmiID string
	)
	overlay := func(id string) *v1alpha1.NetworkAttachment {
		na := &v1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "computeinstance-" + id, Namespace: h.ns,
				Annotations: map[string]string{networktask.TenantAnnotation: "tenant-a"}},
			Spec: v1alpha1.NetworkAttachmentSpec{Target: v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: id},
				Subnet: "sn-1", Interface: v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay}, SecurityGroups: []string{"sg-1", "sg-2"}},
		}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		return na
	}
	physical := func(id string) *v1alpha1.NetworkAttachment {
		na := &v1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "baremetalinstance-" + id, Namespace: h.ns,
				Annotations: map[string]string{networktask.TenantAnnotation: "tenant-a"}},
			Spec: v1alpha1.NetworkAttachmentSpec{Target: v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetBareMetalInstance, ID: id},
				Subnet: "sn-1", SecurityGroups: []string{"sg-1"},
				Interface: v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff"}},
		}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		return na
	}
	vmNamespace := func() string {
		name := crdTestName("osac-subnet")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name,
			Labels: map[string]string{networktask.TenantAnnotation: "tenant-a"}}})).To(Succeed())
		return name
	}
	BeforeEach(func() {
		h = newNetHarness(true)
		r = &NetworkAttachmentReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		ciID, bmiID = h.ns+"-ci", h.ns+"-bmi"
		h.readyVirtualNetwork("vn-1", true)
	})

	It("applies a VM attachment on Kubernetes, then on the Fabric with the VM address, and deletes in reverse", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		ci := h.computeInstance(ciID, "sn-1", "")
		na := overlay(ciID)
		reconcileNet(r, na)
		Expect(na.Finalizers).To(ContainElement(osacNetworkAttachmentFinalizer))
		Expect(na.Status.NetworkManagers).To(Equal(testManagers(true)))
		k8sApply := h.aap.latest(networktask.ApplyNetworkAttachment, netK8s)
		Expect(k8sApply.label).To(Equal("osac-apply_network_attachment-kubernetes-" + ciID + "-1"))
		Expect(k8sApply.vars["target"]).To(Equal(map[string]any{"kind": "ComputeInstance", "id": ciID}))
		Expect(k8sApply.vars["network_attachment"]).To(Equal(map[string]any{"subnet": "sn-1",
			"interface": map[string]any{"type": "overlay"}, "security_group_ids": []any{"sg-1", "sg-2"}}))
		Expect(path(k8sApply.vars, "resource", "id")).To(Equal(ciID))
		Expect(path(k8sApply.vars, "resource", "spec")).To(Equal(map[string]any{
			"target": map[string]any{"kind": "ComputeInstance", "id": ciID}, "subnet": "sn-1"}))
		Expect(path(k8sApply.vars, "subnet", "spec")).To(Equal(map[string]any{"ipv4Cidr": "10.0.1.0/24"}))
		Expect(k8sApply.vars["network_segment"]).To(Equal(map[string]any{"type": "evpn", "l2_vni": float64(10100)}))
		Expect(path(k8sApply.vars, "virtual_network", "status")).To(Equal(map[string]any{"l3Vni": float64(20001)}))
		Expect(path(taskInput(na), "virtual_network", "status", "l3Vni")).To(Equal(float64(20001)))
		Expect(taskInput(na)).NotTo(HaveKey("network_segment"))

		namespace := vmNamespace()
		h.aap.finish(networktask.ApplyNetworkAttachment, netK8s, "successful",
			`{"osac_result": {"vm_attachment": {"namespace": "`+namespace+`"}}}`)
		reconcileNet(r, na)
		Expect(na.Status.VMAttachment).To(Equal(&v1alpha1.NetworkAttachmentVMAttachment{Namespace: namespace}))
		Expect(expectReady(na, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal(msgWaitingForVMAddress))

		h.setVMAddress(ci, "sn-1", "10.0.1.17")
		reconcileNet(r, na)
		fabricApply := h.aap.latest(networktask.ApplyNetworkAttachment, netFabric)
		Expect(path(fabricApply.vars, "network_attachment", "interface")).To(Equal(map[string]any{"type": "overlay", "ipv4_address": "10.0.1.17"}))
		Expect(fabricApply.vars).NotTo(HaveKey("network_segment"))
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		reconcileNet(r, na)
		expectReady(na, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		deleteNet(na)
		reconcileNet(r, na)
		h.aap.finish(networktask.DeleteNetworkAttachment, netFabric, "failed", `{"osac_error": {"message": "x", "retryable": false}}`)
		reconcileNet(r, na)
		Expect(jobState(na, networktask.DeleteNetworkAttachment, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateRetrying),
			"a delete is retried whatever osac_error says, and the finalizer stays")
		Expect(na.Finalizers).To(ContainElement(osacNetworkAttachmentFinalizer))
		Expect(h.aap.tasks(ciID)).To(Equal([]string{"apply_network_attachment/kubernetes", "apply_network_attachment/fabric", "delete_network_attachment/fabric"}))
	})

	It("deletes a VM attachment Fabric, then Kubernetes, with the bound address", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		h.computeInstance(ciID, "sn-1", "10.0.1.17")
		na := overlay(ciID)
		reconcileNet(r, na)
		namespace := vmNamespace()
		h.aap.finish(networktask.ApplyNetworkAttachment, netK8s, "successful", `{"osac_result": {"vm_attachment": {"namespace": "`+namespace+`"}}}`)
		reconcileNet(r, na)
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		reconcileNet(r, na)
		deleteNet(na)
		reconcileNet(r, na)
		h.aap.succeed(networktask.DeleteNetworkAttachment, netFabric)
		reconcileNet(r, na)
		k8sDelete := h.aap.latest(networktask.DeleteNetworkAttachment, netK8s)
		Expect(path(k8sDelete.vars, "network_attachment", "interface", "ipv4_address")).To(Equal("10.0.1.17"))
		Expect(k8sDelete.vars["network_segment"]).NotTo(BeNil())
		h.aap.succeed(networktask.DeleteNetworkAttachment, netK8s)
		_, exists := reconcileNet(r, na)
		Expect(exists).To(BeFalse())
	})

	It("rejects a vm_attachment namespace without the tenant label", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		h.computeInstance(ciID, "sn-1", "")
		na := overlay(ciID)
		reconcileNet(r, na)
		h.aap.finish(networktask.ApplyNetworkAttachment, netK8s, "successful", `{"osac_result": {"vm_attachment": {"namespace": "default"}}}`)
		reconcileNet(r, na)
		expectReady(na, metav1.ConditionFalse, v1alpha1.ReasonFailed)
	})

	It("fails a bare-metal attachment on a Subnet without a physical range", func() {
		h.readySubnet("sn-1", "vn-1", true, v1alpha1.SubnetAddressPlan{Overlay: 16})
		h.bareMetalInstance(bmiID, "rack1-node3")
		na := physical(bmiID)
		reconcileNet(r, na)
		Expect(expectReady(na, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal("Subnet sn-1 has no physical address range"))
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("applies a bare-metal attachment on the Fabric, then queries the DHCP lease after the handoff", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		bmi := h.bareMetalInstance(bmiID, "rack1-node3")
		na := physical(bmiID)
		reconcileNet(r, na)
		apply := h.aap.latest(networktask.ApplyNetworkAttachment, netFabric)
		Expect(apply.vars["target"]).To(Equal(map[string]any{"kind": "BareMetalInstance", "id": bmiID, "host_name": "rack1-node3"}))
		Expect(path(apply.vars, "network_attachment", "interface")).To(Equal(map[string]any{
			"type": "physical", "name": "data-0", "mac_address": "aa:bb:cc:dd:ee:ff"}))
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		result, _ := reconcileNet(r, na)
		expectReady(na, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		Expect(result.RequeueAfter).To(Equal(handoffRecheckInterval))
		Expect(h.aap.tasks(bmiID)).To(Equal([]string{"apply_network_attachment/fabric"}))

		meta.SetStatusCondition(&bmi.Status.Conditions, metav1.Condition{Type: string(bmfov1alpha1.HostConditionNetworkHandoffComplete),
			Status: metav1.ConditionTrue, Reason: "Done"})
		Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
		reconcileNet(r, na)
		Expect(h.aap.tasks(bmiID)).To(Equal([]string{"apply_network_attachment/fabric", "query_dhcp_lease/fabric"}))
		h.aap.finish(networktask.QueryDHCPLease, netFabric, "failed", `{"osac_error": {"message": "no lease yet", "retryable": true}}`)
		result, _ = reconcileNet(r, na)
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		expectReady(na, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		Expect(na.Status.DHCPLease).To(BeNil())
	})

	// deletingBareMetalInstance sets the BMI's deletionTimestamp; a test
	// finalizer keeps it until the spec ends.
	deletingBareMetalInstance := func(bmi *bmfov1alpha1.BareMetalInstance) {
		const hold = "test.osac.openshift.io/hold"
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
		bmi.Finalizers = append(bmi.Finalizers, hold)
		Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
			bmi.Finalizers = nil
			Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
		})
	}

	It("cancels a running bare-metal apply at the BMI deletionTimestamp (BMaaS Delete step 3)", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		bmi := h.bareMetalInstance(bmiID, "rack1-node3")
		na := physical(bmiID)
		reconcileNet(r, na)
		apply := h.aap.latest(networktask.ApplyNetworkAttachment, netFabric)

		deletingBareMetalInstance(bmi)
		reconcileNet(r, na)
		Expect(h.aap.cancelledJobs()).To(Equal([]int{apply.id}), "the running apply is cancelled, not awaited")
		reconcileNet(r, na)
		Expect(jobState(na, networktask.ApplyNetworkAttachment, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateCancelled))
		Expect(h.aap.tasks(bmiID)).To(Equal([]string{"apply_network_attachment/fabric"}), "no job starts again")
	})

	It("cancels a running DHCP lease query at the BMI deletionTimestamp (BMaaS Delete step 3)", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		bmi := h.bareMetalInstance(bmiID, "rack1-node3")
		meta.SetStatusCondition(&bmi.Status.Conditions, metav1.Condition{Type: string(bmfov1alpha1.HostConditionNetworkHandoffComplete),
			Status: metav1.ConditionTrue, Reason: "Done"})
		Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
		na := physical(bmiID)
		reconcileNet(r, na)
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		reconcileNet(r, na)
		query := h.aap.latest(networktask.QueryDHCPLease, netFabric)

		deletingBareMetalInstance(bmi)
		reconcileNet(r, na)
		Expect(h.aap.cancelledJobs()).To(Equal([]int{query.id}))
		reconcileNet(r, na)
		Expect(jobState(na, networktask.QueryDHCPLease, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateCancelled))
		Expect(h.aap.tasks(bmiID)).To(Equal([]string{"apply_network_attachment/fabric", "query_dhcp_lease/fabric"}))
	})

	It("records an accepted DHCP lease", func() {
		h.readySubnet("sn-1", "vn-1", true, testPlan)
		bmi := h.bareMetalInstance(bmiID, "rack1-node3")
		meta.SetStatusCondition(&bmi.Status.Conditions, metav1.Condition{Type: string(bmfov1alpha1.HostConditionNetworkHandoffComplete),
			Status: metav1.ConditionTrue, Reason: "Done"})
		Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
		na := physical(bmiID)
		reconcileNet(r, na)
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		reconcileNet(r, na)
		h.aap.finish(networktask.QueryDHCPLease, netFabric, "successful",
			`{"osac_result": {"dhcp_lease": {"ip_address": "10.0.1.130", "mac_address": "aa:bb:cc:dd:ee:ff"}}}`)
		reconcileNet(r, na)
		Expect(na.Status.DHCPLease).To(Equal(&v1alpha1.NetworkAttachmentDHCPLease{IPAddress: "10.0.1.130", MACAddress: "aa:bb:cc:dd:ee:ff"}))
		deleteNet(na)
		reconcileNet(r, na)
		Expect(h.aap.tasks(bmiID)[2]).To(Equal("delete_network_attachment/fabric"))
	})
})
