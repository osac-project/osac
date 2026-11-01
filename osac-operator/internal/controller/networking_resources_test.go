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
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	netFabric = "fabric"
	netK8s    = "kubernetes"
)

var _ = Describe("VirtualNetwork controller", func() {
	var (
		h *netHarness
		r *VirtualNetworkReconciler
	)
	BeforeEach(func() {
		h = newNetHarness(true)
		r = &VirtualNetworkReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
	})

	It("waits for the NetworkClass to be READY", func() {
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal(msgWaitingForNetworkClass))
		Expect(controllerutil.ContainsFinalizer(vn, osacVirtualNetworkFinalizer)).To(BeTrue())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("uses a Ready NetworkClass without manager status when provisioning is disabled", func() {
		h = newNetHarness(false)
		r = &VirtualNetworkReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.networkClass(false, false)
		nc := &v1alpha1.NetworkClass{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: h.ns, Name: "default"}, nc)).To(Succeed())
		nc.Status.NetworkManagers = nil
		nc.Status.NetworkType = ""
		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonProvisioningDisabled,
		})
		Expect(k8sClient.Status().Update(ctx, nc)).To(Succeed())

		vn := h.virtualNetwork("vn-disabled")
		reconcileNet(r, vn)
		expectReady(vn, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
		Expect(vn.Status.NetworkManagers).To(BeNil())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("runs the Fabric create with network_type, then the Kubernetes create, and records backend_id and l3_vni", func() {
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		Expect(vn.Status.NetworkManagers).To(Equal(testManagers(true)))
		Expect(vn.Status.NetworkType).To(Equal(v1alpha1.NetworkTypeEVPN))
		Expect(h.aap.tasks("vn-1")).To(Equal([]string{"create_virtual_network/fabric"}))
		launch := h.aap.latest(networktask.CreateVirtualNetwork, netFabric)
		Expect(launch.template).To(Equal("osac-create-virtual-network"))
		Expect(launch.label).To(Equal("osac-create_virtual_network-fabric-vn-1-1"))
		Expect(launch.vars["network_type"]).To(Equal("evpn"))
		Expect(launch.vars["tenant"]).To(Equal("tenant-a"))
		Expect(path(launch.vars, "resource", "spec")).To(Equal(map[string]any{"ipv4Cidr": "10.0.0.0/16"}))
		Expect(path(launch.vars, "manager", "ansible_role")).To(Equal("acme.network.fabric"))
		Expect(vn.Status.TaskInput).NotTo(BeNil())
		expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonProgressing)

		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"backend_id": "vpc-7", "l3_vni": 20001}}`)
		reconcileNet(r, vn)
		Expect(vn.Status.BackendNetworkID).To(Equal("vpc-7"))
		Expect(vn.Status.L3VNI).To(Equal(int32(20001)))
		Expect(jobState(vn, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateSucceeded))
		Expect(h.aap.tasks("vn-1")).To(Equal([]string{"create_virtual_network/fabric", "create_virtual_network/kubernetes"}))
		Expect(h.aap.latest(networktask.CreateVirtualNetwork, netK8s).vars).NotTo(HaveKey("network_type"))

		h.aap.succeed(networktask.CreateVirtualNetwork, netK8s)
		reconcileNet(r, vn)
		expectReady(vn, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})

	It("sends no network_type and runs only the Fabric create without a Kubernetes Manager", func() {
		h.networkClass(false, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		Expect(h.aap.latest(networktask.CreateVirtualNetwork, netFabric).vars).NotTo(HaveKey("network_type"))
		h.aap.succeed(networktask.CreateVirtualNetwork, netFabric)
		reconcileNet(r, vn)
		expectReady(vn, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		Expect(vn.Status.L3VNI).To(BeZero())
		Expect(h.aap.tasks("")).To(Equal([]string{"create_virtual_network/fabric"}))
	})

	It("rejects l3_vni without a network type", func() {
		h.networkClass(false, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"l3_vni": 20001}}`)
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).
			To(Equal("OSAC rejected the create_virtual_network result: l3_vni returned without a network type"))
		Expect(vn.Status.L3VNI).To(BeZero())
	})

	It("rejects a Fabric result without l3_vni when a network type is set", func() {
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"backend_id": "vpc-7"}}`)
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).
			To(Equal(`OSAC rejected the create_virtual_network result: missing required output "l3_vni"`))
		Expect(vn.Status.BackendNetworkID).To(BeEmpty())
		Expect(h.aap.tasks("vn-1")).To(Equal([]string{"create_virtual_network/fabric"}))
	})

	It("rejects an l3_vni that differs from the accepted value", func() {
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		vn.Status.L3VNI = 20001
		Expect(k8sClient.Status().Update(ctx, vn)).To(Succeed())
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"l3_vni": 20002}}`)
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).
			To(Equal("OSAC rejected the create_virtual_network result: l3_vni changed from the accepted value"))
		Expect(vn.Status.L3VNI).To(Equal(int32(20001)))
	})

	It("fails finally on a non-retryable error and retries a retryable one", func() {
		h.networkClass(false, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "failed", `{"osac_error": {"message": "backend down", "retryable": true}}`)
		result, _ := reconcileNet(r, vn)
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).
			To(MatchRegexp(`^create_virtual_network \(fabric\) attempt 1 failed: backend down; next retry at `))

		vn2 := h.virtualNetwork("vn-2")
		reconcileNet(r, vn2)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "failed", `{"osac_error": {"message": "no free VNI", "retryable": false}}`)
		reconcileNet(r, vn2)
		Expect(expectReady(vn2, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal("no free VNI"))
	})

	It("rejects an undefined output", func() {
		h.networkClass(false, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"vpc_id": "x"}}`)
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).
			To(Equal(`OSAC rejected the create_virtual_network result: undefined output "vpc_id"`))
	})

	It("deletes after its children, Kubernetes before Fabric", func() {
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", `{"osac_result": {"l3_vni": 20001}}`)
		reconcileNet(r, vn)
		h.aap.succeed(networktask.CreateVirtualNetwork, netK8s)
		reconcileNet(r, vn)
		child := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)

		deleteNet(vn)
		reconcileNet(r, vn)
		Expect(h.aap.tasks("vn-1")).To(HaveLen(2), "no delete while a child Subnet exists")
		deleteNet(child)

		reconcileNet(r, vn)
		Expect(h.aap.tasks("vn-1")[2]).To(Equal("delete_virtual_network/kubernetes"))
		h.aap.succeed(networktask.DeleteVirtualNetwork, netK8s)
		reconcileNet(r, vn)
		Expect(h.aap.tasks("vn-1")[3]).To(Equal("delete_virtual_network/fabric"))
		Expect(h.aap.latest(networktask.DeleteVirtualNetwork, netFabric).vars["network_type"]).To(Equal("evpn"))
		h.aap.succeed(networktask.DeleteVirtualNetwork, netFabric)
		_, exists := reconcileNet(r, vn)
		Expect(exists).To(BeFalse())
	})

	It("cancels a running create and deletes only on the managers whose create started", func() {
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		deleteNet(vn)
		reconcileNet(r, vn) // cancels the running Fabric create
		reconcileNet(r, vn) // sees it ended: Cancelled, starts the Fabric delete
		Expect(jobState(vn, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateCancelled))
		Expect(h.aap.tasks("vn-1")).To(Equal([]string{"create_virtual_network/fabric", "delete_virtual_network/fabric"}))
		h.aap.succeed(networktask.DeleteVirtualNetwork, netFabric)
		_, exists := reconcileNet(r, vn)
		Expect(exists).To(BeFalse())
	})

	It("removes the finalizer without a task when no create started", func() {
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		deleteNet(vn)
		_, exists := reconcileNet(r, vn)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("goes Ready with ProvisioningDisabled and runs no job when provider networking is disabled", func() {
		h = newNetHarness(false)
		r = &VirtualNetworkReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.networkClass(true, false)
		vn := h.virtualNetwork("vn-1")
		reconcileNet(r, vn)
		Expect(expectReady(vn, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled).Message).
			To(Equal("provider networking is disabled: create_virtual_network not run"))
		Expect(vn.Status.TaskInput).NotTo(BeNil())
		deleteNet(vn)
		_, exists := reconcileNet(r, vn)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})
})

var _ = Describe("Subnet controller", func() {
	var (
		h *netHarness
		r *SubnetReconciler
	)
	BeforeEach(func() {
		h = newNetHarness(true)
		r = &SubnetReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
	})

	It("waits for its VirtualNetwork", func() {
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		Expect(expectReady(sn, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal("waiting for VirtualNetwork vn-1 to be READY"))
	})

	It("records the layout, accepts the segment and passes it to the Kubernetes create", func() {
		h.readyVirtualNetwork("vn-1", true)
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		Expect(sn.Status.IPAM).To(Equal(&v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1",
			OverlayIPv4CIDRs: []string{"10.0.1.0/25"}, PhysicalIPv4CIDRs: []string{"10.0.1.128/26"}, LBVIPIPv4CIDRs: []string{"10.0.1.192/26"}}))
		launch := h.aap.latest(networktask.CreateSubnet, netFabric)
		Expect(launch.vars["network_type"]).To(Equal("evpn"))
		Expect(path(launch.vars, "ipam", "physical_ipv4_cidrs")).To(Equal([]any{"10.0.1.128/26"}))
		Expect(path(launch.vars, "virtual_network", "id")).To(Equal("vn-1"))
		Expect(path(launch.vars, "virtual_network", "spec")).To(Equal(map[string]any{"ipv4Cidr": "10.0.0.0/16"}))
		Expect(path(launch.vars, "virtual_network", "status")).To(Equal(map[string]any{"l3Vni": float64(20001)}))
		stored := taskInput(sn)
		Expect(path(stored, "virtual_network", "status", "l3Vni")).To(Equal(float64(20001)))
		Expect(stored).NotTo(HaveKey("network_type"))
		Expect(stored).NotTo(HaveKey("network_segment"))

		h.aap.finish(networktask.CreateSubnet, netFabric, "successful",
			`{"osac_result": {"network_segment": {"type": "evpn", "l2_vni": 10100}}}`)
		reconcileNet(r, sn)
		Expect(sn.Status.NetworkSegment).To(Equal(&v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10100}))
		k8s := h.aap.latest(networktask.CreateSubnet, netK8s)
		Expect(k8s.vars["network_segment"]).To(Equal(map[string]any{"type": "evpn", "l2_vni": float64(10100)}))
		Expect(path(k8s.vars, "virtual_network", "status", "l3Vni")).To(Equal(float64(20001)))
		Expect(k8s.vars).NotTo(HaveKey("network_type"))
		stored = taskInput(sn)
		Expect(stored).NotTo(HaveKey("network_type"))
		Expect(stored).NotTo(HaveKey("network_segment"))
		h.aap.succeed(networktask.CreateSubnet, netK8s)
		reconcileNet(r, sn)
		expectReady(sn, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})

	DescribeTable("records eligibility without changing lifecycle readiness",
		func(artifacts string, supported bool, reason string) {
			h.readyVirtualNetwork("vn-1", true)
			sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
			reconcileNet(r, sn)
			h.aap.finish(networktask.CreateSubnet, netFabric, "successful",
				`{"osac_result":{"network_segment":{"type":"evpn","l2_vni":10100}}}`)
			reconcileNet(r, sn)
			h.aap.finish(networktask.CreateSubnet, netK8s, "successful", artifacts)
			reconcileNet(r, sn)
			expectReady(sn, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
			Expect(sn.Status.ComputeInstanceSupported).To(Equal(ptr.To(supported)))
			Expect(sn.Status.ComputeInstanceUnsupportedReason).To(Equal(reason))
			remote := privatev1.Subnet_builder{Status: privatev1.SubnetStatus_builder{}.Build()}.Build()
			Expect(syncSubnetUpdate(ctx, sn, remote)).To(Succeed())
			Expect(remote.GetStatus().HasComputeInstanceSupported()).To(BeTrue())
			Expect(remote.GetStatus().GetComputeInstanceSupported()).To(Equal(supported))
			Expect(remote.GetStatus().GetComputeInstanceUnsupportedReason()).To(Equal(reason))
		},
		Entry("owner", `{"osac_result":{"compute_instance_supported":true}}`, true, ""),
		Entry("sibling", `{"osac_result":{"compute_instance_supported":false,"compute_instance_unsupported_reason":"owner only"}}`, false, "owner only"),
		Entry("older manager", `{"osac_result":{}}`, true, ""),
	)

	It("waits for the VirtualNetwork's l3_vni before storing input or launching", func() {
		vn := h.readyVirtualNetwork("vn-1", true)
		vn.Status.L3VNI = 0
		Expect(k8sClient.Status().Update(ctx, vn)).To(Succeed())
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		Expect(expectReady(sn, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal(msgWaitingForL3VNI))
		Expect(sn.Status.TaskInput).To(BeNil())
		Expect(h.aap.tasks("")).To(BeEmpty())

		vn.Status.L3VNI = 20001
		Expect(k8sClient.Status().Update(ctx, vn)).To(Succeed())
		reconcileNet(r, sn)
		Expect(h.aap.tasks("sn-1")).To(Equal([]string{"create_subnet/fabric"}))
		Expect(path(taskInput(sn), "virtual_network", "status", "l3Vni")).To(Equal(float64(20001)))
	})

	It("stores no virtual_network status without a network type", func() {
		h.readyVirtualNetwork("vn-1", false)
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		Expect(h.aap.tasks("sn-1")).To(Equal([]string{"create_subnet/fabric"}))
		Expect(taskInput(sn)["virtual_network"]).NotTo(HaveKey("status"))
	})

	It("fails a rejected segment and deletes it on the Fabric only", func() {
		h.readyVirtualNetwork("vn-1", true)
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful",
			`{"osac_result": {"network_segment": {"type": "evpn", "l2_vni": 10100, "l3_vni": 20001}}}`)
		reconcileNet(r, sn)
		Expect(expectReady(sn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal(
			"OSAC rejected the create_subnet result: network_segment must have exactly the keys type, l2_vni"))
		deleteNet(sn)
		reconcileNet(r, sn)
		h.aap.succeed(networktask.DeleteSubnet, netFabric)
		_, exists := reconcileNet(r, sn)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("sn-1")).To(Equal([]string{"create_subnet/fabric", "delete_subnet/fabric"}))
	})

	It("fails a Subnet whose l2_vni is already held by another Subnet", func() {
		h.readyVirtualNetwork("vn-1", true)
		h.readyVirtualNetwork("vn-2", true)
		first := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		second := h.subnet("sn-2", "vn-2", "10.0.2.0/24", testPlan)
		segment := `{"osac_result": {"network_segment": {"type": "evpn", "l2_vni": 10100}}}`
		reconcileNet(r, first)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful", segment)
		reconcileNet(r, first)
		Expect(first.Status.NetworkSegment).NotTo(BeNil())

		reconcileNet(r, second)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful", segment)
		reconcileNet(r, second)
		Expect(expectReady(second, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring("VNI already in use"))
		Expect(second.Status.NetworkSegment).To(BeNil())
	})

	It("runs one Subnet task per VirtualNetwork at a time", func() {
		h.readyVirtualNetwork("vn-1", false)
		first := h.subnet("sn-1", "vn-1", "10.0.1.0/24", v1alpha1.SubnetAddressPlan{Fabric: 16})
		second := h.subnet("sn-2", "vn-1", "10.0.2.0/24", v1alpha1.SubnetAddressPlan{Fabric: 16})
		reconcileNet(r, first)
		reconcileNet(r, second)
		Expect(expectReady(second, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(Equal(msgWaitingForSlot))
		Expect(h.aap.tasks("sn-2")).To(BeEmpty())
		h.aap.succeed(networktask.CreateSubnet, netFabric)
		reconcileNet(r, first)
		expectReady(first, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		reconcileNet(r, second)
		Expect(h.aap.tasks("sn-2")).To(Equal([]string{"create_subnet/fabric"}))
		Expect(h.aap.latest(networktask.CreateSubnet, netFabric).vars).NotTo(HaveKey("network_type"))
	})

	It("fails a Subnet whose CIDR is too small for the plan, without a job", func() {
		h.readyVirtualNetwork("vn-1", false)
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/29", testPlan)
		reconcileNet(r, sn)
		expectReady(sn, metav1.ConditionFalse, v1alpha1.ReasonFailed)
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("waits for its NetworkAttachments before the delete, then deletes Kubernetes before Fabric", func() {
		h.readyVirtualNetwork("vn-1", true)
		sn := h.subnet("sn-1", "vn-1", "10.0.1.0/24", testPlan)
		reconcileNet(r, sn)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful",
			`{"osac_result": {"network_segment": {"type": "evpn", "l2_vni": 10100}}}`)
		reconcileNet(r, sn)
		h.aap.succeed(networktask.CreateSubnet, netK8s)
		reconcileNet(r, sn)
		na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{Name: "computeinstance-ci-1", Namespace: h.ns},
			Spec: v1alpha1.NetworkAttachmentSpec{Target: v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: "ci-1"},
				Subnet: "sn-1", Interface: v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay}, SecurityGroups: []string{"sg-1"}}}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())

		deleteNet(sn)
		reconcileNet(r, sn)
		Expect(h.aap.tasks("sn-1")).To(HaveLen(2))
		Expect(k8sClient.Delete(ctx, na)).To(Succeed())
		reconcileNet(r, sn)
		k8sDelete := h.aap.latest(networktask.DeleteSubnet, netK8s)
		Expect(k8sDelete.vars["network_segment"]).To(Equal(map[string]any{"type": "evpn", "l2_vni": float64(10100)}))
		Expect(path(k8sDelete.vars, "virtual_network", "status", "l3Vni")).To(Equal(float64(20001)))
		Expect(path(k8sDelete.vars, "ipam", "gateway_ipv4")).To(Equal("10.0.1.1"))
		h.aap.succeed(networktask.DeleteSubnet, netK8s)
		reconcileNet(r, sn)
		h.aap.succeed(networktask.DeleteSubnet, netFabric)
		_, exists := reconcileNet(r, sn)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("sn-1")[2:]).To(Equal([]string{"delete_subnet/kubernetes", "delete_subnet/fabric"}))
	})
})

var _ = Describe("SecurityGroup controller", func() {
	var (
		h *netHarness
		r *SecurityGroupReconciler
	)
	rule := func(cidr string) v1alpha1.SecurityRule {
		return v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolTCP, PortFrom: ptr.To[int32](22), PortTo: ptr.To[int32](22), SourceCIDR: cidr}
	}
	newGroup := func() *v1alpha1.SecurityGroup {
		sg := &v1alpha1.SecurityGroup{ObjectMeta: h.meta(kindSecurityGroup, "sg-1"),
			Spec: v1alpha1.SecurityGroupSpec{VirtualNetwork: "vn-1", IngressRules: []v1alpha1.SecurityRule{rule("0.0.0.0/0")}}}
		Expect(k8sClient.Create(ctx, sg)).To(Succeed())
		return sg
	}
	createBoth := func(sg *v1alpha1.SecurityGroup) {
		reconcileNet(r, sg)
		h.aap.succeed(networktask.CreateSecurityGroup, netFabric)
		reconcileNet(r, sg)
		h.aap.succeed(networktask.CreateSecurityGroup, netK8s)
		reconcileNet(r, sg)
	}
	updateRules := func(sg *v1alpha1.SecurityGroup, cidr string) {
		sg.Spec.IngressRules = []v1alpha1.SecurityRule{rule(cidr)}
		Expect(k8sClient.Update(ctx, sg)).To(Succeed())
	}
	BeforeEach(func() {
		h = newNetHarness(true)
		r = &SecurityGroupReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.readyVirtualNetwork("vn-1", true)
	})

	It("runs on the Fabric, then Kubernetes, with the rules and an empty direction as []", func() {
		sg := newGroup()
		createBoth(sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		launch := h.aap.latest(networktask.CreateSecurityGroup, netK8s)
		Expect(path(launch.vars, "resource", "spec", "egressRules")).To(Equal([]any{}))
		Expect(path(launch.vars, "resource", "spec", "ingressRules")).To(Equal([]any{map[string]any{
			"protocol": "tcp", "portFrom": float64(22), "portTo": float64(22), "sourceCidr": "0.0.0.0/0"}}))
		Expect(launch.vars).NotTo(HaveKey("network_type"))
	})

	It("runs a rule update as the next attempt on each manager while staying Ready with Updating", func() {
		sg := newGroup()
		createBoth(sg)
		updateRules(sg, "10.0.0.0/8")
		reconcileNet(r, sg)
		Expect(expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonUpdating).Message).To(ContainSubstring("create_security_group (fabric)"))
		fabricRun := h.aap.latest(networktask.CreateSecurityGroup, netFabric)
		Expect(fabricRun.label).To(HaveSuffix("-2"))
		Expect(path(fabricRun.vars, "resource", "spec", "ingressRules")).To(ContainElement(HaveKeyWithValue("sourceCidr", "10.0.0.0/8")))
		h.aap.succeed(networktask.CreateSecurityGroup, netFabric)
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonUpdating)
		Expect(h.aap.latest(networktask.CreateSecurityGroup, netK8s).label).To(HaveSuffix("-2"))
		h.aap.succeed(networktask.CreateSecurityGroup, netK8s)
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})

	It("skips the Kubernetes run when the Fabric run of an update fails finally, and accepts a new update after the failure", func() {
		sg := newGroup()
		createBoth(sg)
		updateRules(sg, "10.0.0.0/8")
		reconcileNet(r, sg)
		h.aap.finish(networktask.CreateSecurityGroup, netFabric, "failed", `{"osac_error": {"message": "rule not enforceable", "retryable": false}}`)
		reconcileNet(r, sg)
		Expect(expectReady(sg, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(Equal("rule not enforceable"))
		Expect(jobState(sg, networktask.CreateSecurityGroup, v1alpha1.NetworkJobManagerKubernetes)).To(Equal(v1alpha1.NetworkJobStateCancelled))
		Expect(h.aap.tasks("sg-1")).To(HaveLen(3))

		updateRules(sg, "10.1.0.0/16")
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionFalse, v1alpha1.ReasonProgressing)
		Expect(h.aap.latest(networktask.CreateSecurityGroup, netFabric).label).To(HaveSuffix("-3"))
		h.aap.succeed(networktask.CreateSecurityGroup, netFabric)
		reconcileNet(r, sg)
		h.aap.succeed(networktask.CreateSecurityGroup, netK8s)
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	})

	It("only stores a rule update with provider networking disabled", func() {
		h = newNetHarness(false)
		r = &SecurityGroupReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.readyVirtualNetwork("vn-1", true)
		sg := newGroup()
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
		updateRules(sg, "10.0.0.0/8")
		reconcileNet(r, sg)
		expectReady(sg, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
		Expect(securityGroupRulesStored(sg)).To(BeTrue())
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("deletes after no NetworkAttachment selects it", func() {
		sg := newGroup()
		createBoth(sg)
		na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{Name: "computeinstance-ci-9", Namespace: h.ns},
			Spec: v1alpha1.NetworkAttachmentSpec{Target: v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: "ci-9"},
				Subnet: "sn-1", Interface: v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay}, SecurityGroups: []string{"sg-1"}}}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		deleteNet(sg)
		reconcileNet(r, sg)
		Expect(h.aap.tasks("sg-1")).To(HaveLen(2))
		Expect(k8sClient.Delete(ctx, na)).To(Succeed())
		reconcileNet(r, sg)
		h.aap.succeed(networktask.DeleteSecurityGroup, netK8s)
		reconcileNet(r, sg)
		h.aap.succeed(networktask.DeleteSecurityGroup, netFabric)
		_, exists := reconcileNet(r, sg)
		Expect(exists).To(BeFalse())
		Expect(h.aap.tasks("sg-1")[2:]).To(Equal([]string{"delete_security_group/kubernetes", "delete_security_group/fabric"}))
	})
})

// VNI uniqueness at acceptance (EP #427 section 4.9), read through the API
// server under the operator-wide acceptance lock.
var _ = Describe("VNI acceptance", func() {
	const vniFinalizer = "osac.openshift.io/test-hold"
	var (
		h  *netHarness
		vr *VirtualNetworkReconciler
		sr *SubnetReconciler
	)
	BeforeEach(func() {
		h = newNetHarness(true)
		vr = &VirtualNetworkReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		sr = &SubnetReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns}
		h.networkClass(true, false)
	})
	l3Result := func(vni int) string {
		return `{"osac_result": {"l3_vni": ` + strconv.Itoa(vni) + `}}`
	}
	// acceptL3 runs a new VirtualNetwork's Fabric create to its result.
	acceptL3 := func(id string, vni int) *v1alpha1.VirtualNetwork {
		vn := h.virtualNetwork(id)
		reconcileNet(vr, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", l3Result(vni))
		reconcileNet(vr, vn)
		return vn
	}

	It("fails a VirtualNetwork whose l3_vni is already held by another VirtualNetwork", func() {
		first := acceptL3("vn-1", 20001)
		Expect(first.Status.L3VNI).To(Equal(int32(20001)))

		second := acceptL3("vn-2", 20001)
		Expect(expectReady(second, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring("VNI already in use"))
		Expect(second.Status.L3VNI).To(BeZero())
		Expect(jobState(second, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateFailed))
	})

	It("fails a Subnet whose l2_vni equals a live VirtualNetwork l3Vni", func() {
		h.readyVirtualNetwork("vn-1", true) // holds L3 VNI 20001
		parent := h.readyVirtualNetwork("vn-2", true)
		parent.Status.L3VNI = 20002
		Expect(k8sClient.Status().Update(ctx, parent)).To(Succeed())

		sn := h.subnet("sn-1", "vn-2", "10.0.1.0/24", testPlan)
		reconcileNet(sr, sn)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful",
			`{"osac_result": {"network_segment": {"type": "evpn", "l2_vni": 20001}}}`)
		reconcileNet(sr, sn)
		Expect(expectReady(sn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring("VNI already in use"))
		Expect(sn.Status.NetworkSegment).To(BeNil())
	})

	It("retries while the holder is being deleted and accepts once it is gone", func() {
		clk := clocktesting.NewFakeClock(time.Now())
		h.engine.WithClock(clk)
		holder := h.readyVirtualNetwork("vn-1", true) // holds L3 VNI 20001
		holder.Finalizers = append(holder.Finalizers, vniFinalizer)
		Expect(k8sClient.Update(ctx, holder)).To(Succeed())
		deleteNet(holder)

		vn := acceptL3("vn-2", 20001)
		Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonProgressing).Message).To(ContainSubstring("VNI held by a resource being deleted; retrying"))
		Expect(vn.Status.L3VNI).To(BeZero())
		Expect(jobState(vn, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateRetrying))

		By("removing the holder's finalizer so the VirtualNetwork is gone")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(holder), holder)).To(Succeed())
		holder.Finalizers = nil
		Expect(k8sClient.Update(ctx, holder)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(holder), &v1alpha1.VirtualNetwork{}))
		}).Should(BeTrue())

		By("running the retry attempt: the same VNI is now accepted")
		clk.Step(time.Hour)
		reconcileNet(vr, vn)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", l3Result(20001))
		reconcileNet(vr, vn)
		expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonProgressing)
		Expect(vn.Status.L3VNI).To(Equal(int32(20001)))
		Expect(jobState(vn, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric)).To(Equal(v1alpha1.NetworkJobStateSucceeded))
	})

	It("accepts one of two VirtualNetworks reconciled concurrently with the same l3_vni", func() {
		for round := 0; round < 10; round++ {
			vni := 20100 + round
			a, b := h.virtualNetwork("a-"+strconv.Itoa(round)), h.virtualNetwork("b-"+strconv.Itoa(round))
			reconcileNet(vr, a)
			reconcileNet(vr, b)
			// Both launches are in the fake AAP; finish both jobs with the same result.
			h.aap.finishAll(networktask.CreateVirtualNetwork, netFabric, "successful", l3Result(vni))

			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i, vn := range []*v1alpha1.VirtualNetwork{a, b} {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, errs[i] = vr.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(vn)})
				}()
			}
			wg.Wait()
			Expect(errs).To(Equal([]error{nil, nil}))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(a), a)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(b), b)).To(Succeed())
			held := 0
			for _, vn := range []*v1alpha1.VirtualNetwork{a, b} {
				if vn.Status.L3VNI == int32(vni) {
					held++
				} else {
					Expect(vn.Status.L3VNI).To(BeZero())
					Expect(expectReady(vn, metav1.ConditionFalse, v1alpha1.ReasonFailed).Message).To(ContainSubstring("VNI already in use"))
				}
			}
			Expect(held).To(Equal(1), "exactly one VirtualNetwork records l3Vni %d in round %d", vni, round)
		}
	})
})
