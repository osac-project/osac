/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Catalog Item typed field policies", func() {
	DescribeTable("validates concrete SSH policy values",
		func(validate func(*privatev1.StringFieldPolicy) error) {
			empty := ""
			malformed := "not-an-openssh-key"
			valid := testSSHPublicKey

			Expect(validate(privatev1.StringFieldPolicy_builder{Locked: &empty}.Build())).To(HaveOccurred())
			Expect(validate(privatev1.StringFieldPolicy_builder{
				Editable: privatev1.EditableStringField_builder{DefaultValue: &empty}.Build(),
			}.Build())).To(HaveOccurred())
			Expect(validate(privatev1.StringFieldPolicy_builder{Locked: &malformed}.Build())).To(HaveOccurred())
			Expect(validate(privatev1.StringFieldPolicy_builder{Locked: &valid}.Build())).To(Succeed())
			Expect(validate(privatev1.StringFieldPolicy_builder{
				Editable: privatev1.EditableStringField_builder{}.Build(),
			}.Build())).To(Succeed())
		},
		Entry("for Cluster Catalog Items", func(policy *privatev1.StringFieldPolicy) error {
			return validateClusterCatalogItemScalarPolicies(
				privatev1.ClusterCatalogItemFields_builder{SshPublicKey: policy}.Build())
		}),
		Entry("for BareMetalInstance Catalog Items", func(policy *privatev1.StringFieldPolicy) error {
			return validateBareMetalInstanceCatalogItemScalarPolicies(
				privatev1.BareMetalInstanceCatalogItemFields_builder{SshPublicKey: policy}.Build())
		}),
	)

	It("applies compute defaults without aliasing the Catalog Item", func() {
		defaultImage := privatev1.DiskImageReference_builder{Name: "default-image"}.Build()
		spec := &privatev1.ComputeInstanceSpec{}
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
					Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: defaultImage}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		Expect(applyComputeInstanceCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetDiskImage().GetName()).To(Equal("default-image"))
		spec.GetDiskImage().SetName("mutated")
		Expect(item.GetFields().GetDiskImage().GetEditable().GetDefaultValue().GetName()).To(Equal("default-image"))
	})

	It("rejects supplied values for locked compute fields", func() {
		locked := privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
		spec := privatev1.ComputeInstanceSpec_builder{RunStrategy: &locked}.Build()
		lockedPolicy := locked
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				RunStrategy: privatev1.ComputeInstanceRunStrategyFieldPolicy_builder{Locked: &lockedPolicy}.Build(),
			}.Build(),
		}.Build()

		Expect(applyComputeInstanceCatalogItemPolicies(spec, item.GetFields())).To(MatchError(ContainSubstring("not editable")))
	})

	It("applies locked Cluster fields", func() {
		version := privatev1.ClusterVersionReference_builder{Name: "4-17"}.Build()
		item := privatev1.ClusterCatalogItem_builder{
			Fields: privatev1.ClusterCatalogItemFields_builder{
				Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
			}.Build(),
		}.Build()
		spec := &privatev1.ClusterSpec{}

		Expect(applyClusterCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetVersion().GetName()).To(Equal("4-17"))
	})

	It("treats locked Cluster node sets as a whole map", func() {
		size := int32(3)
		nodeSets := privatev1.ClusterNodeSetMap_builder{
			Items: map[string]*privatev1.ClusterTemplateNodeSet{
				"workers": privatev1.ClusterTemplateNodeSet_builder{Size: size}.Build(),
			},
		}.Build()
		item := privatev1.ClusterCatalogItem_builder{
			Fields: privatev1.ClusterCatalogItemFields_builder{
				NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{Locked: nodeSets}.Build(),
			}.Build(),
		}.Build()

		defaulted := &privatev1.ClusterSpec{}
		Expect(applyClusterCatalogItemPolicies(defaulted, item.GetFields())).To(Succeed())
		Expect(defaulted.GetNodeSets()["workers"].GetSize()).To(Equal(int32(3)))
		defaulted.GetNodeSets()["workers"].SetSize(4)
		Expect(nodeSets.GetItems()["workers"].GetSize()).To(Equal(int32(3)))

		supplied := privatev1.ClusterSpec_builder{
			NodeSets: map[string]*privatev1.ClusterNodeSet{
				"control-plane": privatev1.ClusterNodeSet_builder{Size: &size}.Build(),
			},
		}.Build()
		Expect(applyClusterCatalogItemPolicies(supplied, item.GetFields())).To(MatchError(ContainSubstring("not editable")))
	})

	It("rejects a supplied value for a locked Bare Metal field", func() {
		locked := privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS
		spec := &privatev1.BareMetalInstanceSpec{}
		spec.SetRunStrategy(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED)
		item := privatev1.BareMetalInstanceCatalogItem_builder{
			Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{
				RunStrategy: privatev1.BareMetalInstanceRunStrategyFieldPolicy_builder{Locked: &locked}.Build(),
			}.Build(),
		}.Build()

		Expect(applyBareMetalInstanceCatalogItemPolicies(spec, item.GetFields())).To(MatchError(ContainSubstring("not editable")))
	})

	It("rejects empty locked and default network attachment policies", func() {
		computeLocked, err := decodeComputeInstanceNetworkAttachmentListPolicy(
			privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: &privatev1.ComputeNetworkAttachmentList{},
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(validateCatalogItemNetworkAttachmentsNotEmpty("fields.network_attachments", computeLocked)).To(MatchError(ContainSubstring("field 'fields.network_attachments': locked/default network attachments must not be empty")))

		bareMetalDefault, err := decodeBareMetalInstanceNetworkAttachmentListPolicy(
			privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
				Editable: privatev1.EditableBareMetalNetworkAttachmentList_builder{
					DefaultValue: &privatev1.BareMetalNetworkAttachmentList{},
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(validateCatalogItemNetworkAttachmentsNotEmpty("fields.network_attachments", bareMetalDefault)).To(MatchError(ContainSubstring("field 'fields.network_attachments': locked/default network attachments must not be empty")))
	})

	It("allows shared local policies only when editable without a default", func() {
		scope := referenceScope{tenant: auth.SharedTenant, project: ""}
		Expect(validateSharedCatalogItemLocalReferencePolicy(scope, "fields.pull_secret_secret", false, false)).To(Succeed())
		Expect(validateSharedCatalogItemLocalReferencePolicy(scope, "fields.pull_secret_secret", true, false)).To(MatchError(ContainSubstring("field 'fields.pull_secret_secret': shared catalog items cannot define a locked or default tenant-local reference")))
		Expect(validateSharedCatalogItemLocalReferencePolicy(scope, "fields.pull_secret_secret", false, true)).To(MatchError(ContainSubstring("field 'fields.pull_secret_secret': shared catalog items cannot define a locked or default tenant-local reference")))
	})

	It("materializes the resolved full-reference scope", func() {
		resolved := privatev1.InstanceType_builder{Id: "instance-type-id", Metadata: privatev1.Metadata_builder{Name: "current-name", Tenant: auth.SharedTenant, Project: "project-a"}.Build()}.Build()
		canonical := canonicalInstanceTypeReference(resolved)
		Expect(canonical.GetId()).To(Equal("instance-type-id"))
		Expect(canonical.GetName()).To(Equal("current-name"))
		Expect(canonical.GetProject()).To(Equal("project-a"))
		Expect(canonical.GetShared()).To(BeTrue())
	})

	It("preserves canonical representations for local and scoped references", func() {
		localSecret := canonicalSecretLocalReference(privatev1.Secret_builder{Id: "secret-id", Metadata: privatev1.Metadata_builder{Name: "secret-name"}.Build()}.Build())
		Expect(localSecret.GetId()).To(Equal("secret-id"))
		Expect(localSecret.GetName()).To(Equal("secret-name"))

		localBareMetal := canonicalBareMetalInstanceTypeLocalReference(privatev1.BareMetalInstanceType_builder{Id: "bmi-type-id", Metadata: privatev1.Metadata_builder{Name: "bmi-type-name"}.Build()}.Build())
		Expect(localBareMetal.GetId()).To(Equal("bmi-type-id"))
		Expect(localBareMetal.GetName()).To(Equal("bmi-type-name"))

		localSubnet := canonicalSubnetLocalReference(privatev1.Subnet_builder{Id: "subnet-id", Metadata: privatev1.Metadata_builder{Name: "subnet-name"}.Build()}.Build())
		Expect(localSubnet.GetId()).To(Equal("subnet-id"))
		Expect(localSubnet.GetName()).To(Equal("subnet-name"))

		localSecurityGroup := canonicalSecurityGroupLocalReference(privatev1.SecurityGroup_builder{Id: "sg-id", Metadata: privatev1.Metadata_builder{Name: "sg-name"}.Build()}.Build())
		Expect(localSecurityGroup.GetId()).To(Equal("sg-id"))
		Expect(localSecurityGroup.GetName()).To(Equal("sg-name"))

		storageTier := canonicalStorageTierReference(privatev1.StorageTier_builder{Id: "tier-id", Metadata: privatev1.Metadata_builder{Name: "tier-name"}.Build()}.Build())
		Expect(storageTier.GetId()).To(Equal("tier-id"))
		Expect(storageTier.GetName()).To(Equal("tier-name"))

		diskImage := canonicalDiskImageReference(
			privatev1.DiskImage_builder{Id: "image-id", Metadata: privatev1.Metadata_builder{Name: "current-image", Tenant: auth.SharedTenant, Project: "project-a"}.Build()}.Build(),
		)
		Expect(diskImage.GetId()).To(Equal("image-id"))
		Expect(diskImage.GetName()).To(Equal("current-image"))
		Expect(diskImage.GetProject()).To(Equal("project-a"))
		Expect(diskImage.GetShared()).To(BeTrue())

		clusterVersion := buildClusterVersionReference(
			privatev1.ClusterVersion_builder{Id: "version-id", Metadata: privatev1.Metadata_builder{Name: "current-version", Tenant: auth.SharedTenant, Project: "project-b"}.Build()}.Build(),
		)
		Expect(clusterVersion.GetId()).To(Equal("version-id"))
		Expect(clusterVersion.GetName()).To(Equal("current-version"))
		Expect(clusterVersion.GetProject()).To(Equal("project-b"))
		Expect(clusterVersion.GetShared()).To(BeTrue())

		template := canonicalComputeInstanceTemplateReference(
			privatev1.ComputeInstanceTemplate_builder{Id: "template-id", Metadata: privatev1.Metadata_builder{Name: "current-template", Tenant: auth.SharedTenant, Project: "project-c"}.Build()}.Build(),
		)
		Expect(template.GetId()).To(Equal("template-id"))
		Expect(template.GetName()).To(Equal("current-template"))
		Expect(template.GetProject()).To(Equal("project-c"))
		Expect(template.GetShared()).To(BeTrue())

		bareMetalTemplate := canonicalBareMetalInstanceTemplateReference(
			privatev1.BareMetalInstanceTemplate_builder{Id: "baremetal-template-id", Metadata: privatev1.Metadata_builder{Name: "current-template", Tenant: auth.SharedTenant, Project: "project-d"}.Build()}.Build(),
		)
		Expect(bareMetalTemplate.GetId()).To(Equal("baremetal-template-id"))
		Expect(bareMetalTemplate.GetName()).To(Equal("current-template"))
		Expect(bareMetalTemplate.GetProject()).To(Equal("project-d"))
		Expect(bareMetalTemplate.GetShared()).To(BeTrue())

		clusterTemplate := canonicalClusterTemplateReference(
			privatev1.ClusterTemplate_builder{Id: "cluster-template-id", Metadata: privatev1.Metadata_builder{Name: "current-template", Tenant: auth.SharedTenant, Project: "project-e"}.Build()}.Build(),
		)
		Expect(clusterTemplate.GetId()).To(Equal("cluster-template-id"))
		Expect(clusterTemplate.GetName()).To(Equal("current-template"))
		Expect(clusterTemplate.GetProject()).To(Equal("project-e"))
		Expect(clusterTemplate.GetShared()).To(BeTrue())
	})
})

var _ = Describe("Catalog provenance", func() {
	It("preserves stored identity without resolving a deleted catalog", func() {
		stored := privatev1.ComputeInstanceCatalogItemReference_builder{Id: "deleted-catalog", Name: "offering", Shared: true, Project: "project"}.Build()
		canonical, err := preserveCatalogItemProvenance(stored, privatev1.ComputeInstanceCatalogItemReference_builder{Id: stored.GetId()}.Build(), &fieldmaskpb.FieldMask{Paths: []string{"spec.catalog_item"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(canonical, stored)).To(BeTrue())
		canonical.SetName("changed")
		Expect(stored.GetName()).To(Equal("offering"))
		for _, ref := range []*privatev1.ComputeInstanceCatalogItemReference{
			nil,
			privatev1.ComputeInstanceCatalogItemReference_builder{Id: "different"}.Build(),
			privatev1.ComputeInstanceCatalogItemReference_builder{Id: stored.GetId(), Name: "different"}.Build(),
			privatev1.ComputeInstanceCatalogItemReference_builder{Id: stored.GetId(), Project: "different"}.Build(),
		} {
			_, err := preserveCatalogItemProvenance(stored, ref, &fieldmaskpb.FieldMask{Paths: []string{"spec.catalog_item"}})
			Expect(err).To(HaveOccurred())
		}
		_, err = preserveCatalogItemProvenance(stored, privatev1.ComputeInstanceCatalogItemReference_builder{Id: stored.GetId()}.Build(), &fieldmaskpb.FieldMask{Paths: []string{"spec.catalog_item.shared"}})
		Expect(err).To(HaveOccurred())
	})
	It("checks Compute immutable fields when the mask replaces spec", func() {
		current := privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "original"}.Build()}.Build()}.Build()
		candidate := proto.Clone(current).(*privatev1.ComputeInstance)
		candidate.GetSpec().GetCatalogItem().SetId("replacement")
		Expect(validateComputeInstanceImmutability(current, candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec"}})).To(HaveOccurred())
	})
	It("rejects explicit Cluster provenance clearing without mutating the current object", func() {
		current := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "original"}.Build()}.Build()}.Build()
		candidate := proto.Clone(current).(*privatev1.Cluster)
		candidate.GetSpec().ClearCatalogItem()
		Expect(validateClusterTemplateImmutability(current, candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec.catalog_item"}})).To(HaveOccurred())
		Expect(current.GetSpec().GetCatalogItem().GetId()).To(Equal("original"))
	})
})
