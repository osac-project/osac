//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

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

	It("leaves role assignment to RoleBindings", func() {
		for _, schema := range []protoreflect.MessageDescriptor{
			(&privatev1.ServiceAccountSpec{}).ProtoReflect().Descriptor(),
			(&publicv1.ServiceAccountSpec{}).ProtoReflect().Descriptor(),
		} {
			Expect(schema.Fields().Len()).To(BeZero())
		}
	})

	It("keeps internal status fields and Signal private", func() {
		publicStatus := (&publicv1.ServiceAccountStatus{}).ProtoReflect().Descriptor().Fields()
		privateStatus := (&privatev1.ServiceAccountStatus{}).ProtoReflect().Descriptor().Fields()
		Expect(publicStatus.ByName("keycloak_client_uuid")).To(BeNil())
		Expect(privateStatus.ByName("keycloak_client_uuid").Number()).To(Equal(protoreflect.FieldNumber(6)))
		Expect(privateStatus.ByName("registration_secret").Number()).To(Equal(protoreflect.FieldNumber(7)))
		Expect(privateStatus.ByName("secret_rotation_generation").Number()).To(Equal(protoreflect.FieldNumber(8)))
		state := (&publicv1.ServiceAccountStatus{}).ProtoReflect().Descriptor().Fields().ByName("state")
		Expect(state).ToNot(BeNil())
		Expect(state.Number()).To(Equal(protoreflect.FieldNumber(2)))
		Expect(publicStatus.ByName("message")).ToNot(BeNil())
		Expect(publicStatus.ByName("secret_rotation_state")).ToNot(BeNil())
		Expect(publicStatus.ByName("secret_rotation_error")).ToNot(BeNil())
		Expect(publicStatus.ByName("registration_secret")).To(BeNil())
		Expect(publicStatus.ByName("provisioning_error")).To(BeNil())
		Expect(publicv1.File_osac_public_v1_service_accounts_service_proto.Services().ByName("ServiceAccounts").Methods().ByName("Signal")).To(BeNil())
		Expect(privatev1.File_osac_private_v1_service_accounts_service_proto.Services().ByName("ServiceAccounts").Methods().ByName("Signal")).ToNot(BeNil())
	})

	It("keeps credential bytes out of service account objects and exposes a Secret reference", func() {
		for _, message := range []proto.Message{
			&privatev1.ServiceAccount{}, &privatev1.ServiceAccountSpec{}, &privatev1.ServiceAccountStatus{},
			&publicv1.ServiceAccount{}, &publicv1.ServiceAccountSpec{}, &publicv1.ServiceAccountStatus{},
		} {
			if message.ProtoReflect().Descriptor().FullName() == "osac.private.v1.ServiceAccountStatus" ||
				message.ProtoReflect().Descriptor().FullName() == "osac.public.v1.ServiceAccountStatus" {
				field := message.ProtoReflect().Descriptor().Fields().ByName("client_secret")
				Expect(field).ToNot(BeNil())
				expectedRef := protoreflect.FullName("osac.private.v1.SecretLocalReference")
				if message.ProtoReflect().Descriptor().FullName() == "osac.public.v1.ServiceAccountStatus" {
					expectedRef = "osac.public.v1.SecretLocalReference"
				}
				Expect(field.Message().FullName()).To(Equal(expectedRef))
			} else {
				Expect(message.ProtoReflect().Descriptor().Fields().ByName("client_secret")).To(BeNil())
			}
		}
		field := (&privatev1.Event{}).ProtoReflect().Descriptor().Fields().ByName("service_account")
		Expect(field.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.ServiceAccount")))
	})

	It("returns standard create responses and exposes asynchronous rotation", func() {
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
			Expect(create.Output().Fields().ByName("client_secret")).To(BeNil())
			regenerate := service.Methods().ByName("RegenerateSecret")
			Expect(regenerate).ToNot(BeNil())
			regenerateRule := proto.GetExtension(regenerate.Options(), annotations.E_Http).(*annotations.HttpRule)
			Expect(regenerateRule.GetPost()).To(Equal(prefix + "/{id}/regenerate_secret"))
			Expect(regenerateRule.GetBody()).To(Equal("*"))
		}
	})
})
