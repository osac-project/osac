/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// CEL and schema rules of the networking CRDs (INV-operator A1-A6).

func crdTestName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func crdMeta(prefix string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: crdTestName(prefix), Namespace: "default"}
}

func expectRejected(err error, message string) {
	Expect(err).To(HaveOccurred())
	if message != "" {
		Expect(err.Error()).To(ContainSubstring(message))
	}
}

func evenPlan() v1alpha1.SubnetAddressPlan {
	return v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4}
}

func newCRDSubnet(plan v1alpha1.SubnetAddressPlan) *v1alpha1.Subnet {
	return &v1alpha1.Subnet{ObjectMeta: crdMeta("subnet"), Spec: v1alpha1.SubnetSpec{
		VirtualNetwork: "vn-1", IPv4CIDR: "10.240.1.0/24", AddressPlan: plan,
	}}
}

func newCRDAttachment(mutate func(*v1alpha1.NetworkAttachmentSpec)) *v1alpha1.NetworkAttachment {
	na := &v1alpha1.NetworkAttachment{ObjectMeta: crdMeta("na"), Spec: v1alpha1.NetworkAttachmentSpec{
		Target:         v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: "ci-1"},
		Subnet:         "subnet-1",
		Interface:      v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay},
		SecurityGroups: []string{"sg-1"},
	}}
	if mutate != nil {
		mutate(&na.Spec)
	}
	return na
}

var _ = Describe("Networking CRD contracts", func() {
	Describe("Subnet", func() {
		It("accepts a plan in sixteenths that sums to 16 and keeps the spec immutable", func() {
			subnet := newCRDSubnet(evenPlan())
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
			subnet.Spec.AddressPlan = v1alpha1.SubnetAddressPlan{Overlay: 12, Fabric: 4}
			expectRejected(k8sClient.Update(ctx, subnet), "spec is immutable")
		})

		It("rejects a plan that does not sum to 16 and a share above 16", func() {
			expectRejected(k8sClient.Create(ctx, newCRDSubnet(v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4})),
				"addressPlan shares must sum to 16 sixteenths")
			expectRejected(k8sClient.Create(ctx, newCRDSubnet(v1alpha1.SubnetAddressPlan{Overlay: 17})), "")
		})

		It("validates the network segment in status", func() {
			subnet := newCRDSubnet(evenPlan())
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
			subnet.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 0}
			expectRejected(k8sClient.Status().Update(ctx, subnet), "")
			subnet.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 16777216}
			expectRejected(k8sClient.Status().Update(ctx, subnet), "")
			subnet.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10100}
			Expect(k8sClient.Status().Update(ctx, subnet)).To(Succeed())
		})
	})

	Describe("VirtualNetwork", func() {
		It("validates the L3 VNI in status", func() {
			vn := &v1alpha1.VirtualNetwork{ObjectMeta: crdMeta("vn"), Spec: v1alpha1.VirtualNetworkSpec{IPv4CIDR: "10.0.0.0/16"}}
			Expect(k8sClient.Create(ctx, vn)).To(Succeed())
			vn.Status.L3VNI = 16777216
			expectRejected(k8sClient.Status().Update(ctx, vn), "")
			vn.Status.L3VNI = -1
			expectRejected(k8sClient.Status().Update(ctx, vn), "")
			vn.Status.L3VNI = 20001
			Expect(k8sClient.Status().Update(ctx, vn)).To(Succeed())
		})

		It("keeps one job entry per task and manager", func() {
			subnet := newCRDSubnet(evenPlan())
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
			entry := v1alpha1.NetworkJobStatus{Task: "create_subnet", Manager: v1alpha1.NetworkJobManagerFabric, Attempt: 1, State: v1alpha1.NetworkJobStateRunning}
			subnet.Status.Jobs = []v1alpha1.NetworkJobStatus{entry, entry}
			expectRejected(k8sClient.Status().Update(ctx, subnet), "")
			k8s := entry
			k8s.Manager = v1alpha1.NetworkJobManagerKubernetes
			subnet.Status.Jobs = []v1alpha1.NetworkJobStatus{entry, k8s}
			Expect(k8sClient.Status().Update(ctx, subnet)).To(Succeed())
			subnet.Status.Jobs[0].State = "Pending"
			expectRejected(k8sClient.Status().Update(ctx, subnet), "")
		})
	})

	Describe("SecurityGroup", func() {
		It("allows rule updates and keeps the VirtualNetwork immutable", func() {
			sg := &v1alpha1.SecurityGroup{ObjectMeta: crdMeta("sg"), Spec: v1alpha1.SecurityGroupSpec{VirtualNetwork: "vn-1"}}
			Expect(k8sClient.Create(ctx, sg)).To(Succeed())
			sg.Spec.IngressRules = []v1alpha1.SecurityRule{{Protocol: "tcp", PortFrom: ptr.To[int32](22), PortTo: ptr.To[int32](22), SourceCIDR: "0.0.0.0/0"}}
			Expect(k8sClient.Update(ctx, sg)).To(Succeed())
			sg.Spec.VirtualNetwork = "vn-2"
			expectRejected(k8sClient.Update(ctx, sg), "virtualNetwork is immutable")
		})

		DescribeTable("rejects rules with wrong ports",
			func(rule v1alpha1.SecurityRule, message string) {
				sg := &v1alpha1.SecurityGroup{ObjectMeta: crdMeta("sg"), Spec: v1alpha1.SecurityGroupSpec{
					VirtualNetwork: "vn-1", IngressRules: []v1alpha1.SecurityRule{rule}}}
				expectRejected(k8sClient.Create(ctx, sg), message)
			},
			Entry("tcp without ports", v1alpha1.SecurityRule{Protocol: "tcp", SourceCIDR: "10.0.0.0/8"},
				"portFrom and portTo are set exactly for tcp and udp rules"),
			Entry("udp with only portFrom", v1alpha1.SecurityRule{Protocol: "udp", PortFrom: ptr.To[int32](53), SourceCIDR: "10.0.0.0/8"},
				"portFrom and portTo are set exactly for tcp and udp rules"),
			Entry("icmp with ports", v1alpha1.SecurityRule{Protocol: "icmp", PortFrom: ptr.To[int32](1), PortTo: ptr.To[int32](1), SourceCIDR: "10.0.0.0/8"},
				"portFrom and portTo are set exactly for tcp and udp rules"),
			Entry("portFrom above portTo", v1alpha1.SecurityRule{Protocol: "tcp", PortFrom: ptr.To[int32](80), PortTo: ptr.To[int32](22), SourceCIDR: "10.0.0.0/8"},
				"portFrom must not exceed portTo"),
		)
	})

	Describe("FabricDomain", func() {
		It("accepts only ethernet_ew and keeps the servers fixed", func() {
			fd := &v1alpha1.FabricDomain{ObjectMeta: crdMeta("fd"), Spec: v1alpha1.FabricDomainSpec{
				Type: "EthernetEW", Servers: []string{"host-1"}, VirtualNetwork: "vn-1"}}
			expectRejected(k8sClient.Create(ctx, fd), "")
			fd.Spec.Type = v1alpha1.FabricDomainTypeEthernetEW
			Expect(k8sClient.Create(ctx, fd)).To(Succeed())
			fd.Spec.Servers = []string{"host-1", "host-2"}
			expectRejected(k8sClient.Update(ctx, fd),
				"FabricDomain servers cannot change in this release; delete and recreate the FabricDomain")
		})
	})

	Describe("NetworkClass", func() {
		newClass := func() *v1alpha1.NetworkClass {
			return &v1alpha1.NetworkClass{ObjectMeta: crdMeta("nc"), Spec: v1alpha1.NetworkClassSpec{
				FabricManager: "acme_fabric",
				Defaults: v1alpha1.NetworkClassDefaults{
					VirtualNetworkIPv4CIDR: "10.0.0.0/16", SubnetIPv4CIDR: "10.0.1.0/24",
					EgressRules: []v1alpha1.SecurityRule{{Protocol: "all", DestinationCIDR: "0.0.0.0/0"}},
				},
				AddressPlan: evenPlan(),
			}}
		}

		It("keeps the whole spec immutable", func() {
			nc := newClass()
			Expect(k8sClient.Create(ctx, nc)).To(Succeed())

			changed := nc.DeepCopy()
			changed.Spec.AddressPlan = v1alpha1.SubnetAddressPlan{Overlay: 12, Fabric: 4}
			expectRejected(k8sClient.Update(ctx, changed), "addressPlan is immutable")
			changed = nc.DeepCopy()
			changed.Spec.FabricManager = "other"
			expectRejected(k8sClient.Update(ctx, changed), "fabricManager is immutable")
			changed = nc.DeepCopy()
			changed.Spec.K8sManager = "cudn_evpn"
			expectRejected(k8sClient.Update(ctx, changed), "k8sManager is immutable")
			changed = nc.DeepCopy()
			changed.Spec.Defaults.SubnetIPv4CIDR = "10.0.2.0/24"
			expectRejected(k8sClient.Update(ctx, changed), "defaults is immutable")
			changed = nc.DeepCopy()
			changed.Spec.EastWestCapabilities = &v1alpha1.NetworkClassEastWestCapabilities{SupportsEastWestEthernet: true}
			expectRejected(k8sClient.Update(ctx, changed), "eastWestCapabilities is immutable")
		})

		It("requires the address plan", func() {
			nc := newClass()
			nc.Spec.AddressPlan = v1alpha1.SubnetAddressPlan{}
			expectRejected(k8sClient.Create(ctx, nc), "addressPlan shares must sum to 16 sixteenths")
		})
	})

	Describe("NetworkAttachment", func() {
		It("accepts an overlay and a physical attachment and keeps the spec immutable", func() {
			overlay := newCRDAttachment(nil)
			Expect(k8sClient.Create(ctx, overlay)).To(Succeed())
			physical := newCRDAttachment(func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.Target.Kind = v1alpha1.NetworkAttachmentTargetBareMetalInstance
				spec.Interface = v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff"}
			})
			Expect(k8sClient.Create(ctx, physical)).To(Succeed())
			overlay.Spec.SecurityGroups = []string{"sg-2"}
			expectRejected(k8sClient.Update(ctx, overlay), "spec is immutable after creation")
		})

		DescribeTable("rejects invalid attachments",
			func(mutate func(*v1alpha1.NetworkAttachmentSpec), message string) {
				expectRejected(k8sClient.Create(ctx, newCRDAttachment(mutate)), message)
			},
			Entry("physical without MAC", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.Target.Kind = v1alpha1.NetworkAttachmentTargetBareMetalInstance
				spec.Interface = v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfacePhysical, Name: "data-0"}
			}, "interface name and macAddress are set exactly for a physical interface"),
			Entry("overlay with a name", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.Interface.Name = "eth0"
			}, "interface name and macAddress are set exactly for a physical interface"),
			Entry("ComputeInstance with a physical interface", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.Interface = v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "aa:bb:cc:dd:ee:ff"}
			}, "a ComputeInstance has an overlay interface and a BareMetalInstance a physical one"),
			Entry("uppercase MAC", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.Target.Kind = v1alpha1.NetworkAttachmentTargetBareMetalInstance
				spec.Interface = v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfacePhysical, Name: "data-0", MACAddress: "AA:BB:CC:DD:EE:FF"}
			}, "macAddress must be a lowercase colon-separated MAC address"),
			Entry("no SecurityGroup", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.SecurityGroups = []string{}
			}, ""),
			Entry("a SecurityGroup listed twice", func(spec *v1alpha1.NetworkAttachmentSpec) {
				spec.SecurityGroups = []string{"sg-1", "sg-1"}
			}, ""),
		)
	})

	Describe("ExternalIPAttachment destination", func() {
		It("requires the VirtualNetwork and Subnet IDs exactly for a tenant routing domain", func() {
			eipa := &v1alpha1.ExternalIPAttachment{ObjectMeta: crdMeta("eipa"), Spec: v1alpha1.ExternalIPAttachmentSpec{
				ExternalIP: "eip-1", ComputeInstance: ptr.To("ci-1")}}
			Expect(k8sClient.Create(ctx, eipa)).To(Succeed())
			eipa.Status.Destination = &v1alpha1.ExternalIPAttachmentDestination{Address: "10.0.1.17",
				RoutingDomain: v1alpha1.ExternalIPAttachmentRoutingDomain{Type: v1alpha1.RoutingDomainTenantVirtualNetwork}}
			expectRejected(k8sClient.Status().Update(ctx, eipa), "virtualNetworkId and subnetId are set exactly for tenant_virtual_network")
			eipa.Status.Destination.RoutingDomain.VirtualNetworkID = "vn-1"
			eipa.Status.Destination.RoutingDomain.SubnetID = "subnet-1"
			Expect(k8sClient.Status().Update(ctx, eipa)).To(Succeed())
		})
	})
})
