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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private bare metal instance templates server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateBareMetalInstanceTemplatesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateBareMetalInstanceTemplatesServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:          "test_template_create",
					Title:       "My template",
					Description: "A test template.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object.GetId()).To(Equal("test_template_create"))
			Expect(object.GetTitle()).To(Equal("My template"))
			Expect(object.GetDescription()).To(Equal("A test template."))
		})

		It("Lists objects", func() {
			const count = 5
			for i := range count {
				_, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceTemplate_builder{
						Id:    fmt.Sprintf("test_template_list_%d", i),
						Title: fmt.Sprintf("Template %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Gets object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:    "test_template_get",
					Title: "My template",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Updates object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:          "test_template_update",
					Title:       "Original title",
					Description: "Original description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Updated description."))
		})

		It("Rejects update when ID does not match required pattern", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:    "0192a3b4-5678-7def-9012-abcdef345678",
					Title: "My template",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:    createResponse.GetObject().GetId(),
					Title: "Updated title",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not match regex pattern"))
		})

		It("Deletes object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id: "test_template_delete",
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"keep"},
					}.Build(),
					Title: "My template",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Signals object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:    "test_template_signal",
					Title: "My template",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Signal(ctx, privatev1.BareMetalInstanceTemplatesSignalRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
