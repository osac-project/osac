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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private roles server", func() {
	var rolesServer *PrivateRolesServer

	BeforeEach(func() {
		var err error

		rolesServer, err = NewPrivateRolesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateRolesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates a role", func() {
			response, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("admin-role"))
		})

		It("Lists roles", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "role-a",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "role-b",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := rolesServer.List(ctx, privatev1.RolesListRequest_builder{
				Filter: new("this.metadata.name in ['role-a', 'role-b']"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetItems()).To(HaveLen(2))
		})

		It("Gets a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := rolesServer.Get(ctx, privatev1.RolesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("admin-role"))
		})

		It("Updates a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
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
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
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
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Delete(ctx, privatev1.RolesDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Signal(ctx, privatev1.RolesSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
