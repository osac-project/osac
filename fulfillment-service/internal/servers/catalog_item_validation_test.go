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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func catalogPolicyStringPtr(value string) *string { return &value }

var _ = Describe("applyFieldDefinitions", func() {
	It("rejects editable field with no default and no user value", func() {
		spec := &privatev1.ClusterSpec{}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("pull_secret_secret"))
	})

	It("accepts editable field with no default when user provides value", func() {
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret"}.Build(),
		}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecretSecret().GetName()).To(Equal("my-secret"))
	})

	It("applies default for editable field when user provides no value", func() {
		spec := &privatev1.ClusterSpec{}
		defaultVal, err := structpb.NewValue(map[string]any{"name": "default-secret"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecretSecret().GetName()).To(Equal("default-secret"))
	})

	It("rejects user value for non-editable field", func() {
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "user-value"}.Build(),
		}
		defaultVal, err := structpb.NewValue(map[string]any{"name": "admin-value"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("not editable"))
	})

	It("applies default for non-editable field when user provides no value", func() {
		spec := &privatev1.ClusterSpec{}
		defaultVal, err := structpb.NewValue(map[string]any{"name": "admin-value"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecretSecret().GetName()).To(Equal("admin-value"))
	})

	It("happy path: editable value preserved and non-editable default applied", func() {
		sshKey := "ssh-ed25519 USER_KEY"
		spec := &privatev1.ClusterSpec{
			SshPublicKey: &sshKey,
		}
		defaultVersion, err := structpb.NewValue(map[string]interface{}{"name": "4-17-0"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{
			{Path: "ssh_public_key", Editable: true},
			{Path: "version", Editable: false, Default: defaultVersion},
		}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-ed25519 USER_KEY"))
		Expect(spec.GetVersion().GetName()).To(Equal("4-17-0"))
	})

	It("returns no error for empty field definitions", func() {
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret"}.Build(),
		}
		err := applyFieldDefinitions(spec, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects when any required field is missing among multiple fields", func() {
		sshKey := "my-ssh-key"
		spec := &privatev1.ClusterSpec{
			SshPublicKey: &sshKey,
		}
		defaultVersion, err := structpb.NewValue(map[string]interface{}{"name": "4-17-0"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{
			{
				Path:     "version",
				Editable: true,
				Default:  defaultVersion,
			},
			{
				Path:     "pull_secret_secret",
				Editable: true,
			},
			{
				Path:     "ssh_public_key",
				Editable: true,
			},
		}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("pull_secret_secret"))
	})

	It("applies disk_image field definition default to compute instance spec", func() {
		spec := &privatev1.ComputeInstanceSpec{}
		defaultVal, err := structpb.NewValue("my-disk-image")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "disk_image",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetDiskImage().GetName()).To(Equal("my-disk-image"))
	})

	It("applies non-editable default for string field disk_image on compute instance spec", func() {
		spec := &privatev1.ComputeInstanceSpec{}
		defaultVal, err := structpb.NewValue("my-disk-image")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "disk_image",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetDiskImage().GetName()).To(Equal("my-disk-image"))
	})

	It("rejects user value for non-editable template_parameter", func() {
		vpcID, err := anypb.New(wrapperspb.String("vpc-123"))
		Expect(err).ToNot(HaveOccurred())
		spec := privatev1.ClusterSpec_builder{
			TemplateParameters: map[string]*anypb.Any{
				"vpc_id": vpcID,
			},
		}.Build()
		defaultVal, err := structpb.NewValue("vpc-admin")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "template_parameters.vpc_id",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("not editable"))
	})

	DescribeTable("applies default for non-editable template_parameter when user provides no value",
		func(defaultInput any, expectedTypeURL string) {
			spec := &privatev1.ClusterSpec{}
			defaultVal, err := structpb.NewValue(defaultInput)
			Expect(err).ToNot(HaveOccurred())
			fieldDefs := []*privatev1.FieldDefinition{{
				Path:     "template_parameters.param",
				Editable: false,
				Default:  defaultVal,
			}}
			err = applyFieldDefinitions(spec, fieldDefs)
			Expect(err).ToNot(HaveOccurred())
			tp := spec.GetTemplateParameters()
			Expect(tp).To(HaveKey("param"))
			Expect(tp["param"].GetTypeUrl()).To(Equal(expectedTypeURL))
		},
		Entry("string value", "vpc-production-01", "type.googleapis.com/google.protobuf.StringValue"),
		Entry("bool value", true, "type.googleapis.com/google.protobuf.BoolValue"),
		Entry("integer value", float64(100), "type.googleapis.com/google.protobuf.Int64Value"),
		Entry("float value", float64(3.14), "type.googleapis.com/google.protobuf.DoubleValue"),
	)

	It("rejects editable template_parameter with no default and no user value", func() {
		spec := &privatev1.ClusterSpec{}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "template_parameters.vpc_id",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("template_parameters.vpc_id"))
	})

	It("accepts template_parameter value that passes validation schema", func() {
		vlan, err := anypb.New(wrapperspb.Int64(100))
		Expect(err).ToNot(HaveOccurred())
		spec := privatev1.ClusterSpec_builder{
			TemplateParameters: map[string]*anypb.Any{
				"vlan": vlan,
			},
		}.Build()
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:             "template_parameters.vlan",
			Editable:         true,
			ValidationSchema: `{"type":"number","minimum":1,"maximum":4094}`,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetTemplateParameters()).To(HaveKey("vlan"))
	})

	It("rejects template_parameter value that fails validation schema", func() {
		vlan, err := anypb.New(wrapperspb.Int64(9999))
		Expect(err).ToNot(HaveOccurred())
		spec := privatev1.ClusterSpec_builder{
			TemplateParameters: map[string]*anypb.Any{
				"vlan": vlan,
			},
		}.Build()
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:             "template_parameters.vlan",
			Editable:         true,
			ValidationSchema: `{"type":"number","maximum":4094}`,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("validation failed"))
	})
})

var _ = Describe("validateFieldDefinitions", func() {
	It("rejects template_parameter with invalid validation_schema", func() {
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:             "template_parameters.vlan",
			Editable:         true,
			ValidationSchema: `not-json`,
		}}
		err := validateFieldDefinitions(fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("invalid validation_schema"))
	})
})

var _ = Describe("applyFieldDefinitions rejects unlisted fields", func() {
	It("rejects a single unlisted field on ClusterSpec", func() {
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret"}.Build(),
		}
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("pull_secret_secret"))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})

	It("rejects multiple unlisted fields on ClusterSpec", func() {
		spec := privatev1.ClusterSpec_builder{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret"}.Build(),
			Version:          &privatev1.ClusterVersionReference{Name: "4-17-0"},
		}.Build()
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("pull_secret_secret"))
		Expect(err.Error()).To(ContainSubstring("version"))
	})

	It("accepts when all fields are covered by field_definitions", func() {
		sshKey := "ssh-ed25519 AAAA"
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret"}.Build(),
			SshPublicKey:     &sshKey,
		}
		fieldDefs := []*privatev1.FieldDefinition{
			{Path: "pull_secret_secret", Editable: true},
			{Path: "ssh_public_key", Editable: true},
		}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("always allows catalog_item without a field_definition", func() {
		spec := privatev1.ClusterSpec_builder{
			CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-123"}.Build(),
		}.Build()
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("always allows template without a field_definition", func() {
		spec := privatev1.ClusterSpec_builder{
			Template: privatev1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
		}.Build()
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("parent field_definition covers nested children", func() {
		spec := privatev1.ClusterSpec_builder{
			Network: privatev1.ClusterNetwork_builder{
				PodCidr:     proto.String("10.128.0.0/14"),
				ServiceCidr: proto.String("172.30.0.0/16"),
			}.Build(),
		}.Build()
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "network",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects unlisted field before checking non-editable override", func() {
		spec := &privatev1.ClusterSpec{
			PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "user-override"}.Build(),
		}
		defaultVal, err := structpb.NewValue(map[string]interface{}{"name": "4-17-0"})
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "version",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
		Expect(err.Error()).To(ContainSubstring("pull_secret_secret"))
	})

	It("rejects template_parameters without a field_definition", func() {
		vpcID, err := anypb.New(wrapperspb.String("vpc-123"))
		Expect(err).ToNot(HaveOccurred())
		spec := privatev1.ClusterSpec_builder{
			TemplateParameters: map[string]*anypb.Any{
				"vpc_id": vpcID,
			},
		}.Build()
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("template_parameters"))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})

	It("accepts template_parameters when listed in field_definitions", func() {
		vpcID, err := anypb.New(wrapperspb.String("vpc-123"))
		Expect(err).ToNot(HaveOccurred())
		spec := privatev1.ClusterSpec_builder{
			TemplateParameters: map[string]*anypb.Any{
				"vpc_id": vpcID,
			},
		}.Build()
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "template_parameters.vpc_id",
			Editable: true,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects unlisted field on ComputeInstanceSpec", func() {
		spec := &privatev1.ComputeInstanceSpec{
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}
		defaultVal, err := structpb.NewValue("ssh-ed25519 AAAA")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "ssh_public_key",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})
})
var _ = Describe("Catalog Item typed field policies", func() {
	It("applies compute defaults without aliasing the Catalog Item", func() {
		defaultImage := privatev1.DiskImageReference_builder{Name: "default-image"}.Build()
		spec := &privatev1.ComputeInstanceSpec{}
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
					Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: defaultImage}.Build(),
				}.Build(),
				SshPublicKey: privatev1.StringFieldPolicy_builder{
					Editable: privatev1.EditableStringField_builder{DefaultValue: catalogPolicyStringPtr("ssh-ed25519 default")}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		Expect(applyComputeInstanceCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetDiskImage().GetName()).To(Equal("default-image"))
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-ed25519 default"))
		spec.GetDiskImage().SetName("mutated")
		Expect(item.GetFields().GetDiskImage().GetEditable().GetDefaultValue().GetName()).To(Equal("default-image"))
	})

	It("rejects supplied values for locked compute fields", func() {
		locked := "ssh-ed25519 catalog"
		spec := privatev1.ComputeInstanceSpec_builder{SshPublicKey: catalogPolicyStringPtr("ssh-ed25519 user")}.Build()
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: &locked}.Build(),
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

	It("validates atomic Cluster node-set map values", func() {
		size := int32(2)
		valid := map[string]*privatev1.ClusterNodeSet{"workers": privatev1.ClusterNodeSet_builder{Size: &size}.Build()}
		Expect(validateClusterCatalogItemNodeSetMap("fields.node_sets", valid)).To(Succeed())
		Expect(validateClusterCatalogItemNodeSetMap("fields.node_sets", map[string]*privatev1.ClusterNodeSet{"workers": nil})).To(MatchError(ContainSubstring("field 'fields.node_sets': node set 'workers' must not be null")))
		zero := int32(0)
		Expect(validateClusterCatalogItemNodeSetMap("fields.node_sets", map[string]*privatev1.ClusterNodeSet{"workers": privatev1.ClusterNodeSet_builder{Size: &zero}.Build()})).To(MatchError(ContainSubstring("field 'fields.node_sets': node set 'workers' size must be greater than zero")))
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
