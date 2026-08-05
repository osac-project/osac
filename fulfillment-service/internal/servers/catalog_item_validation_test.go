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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("applyFieldDefinitions", func() {
	It("rejects editable field with no default and no user value", func() {
		spec := &privatev1.ClusterSpec{}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("pull_secret"))
	})

	It("accepts editable field with no default when user provides value", func() {
		pullSecret := "my-secret"
		spec := &privatev1.ClusterSpec{
			PullSecret: &pullSecret,
		}
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret",
			Editable: true,
		}}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("my-secret"))
	})

	It("applies default for editable field when user provides no value", func() {
		spec := &privatev1.ClusterSpec{}
		defaultVal, err := structpb.NewValue("default-secret")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("default-secret"))
	})

	It("rejects user value for non-editable field", func() {
		userValue := "user-value"
		spec := &privatev1.ClusterSpec{
			PullSecret: &userValue,
		}
		defaultVal, err := structpb.NewValue("admin-value")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret",
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
		defaultVal, err := structpb.NewValue("admin-value")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "pull_secret",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("admin-value"))
	})

	It("happy path: editable value preserved and non-editable default applied", func() {
		sshKey := "ssh-ed25519 USER_KEY"
		spec := &privatev1.ClusterSpec{
			SshPublicKey: &sshKey,
		}
		defaultVersion, err := structpb.NewValue("4-17-0")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{
			{Path: "ssh_public_key", Editable: true},
			{Path: "version_name", Editable: false, Default: defaultVersion},
		}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-ed25519 USER_KEY"))
		Expect(spec.GetVersionName()).To(Equal("4-17-0"))
	})

	It("returns no error for empty field definitions", func() {
		pullSecret := "my-secret"
		spec := &privatev1.ClusterSpec{
			PullSecret: &pullSecret,
		}
		err := applyFieldDefinitions(spec, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects when any required field is missing among multiple fields", func() {
		sshKey := "my-ssh-key"
		spec := &privatev1.ClusterSpec{
			SshPublicKey: &sshKey,
		}
		defaultVersion, err := structpb.NewValue("4-17-0")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{
			{
				Path:     "version_name",
				Editable: true,
				Default:  defaultVersion,
			},
			{
				Path:     "pull_secret",
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
		Expect(err.Error()).To(ContainSubstring("pull_secret"))
	})

	It("applies is_windows field definition default to compute instance spec", func() {
		spec := &privatev1.ComputeInstanceSpec{}
		defaultVal, err := structpb.NewValue(true)
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "is_windows",
			Editable: true,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetIsWindows()).To(BeTrue())
	})

	It("applies non-editable default for bool field is_windows on compute instance spec", func() {
		spec := &privatev1.ComputeInstanceSpec{}
		defaultVal, err := structpb.NewValue(true)
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "is_windows",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetIsWindows()).To(BeTrue())
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
		pullSecret := "my-secret"
		spec := &privatev1.ClusterSpec{
			PullSecret: &pullSecret,
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
		Expect(err.Error()).To(ContainSubstring("pull_secret"))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})

	It("rejects multiple unlisted fields on ClusterSpec", func() {
		pullSecret := "my-secret"
		versionName := "4-17-0"
		spec := &privatev1.ClusterSpec{
			PullSecret:  &pullSecret,
			VersionName: &versionName,
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
		Expect(err.Error()).To(ContainSubstring("pull_secret"))
		Expect(err.Error()).To(ContainSubstring("version_name"))
	})

	It("accepts when all fields are covered by field_definitions", func() {
		pullSecret := "my-secret"
		sshKey := "ssh-ed25519 AAAA"
		spec := &privatev1.ClusterSpec{
			PullSecret:   &pullSecret,
			SshPublicKey: &sshKey,
		}
		fieldDefs := []*privatev1.FieldDefinition{
			{Path: "pull_secret", Editable: true},
			{Path: "ssh_public_key", Editable: true},
		}
		err := applyFieldDefinitions(spec, fieldDefs)
		Expect(err).ToNot(HaveOccurred())
	})

	It("always allows catalog_item without a field_definition", func() {
		spec := privatev1.ClusterSpec_builder{
			CatalogItem: "cat-123",
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
			Template: "my-template",
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
		pullSecret := "user-override"
		spec := &privatev1.ClusterSpec{
			PullSecret: &pullSecret,
		}
		defaultVal, err := structpb.NewValue("4-17-0")
		Expect(err).ToNot(HaveOccurred())
		fieldDefs := []*privatev1.FieldDefinition{{
			Path:     "version_name",
			Editable: false,
			Default:  defaultVal,
		}}
		err = applyFieldDefinitions(spec, fieldDefs)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("not allowed"))
		Expect(err.Error()).To(ContainSubstring("pull_secret"))
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
		runStrategy := "Always"
		spec := &privatev1.ComputeInstanceSpec{
			RunStrategy: &runStrategy,
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
