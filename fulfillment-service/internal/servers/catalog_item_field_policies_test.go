/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func policyTestInt32(value int32) *int32 { return &value }

func policyTestSubnet(name string) *privatev1.SubnetLocalReference {
	return privatev1.SubnetLocalReference_builder{Name: name}.Build()
}

func policyTestSecurityGroup(name string) *privatev1.SecurityGroupLocalReference {
	return privatev1.SecurityGroupLocalReference_builder{Name: name}.Build()
}

var _ = Describe("Shared typed-policy helper", func() {
	Describe("Cluster CIDR policies", func() {
		It("canonicalizes IPv6 defaults", func() {
			value := "2001:db8:1::1/48"
			policy := privatev1.StringFieldPolicy_builder{
				Editable: privatev1.EditableStringField_builder{DefaultValue: &value}.Build(),
			}.Build()
			Expect(canonicalizeCatalogItemCIDRPolicy(policy, "fields.network.pod_cidr")).To(Succeed())
			Expect(policy.GetEditable().GetDefaultValue()).To(Equal("2001:db8:1::/48"))
		})

		It("qualifies invalid values once", func() {
			value := "invalid"
			policy := privatev1.StringFieldPolicy_builder{Locked: &value}.Build()
			err := canonicalizeCatalogItemCIDRPolicy(policy, "fields.network.pod_cidr")
			Expect(err).To(HaveOccurred())
			Expect(strings.Count(err.Error(), "fields.network.pod_cidr")).To(Equal(1))
		})
	})

	It("applies scalar precedence", func() {
		locked := "locked"
		got := ""
		set := func(value string) { got = value }
		Expect(applyPolicy(privatev1.StringFieldPolicy_builder{Locked: &locked}.Build(), false, set, decodeStringPolicy, identity[string])).To(Succeed())
		Expect(got).To(Equal(locked))
		Expect(applyPolicy(privatev1.StringFieldPolicy_builder{Locked: &locked}.Build(), true, set, decodeStringPolicy, identity[string])).To(MatchError(ContainSubstring("field is not editable")))
		Expect(applyPolicy(&privatev1.StringFieldPolicy{}, false, set, decodeStringPolicy, identity[string])).To(MatchError(ContainSubstring("string policy has no behavior")))
	})

	It("leaves specs unchanged when no typed policies are present", func() {
		empty := "existing"
		spec := privatev1.ComputeInstanceSpec_builder{UserData: &empty}.Build()
		before := proto.Clone(spec)
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{}.Build(),
		}.Build()

		Expect(applyComputeInstanceCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(proto.Equal(before, spec)).To(BeTrue())
	})

	It("applies editable defaults and preserves supplied values", func() {
		By("applying compute instance disk-image defaults")
		diskImage := privatev1.DiskImageReference_builder{Name: "default-image"}.Build()
		computeFields := privatev1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
				Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: diskImage}.Build(),
			}.Build(),
		}.Build()
		defaultedCompute := &privatev1.ComputeInstanceSpec{}
		Expect(applyComputeInstanceCatalogItemPolicies(defaultedCompute, privatev1.ComputeInstanceCatalogItem_builder{Fields: computeFields}.Build().GetFields())).To(Succeed())
		Expect(defaultedCompute.GetDiskImage().GetName()).To(Equal("default-image"))

		suppliedImage := privatev1.DiskImageReference_builder{Name: "supplied-image"}.Build()
		suppliedCompute := privatev1.ComputeInstanceSpec_builder{DiskImage: suppliedImage}.Build()
		Expect(applyComputeInstanceCatalogItemPolicies(suppliedCompute, privatev1.ComputeInstanceCatalogItem_builder{Fields: computeFields}.Build().GetFields())).To(Succeed())
		Expect(suppliedCompute.GetDiskImage()).To(BeIdenticalTo(suppliedImage))

		By("applying cluster version and node-set defaults")
		version := privatev1.ClusterVersionReference_builder{Name: "default-version"}.Build()
		clusterFields := privatev1.ClusterCatalogItemFields_builder{
			Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{
				Editable: privatev1.EditableClusterVersionReferenceField_builder{DefaultValue: version}.Build(),
			}.Build(),
			NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{
				Editable: privatev1.EditableClusterNodeSetMap_builder{
					DefaultValue: privatev1.ClusterNodeSetMap_builder{Items: map[string]*privatev1.ClusterTemplateNodeSet{
						"workers": privatev1.ClusterTemplateNodeSet_builder{Size: 3}.Build(),
					}}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		defaultedCluster := &privatev1.ClusterSpec{}
		Expect(applyClusterCatalogItemPolicies(defaultedCluster, privatev1.ClusterCatalogItem_builder{Fields: clusterFields}.Build().GetFields())).To(Succeed())
		Expect(defaultedCluster.GetVersion().GetName()).To(Equal("default-version"))
		Expect(defaultedCluster.GetNodeSets()["workers"].GetSize()).To(Equal(int32(3)))

		suppliedVersion := privatev1.ClusterVersionReference_builder{Name: "supplied-version"}.Build()
		suppliedCluster := privatev1.ClusterSpec_builder{Version: suppliedVersion, NodeSets: map[string]*privatev1.ClusterNodeSet{
			"workers": privatev1.ClusterNodeSet_builder{Size: policyTestInt32(5)}.Build(),
		}}.Build()
		Expect(applyClusterCatalogItemPolicies(suppliedCluster, privatev1.ClusterCatalogItem_builder{Fields: clusterFields}.Build().GetFields())).To(Succeed())
		Expect(suppliedCluster.GetVersion()).To(BeIdenticalTo(suppliedVersion))
		Expect(suppliedCluster.GetNodeSets()["workers"].GetSize()).To(Equal(int32(5)))

		By("applying bare metal disk-image defaults")
		image := privatev1.DiskImageReference_builder{Name: "default"}.Build()
		bareMetalFields := privatev1.BareMetalInstanceCatalogItemFields_builder{
			DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
				Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: image}.Build(),
			}.Build(),
		}.Build()
		defaultedBareMetal := &privatev1.BareMetalInstanceSpec{}
		Expect(applyBareMetalInstanceCatalogItemPolicies(defaultedBareMetal, privatev1.BareMetalInstanceCatalogItem_builder{Fields: bareMetalFields}.Build().GetFields())).To(Succeed())
		Expect(defaultedBareMetal.GetDiskImage().GetName()).To(Equal("default"))

		suppliedBareMetalImage := privatev1.DiskImageReference_builder{Name: "supplied"}.Build()
		suppliedBareMetal := privatev1.BareMetalInstanceSpec_builder{DiskImage: suppliedBareMetalImage}.Build()
		Expect(applyBareMetalInstanceCatalogItemPolicies(suppliedBareMetal, privatev1.BareMetalInstanceCatalogItem_builder{Fields: bareMetalFields}.Build().GetFields())).To(Succeed())
		Expect(suppliedBareMetal.GetDiskImage()).To(BeIdenticalTo(suppliedBareMetalImage))
	})

	It("rejects nil message payloads without mutating the target", func() {
		malformed := &privatev1.DiskImageReferenceFieldPolicy{
			Behavior: &privatev1.DiskImageReferenceFieldPolicy_Locked{},
		}
		fields := privatev1.ComputeInstanceCatalogItemFields_builder{DiskImage: malformed}.Build()
		spec := &privatev1.ComputeInstanceSpec{}
		before := proto.Clone(spec)

		Expect(applyComputeInstanceCatalogItemPolicies(spec, privatev1.ComputeInstanceCatalogItem_builder{Fields: fields}.Build().GetFields())).To(MatchError(ContainSubstring("locked disk image policy is empty")))
		Expect(proto.Equal(before, spec)).To(BeTrue())
	})

	It("rejects explicit false, zero, and empty string values for locked policies", func() {
		By("rejecting an explicitly false external-IP setting")
		falseValue := false
		boolSpec := &privatev1.ComputeInstanceSpec{}
		boolSpec.SetAutoExternalIpAttachment(falseValue)
		boolItem := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{Locked: &falseValue}.Build(),
			}.Build(),
		}.Build()
		Expect(applyComputeInstanceCatalogItemPolicies(boolSpec, boolItem.GetFields())).To(MatchError(ContainSubstring("field is not editable")))
		Expect(boolSpec.HasAutoExternalIpAttachment()).To(BeTrue())
		Expect(boolSpec.GetAutoExternalIpAttachment()).To(BeFalse())

		By("rejecting an explicitly zero boot-disk size")
		zero := int32(0)
		sizeSpec := &privatev1.ComputeInstanceSpec{}
		sizeSpec.SetBootDisk(privatev1.ComputeInstanceDisk_builder{SizeGib: &zero}.Build())
		lockedSize := int32(10)
		sizeItem := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				BootDisk: privatev1.ComputeInstanceBootDiskFieldPolicies_builder{
					SizeGib: privatev1.Int32FieldPolicy_builder{Locked: &lockedSize}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		Expect(applyComputeInstanceCatalogItemPolicies(sizeSpec, sizeItem.GetFields())).To(MatchError(ContainSubstring("field is not editable")))
		Expect(sizeSpec.GetBootDisk().HasSizeGib()).To(BeTrue())
		Expect(sizeSpec.GetBootDisk().GetSizeGib()).To(Equal(zero))
	})

	It("treats empty collections as omitted input", func() {
		By("defaulting empty compute attachment and disk lists")
		computeAttachment := privatev1.ComputeNetworkAttachment_builder{Subnet: policyTestSubnet("compute-subnet")}.Build()
		computeSpec := &privatev1.ComputeInstanceSpec{}
		computeSpec.SetNetworkAttachments([]*privatev1.ComputeNetworkAttachment{})
		computeSpec.SetAdditionalDisks([]*privatev1.ComputeInstanceDisk{})
		computeFields := privatev1.ComputeInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.ComputeNetworkAttachmentList_builder{Items: []*privatev1.ComputeNetworkAttachment{computeAttachment}}.Build(),
			}.Build(),
			AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
				Locked: privatev1.ComputeInstanceDiskList_builder{Items: []*privatev1.ComputeInstanceDisk{privatev1.ComputeInstanceDisk_builder{SizeGib: policyTestInt32(20)}.Build()}}.Build(),
			}.Build(),
		}.Build()
		Expect(applyComputeInstanceCatalogItemPolicies(computeSpec, privatev1.ComputeInstanceCatalogItem_builder{Fields: computeFields}.Build().GetFields())).To(Succeed())
		Expect(computeSpec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(computeSpec.GetAdditionalDisks()).To(HaveLen(1))

		By("defaulting an empty cluster node-set map")
		clusterSize := int32(2)
		clusterSpec := &privatev1.ClusterSpec{}
		clusterSpec.SetNodeSets(map[string]*privatev1.ClusterNodeSet{})
		clusterFields := privatev1.ClusterCatalogItemFields_builder{
			NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{
				Locked: privatev1.ClusterNodeSetMap_builder{Items: map[string]*privatev1.ClusterTemplateNodeSet{"workers": privatev1.ClusterTemplateNodeSet_builder{Size: clusterSize}.Build()}}.Build(),
			}.Build(),
		}.Build()
		Expect(applyClusterCatalogItemPolicies(clusterSpec, privatev1.ClusterCatalogItem_builder{Fields: clusterFields}.Build().GetFields())).To(Succeed())
		Expect(clusterSpec.GetNodeSets()).To(HaveLen(1))

		By("defaulting an empty bare metal attachment list")
		bareMetalAttachment := privatev1.BareMetalNetworkAttachment_builder{Subnet: policyTestSubnet("bare-metal-subnet")}.Build()
		bareMetalSpec := &privatev1.BareMetalInstanceSpec{}
		bareMetalSpec.SetNetworkAttachments([]*privatev1.BareMetalNetworkAttachment{})
		bareMetalFields := privatev1.BareMetalInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.BareMetalNetworkAttachmentList_builder{Items: []*privatev1.BareMetalNetworkAttachment{bareMetalAttachment}}.Build(),
			}.Build(),
		}.Build()
		Expect(applyBareMetalInstanceCatalogItemPolicies(bareMetalSpec, privatev1.BareMetalInstanceCatalogItem_builder{Fields: bareMetalFields}.Build().GetFields())).To(Succeed())
		Expect(bareMetalSpec.GetNetworkAttachments()).To(HaveLen(1))
	})

})
