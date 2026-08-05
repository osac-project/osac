/*
Copyright (c) 2026 Red Hat Inc.

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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Public roles server", func() {
	var rolesServer *RolesServer

	BeforeEach(func() {
		var err error

		rolesServer, err = NewRolesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			server, err := NewRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewRolesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates a role", func() {
			request := publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
				}.Build(),
			}.Build()

			response, err := rolesServer.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("test-role"))
		})

		It("Lists roles", func() {
			_, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := rolesServer.List(ctx, publicv1.RolesListRequest_builder{
				Filter: new("this.metadata.name == 'test-role'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(1)))
			Expect(listResponse.GetItems()).To(HaveLen(1))
			Expect(listResponse.GetItems()[0].GetMetadata().GetName()).To(Equal("test-role"))
		})

		It("Gets a role", func() {
			createResponse, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := rolesServer.Get(ctx, publicv1.RolesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("test-role"))
		})

		It("Updates a role", func() {
			createResponse, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
					Spec: publicv1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, publicv1.RolesUpdateRequest_builder{
				Object: publicv1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.RoleSpec_builder{
						Title: "Updated title",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
		})

		It("Ignores fields not included in the update mask", func() {
			createResponse, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
					Spec: publicv1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, publicv1.RolesUpdateRequest_builder{
				Object: publicv1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.RoleSpec_builder{
						Title:       "Updated title",
						Description: "Updated description.",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetSpec().GetDescription()).To(Equal("Original description."))
		})

		It("Deletes a role", func() {
			createResponse, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Delete(ctx, publicv1.RolesDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns error when creating with nil object", func() {
			_, err := rolesServer.Create(ctx, publicv1.RolesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Returns error when updating with nil object", func() {
			_, err := rolesServer.Update(ctx, publicv1.RolesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Returns error when updating without object identifier", func() {
			_, err := rolesServer.Update(ctx, publicv1.RolesUpdateRequest_builder{
				Object: publicv1.Role_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Returns not found when getting non-existent role", func() {
			_, err := rolesServer.Get(ctx, publicv1.RolesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
		})
	})
})
