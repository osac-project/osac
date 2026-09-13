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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("applyFieldDefinitions", func() {
	It("rejects editable field with no default and no user value", func() {
		spec := &privatev1.ClusterSpec{}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret_secret",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
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
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})
})

var _ = Describe("resolveAutoExternalIpPolicy", func() {
	It("returns user value when policy is nil", func() {
		result, err := resolveAutoExternalIpPolicy(true, true, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
	})

	It("returns false when policy is nil and user provides no value", func() {
		result, err := resolveAutoExternalIpPolicy(false, false, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
	})

	It("applies locked value when user provides no value", func() {
		policy := privatev1.BoolFieldPolicy_builder{Locked: proto.Bool(true)}.Build()
		result, err := resolveAutoExternalIpPolicy(false, false, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
	})

	It("accepts user value matching locked value", func() {
		policy := privatev1.BoolFieldPolicy_builder{Locked: proto.Bool(true)}.Build()
		result, err := resolveAutoExternalIpPolicy(true, true, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
	})

	It("rejects user value conflicting with locked value", func() {
		policy := privatev1.BoolFieldPolicy_builder{Locked: proto.Bool(false)}.Build()
		_, err := resolveAutoExternalIpPolicy(true, true, policy)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("locked"))
	})

	It("accepts user value when editable", func() {
		policy := privatev1.BoolFieldPolicy_builder{
			Editable: privatev1.EditableBoolField_builder{DefaultValue: proto.Bool(false)}.Build(),
		}.Build()
		result, err := resolveAutoExternalIpPolicy(true, true, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
	})

	It("applies editable default when user provides no value", func() {
		policy := privatev1.BoolFieldPolicy_builder{
			Editable: privatev1.EditableBoolField_builder{DefaultValue: proto.Bool(true)}.Build(),
		}.Build()
		result, err := resolveAutoExternalIpPolicy(false, false, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
	})

	It("returns false when editable with no default and no user value", func() {
		policy := privatev1.BoolFieldPolicy_builder{
			Editable: privatev1.EditableBoolField_builder{}.Build(),
		}.Build()
		result, err := resolveAutoExternalIpPolicy(false, false, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("resolveComputeNetworkAttachmentsPolicy", func() {
	It("leaves user attachments unchanged when policy is nil", func() {
		att := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{
			NetworkAttachments: []*privatev1.ComputeNetworkAttachment{att},
		}.Build()
		err := resolveComputeNetworkAttachmentsPolicy(spec, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("user-subnet"))
	})

	It("applies locked attachments when user provides none", func() {
		lockedAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
			Locked: privatev1.ComputeNetworkAttachmentList_builder{
				Items: []*privatev1.ComputeNetworkAttachment{lockedAtt},
			}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{}.Build()
		err := resolveComputeNetworkAttachmentsPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("locked-subnet"))
	})

	It("rejects user attachments when locked", func() {
		userAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		lockedAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
			Locked: privatev1.ComputeNetworkAttachmentList_builder{
				Items: []*privatev1.ComputeNetworkAttachment{lockedAtt},
			}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{
			NetworkAttachments: []*privatev1.ComputeNetworkAttachment{userAtt},
		}.Build()
		err := resolveComputeNetworkAttachmentsPolicy(spec, policy)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("locked"))
	})

	It("accepts user attachments when editable", func() {
		userAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		policy := privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
			Editable: privatev1.EditableComputeNetworkAttachmentList_builder{}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{
			NetworkAttachments: []*privatev1.ComputeNetworkAttachment{userAtt},
		}.Build()
		err := resolveComputeNetworkAttachmentsPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("user-subnet"))
	})

	It("applies editable default when user provides no attachments", func() {
		defaultAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "default-subnet"}.Build(),
		}.Build()
		policy := privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
			Editable: privatev1.EditableComputeNetworkAttachmentList_builder{
				DefaultValue: privatev1.ComputeNetworkAttachmentList_builder{
					Items: []*privatev1.ComputeNetworkAttachment{defaultAtt},
				}.Build(),
			}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{}.Build()
		err := resolveComputeNetworkAttachmentsPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("default-subnet"))
	})
})

var _ = Describe("resolveClusterNetworkAttachmentPolicy", func() {
	It("leaves user attachment unchanged when policy is nil", func() {
		att := privatev1.ClusterNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		spec := privatev1.ClusterSpec_builder{NetworkAttachment: att}.Build()
		err := resolveClusterNetworkAttachmentPolicy(spec, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal("user-subnet"))
	})

	It("applies locked attachment when user provides none", func() {
		lockedAtt := privatev1.ClusterNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
			Locked: lockedAtt,
		}.Build()
		spec := privatev1.ClusterSpec_builder{}.Build()
		err := resolveClusterNetworkAttachmentPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal("locked-subnet"))
	})

	It("rejects user attachment when locked", func() {
		userAtt := privatev1.ClusterNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		lockedAtt := privatev1.ClusterNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
			Locked: lockedAtt,
		}.Build()
		spec := privatev1.ClusterSpec_builder{NetworkAttachment: userAtt}.Build()
		err := resolveClusterNetworkAttachmentPolicy(spec, policy)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("locked"))
	})

	It("applies editable default when user provides no attachment", func() {
		defaultAtt := privatev1.ClusterNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "default-subnet"}.Build(),
		}.Build()
		policy := privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
			Editable: privatev1.EditableClusterNetworkAttachmentField_builder{
				DefaultValue: defaultAtt,
			}.Build(),
		}.Build()
		spec := privatev1.ClusterSpec_builder{}.Build()
		err := resolveClusterNetworkAttachmentPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal("default-subnet"))
	})
})

var _ = Describe("resolveBareMetalNetworkAttachmentsPolicy", func() {
	It("applies locked attachments when user provides none", func() {
		lockedAtt := privatev1.BareMetalNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
			Locked: privatev1.BareMetalNetworkAttachmentList_builder{
				Items: []*privatev1.BareMetalNetworkAttachment{lockedAtt},
			}.Build(),
		}.Build()
		spec := privatev1.BareMetalInstanceSpec_builder{}.Build()
		err := resolveBareMetalNetworkAttachmentsPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("locked-subnet"))
	})

	It("rejects user attachments when locked", func() {
		userAtt := privatev1.BareMetalNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "user-subnet"}.Build(),
		}.Build()
		lockedAtt := privatev1.BareMetalNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		policy := privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
			Locked: privatev1.BareMetalNetworkAttachmentList_builder{
				Items: []*privatev1.BareMetalNetworkAttachment{lockedAtt},
			}.Build(),
		}.Build()
		spec := privatev1.BareMetalInstanceSpec_builder{
			NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{userAtt},
		}.Build()
		err := resolveBareMetalNetworkAttachmentsPolicy(spec, policy)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("locked"))
	})

	It("applies editable default when user provides no attachments", func() {
		defaultAtt := privatev1.BareMetalNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "default-subnet"}.Build(),
		}.Build()
		policy := privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
			Editable: privatev1.EditableBareMetalNetworkAttachmentList_builder{
				DefaultValue: privatev1.BareMetalNetworkAttachmentList_builder{
					Items: []*privatev1.BareMetalNetworkAttachment{defaultAtt},
				}.Build(),
			}.Build(),
		}.Build()
		spec := privatev1.BareMetalInstanceSpec_builder{}.Build()
		err := resolveBareMetalNetworkAttachmentsPolicy(spec, policy)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("default-subnet"))
	})
})

var _ = Describe("applyComputeInstanceTypedNetworkingPolicies", func() {
	It("does nothing when fields is nil", func() {
		spec := privatev1.ComputeInstanceSpec_builder{}.Build()
		err := applyComputeInstanceTypedNetworkingPolicies(spec, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("resolves locked auto_external_ip and locked network_attachments together", func() {
		lockedAtt := privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: "locked-subnet"}.Build(),
		}.Build()
		fields := privatev1.ComputeInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.ComputeNetworkAttachmentList_builder{
					Items: []*privatev1.ComputeNetworkAttachment{lockedAtt},
				}.Build(),
			}.Build(),
			AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{
				Locked: proto.Bool(true),
			}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{}.Build()
		err := applyComputeInstanceTypedNetworkingPolicies(spec, fields)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("locked-subnet"))
		Expect(spec.GetAutoExternalIpAttachment()).To(BeTrue())
	})

	It("rejects user override of locked auto_external_ip", func() {
		fields := privatev1.ComputeInstanceCatalogItemFields_builder{
			AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{
				Locked: proto.Bool(false),
			}.Build(),
		}.Build()
		spec := privatev1.ComputeInstanceSpec_builder{
			AutoExternalIpAttachment: proto.Bool(true),
		}.Build()
		err := applyComputeInstanceTypedNetworkingPolicies(spec, fields)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("locked"))
	})
})

var _ = Describe("catalog item networking policy validation", func() {
	It("validates compute instance catalog item with valid locked network_attachments", func() {
		fields := privatev1.ComputeInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.ComputeNetworkAttachmentList_builder{
					Items: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build()
		err := validateComputeInstanceCatalogItemNetworkingPolicies(fields)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects compute instance catalog item with missing subnet in locked network_attachments", func() {
		fields := privatev1.ComputeInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.ComputeNetworkAttachmentList_builder{
					Items: []*privatev1.ComputeNetworkAttachment{
						privatev1.ComputeNetworkAttachment_builder{}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build()
		err := validateComputeInstanceCatalogItemNetworkingPolicies(fields)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("subnet"))
	})

	It("validates cluster catalog item with valid locked network_attachment", func() {
		fields := privatev1.ClusterCatalogItemFields_builder{
			NetworkAttachment: privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
				Locked: privatev1.ClusterNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		err := validateClusterCatalogItemNetworkingPolicies(fields)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects cluster catalog item with missing subnet in locked network_attachment", func() {
		fields := privatev1.ClusterCatalogItemFields_builder{
			NetworkAttachment: privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
				Locked: privatev1.ClusterNetworkAttachment_builder{}.Build(),
			}.Build(),
		}.Build()
		err := validateClusterCatalogItemNetworkingPolicies(fields)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("subnet"))
	})

	It("validates bare metal instance catalog item with valid locked network_attachments", func() {
		fields := privatev1.BareMetalInstanceCatalogItemFields_builder{
			NetworkAttachments: privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.BareMetalNetworkAttachmentList_builder{
					Items: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build()
		err := validateBareMetalInstanceCatalogItemNetworkingPolicies(fields)
		Expect(err).ToNot(HaveOccurred())
	})

	It("accepts nil fields for all catalog item types", func() {
		Expect(validateComputeInstanceCatalogItemNetworkingPolicies(nil)).To(Succeed())
		Expect(validateClusterCatalogItemNetworkingPolicies(nil)).To(Succeed())
		Expect(validateBareMetalInstanceCatalogItemNetworkingPolicies(nil)).To(Succeed())
	})
})

var _ = Describe("addPublishedFilter", func() {
	var server *ClusterCatalogItemsServer

	BeforeEach(func() {
		server = &ClusterCatalogItemsServer{}
	})

	DescribeTable("composes filter correctly",
		func(input string, expected string) {
			result, err := server.addPublishedFilter(input)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(expected))
		},
		Entry("empty filter", "", "this.published"),
		Entry("simple filter", "this.id == '123'", "(this.id == '123') && this.published"),
		Entry("compound filter", "this.title == 'a' && this.template == 'b'",
			"(this.title == 'a' && this.template == 'b') && this.published"),
		Entry("valid filter with OR is safely composed", "true || true",
			"(true || true) && this.published"),
	)

	DescribeTable("rejects malformed filters",
		func(input string) {
			_, err := server.addPublishedFilter(input)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		},
		Entry("unbalanced parens to bypass published", `true) || (true`),
		Entry("unbalanced closing paren", `true)`),
		Entry("unbalanced opening paren", `(true`),
	)

})
