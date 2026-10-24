/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package validation

import (
	"strings"

	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	annotations "google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("ServiceAccount API contract", func() {
	DescribeTable("validates tenant-wide names in both schemas", func(name, project string, valid bool) {
		for _, object := range []proto.Message{
			privatev1.ServiceAccount_builder{Metadata: privatev1.Metadata_builder{Name: name, Project: project}.Build()}.Build(),
			publicv1.ServiceAccount_builder{Metadata: publicv1.Metadata_builder{Name: name, Project: project}.Build()}.Build(),
		} {
			if valid {
				Expect(protovalidate.Validate(object)).To(Succeed())
			} else {
				Expect(protovalidate.Validate(object)).To(HaveOccurred())
			}
		}
	},
		Entry("DNS name", "automation", "", true),
		Entry("maximum name", strings.Repeat("a", 63), "", true),
		Entry("missing name", "", "", false),
		Entry("uppercase", "Automation", "", false),
		Entry("leading hyphen", "-automation", "", false),
		Entry("long name", strings.Repeat("a", 64), "", false),
		Entry("project", "automation", "team-a", false),
	)

	It("requires metadata", func() {
		Expect(protovalidate.Validate(&privatev1.ServiceAccount{})).To(HaveOccurred())
		Expect(protovalidate.Validate(&publicv1.ServiceAccount{})).To(HaveOccurred())
	})

	DescribeTable("accepts only distinct ID-only role references", func(roles []*privatev1.RoleReference, valid bool) {
		object := privatev1.ServiceAccountSpec_builder{Roles: roles}.Build()
		publicObject := &publicv1.ServiceAccountSpec{}
		data, err := proto.Marshal(object)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Unmarshal(data, publicObject)).To(Succeed())
		for _, spec := range []proto.Message{object, publicObject} {
			if valid {
				Expect(protovalidate.Validate(spec)).To(Succeed())
			} else {
				Expect(protovalidate.Validate(spec)).To(HaveOccurred())
			}
		}
	},
		Entry("no grants", nil, true),
		Entry("ID", []*privatev1.RoleReference{privatev1.RoleReference_builder{Id: "role-a"}.Build()}, true),
		Entry("distinct IDs", []*privatev1.RoleReference{
			privatev1.RoleReference_builder{Id: "role-a"}.Build(),
			privatev1.RoleReference_builder{Id: "role-b"}.Build(),
		}, true),
		Entry("more than four roles", []*privatev1.RoleReference{
			privatev1.RoleReference_builder{Id: "role-a"}.Build(),
			privatev1.RoleReference_builder{Id: "role-b"}.Build(),
			privatev1.RoleReference_builder{Id: "role-c"}.Build(),
			privatev1.RoleReference_builder{Id: "role-d"}.Build(),
			privatev1.RoleReference_builder{Id: "role-e"}.Build(),
		}, false),
		Entry("duplicate ID", []*privatev1.RoleReference{
			privatev1.RoleReference_builder{Id: "role-a"}.Build(),
			privatev1.RoleReference_builder{Id: "role-a"}.Build(),
		}, false),
		Entry("missing ID", []*privatev1.RoleReference{{}}, false),
		Entry("name", []*privatev1.RoleReference{privatev1.RoleReference_builder{Id: "role-a", Name: "admin"}.Build()}, false),
		Entry("project", []*privatev1.RoleReference{privatev1.RoleReference_builder{Id: "role-a", Project: "team"}.Build()}, false),
		Entry("shared", []*privatev1.RoleReference{privatev1.RoleReference_builder{Id: "role-a", Shared: true}.Build()}, false),
	)

	It("keeps the Keycloak UUID and Signal private", func() {
		Expect((&publicv1.ServiceAccountStatus{}).ProtoReflect().Descriptor().Fields().ByName("keycloak_client_uuid")).To(BeNil())
		Expect((&privatev1.ServiceAccountStatus{}).ProtoReflect().Descriptor().Fields().ByName("keycloak_client_uuid").Number()).To(Equal(protoreflect.FieldNumber(2)))
		Expect(publicv1.File_osac_public_v1_service_accounts_service_proto.Services().ByName("ServiceAccounts").Methods().ByName("Signal")).To(BeNil())
		Expect(privatev1.File_osac_private_v1_service_accounts_service_proto.Services().ByName("ServiceAccounts").Methods().ByName("Signal")).ToNot(BeNil())
	})

	It("keeps credentials out of stored objects and event payloads", func() {
		for _, message := range []proto.Message{
			&privatev1.ServiceAccount{}, &privatev1.ServiceAccountSpec{}, &privatev1.ServiceAccountStatus{},
			&publicv1.ServiceAccount{}, &publicv1.ServiceAccountSpec{}, &publicv1.ServiceAccountStatus{},
		} {
			Expect(message.ProtoReflect().Descriptor().Fields().ByName("client_secret")).To(BeNil())
		}
		field := (&privatev1.Event{}).ProtoReflect().Descriptor().Fields().ByName("service_account")
		Expect(field.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.ServiceAccount")))
	})

	It("preserves the credential response envelopes and action route in both APIs", func() {
		for _, file := range []protoreflect.FileDescriptor{
			privatev1.File_osac_private_v1_service_accounts_service_proto,
			publicv1.File_osac_public_v1_service_accounts_service_proto,
		} {
			service := file.Services().ByName("ServiceAccounts")
			prefix := "/api/private/v1/service_accounts"
			if file.Package() == "osac.public.v1" {
				prefix = "/api/fulfillment/v1/service_accounts"
			}
			create := service.Methods().ByName("Create")
			rule := proto.GetExtension(create.Options(), annotations.E_Http).(*annotations.HttpRule)
			Expect(rule.GetPost()).To(Equal(prefix))
			Expect(rule.GetBody()).To(Equal("object"))
			Expect(rule.GetResponseBody()).To(BeEmpty())
			Expect(create.Output().Fields().ByName("client_secret").Number()).To(Equal(protoreflect.FieldNumber(2)))
			rotate := service.Methods().ByName("RegenerateSecret")
			rule = proto.GetExtension(rotate.Options(), annotations.E_Http).(*annotations.HttpRule)
			Expect(rule.GetPost()).To(Equal(prefix + "/{id}:regenerate_secret"))
			Expect(rule.GetResponseBody()).To(BeEmpty())
			Expect(rotate.Output().Fields().ByName("client_secret").Number()).To(Equal(protoreflect.FieldNumber(1)))
		}
	})
})
