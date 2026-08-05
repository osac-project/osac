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

var _ = Describe("Public role bindings server", func() {
	var roleBindingsServer *RoleBindingsServer

	BeforeEach(func() {
		var err error

		roleBindingsServer, err = NewRoleBindingsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			server, err := NewRoleBindingsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewRoleBindingsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewRoleBindingsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates a role binding", func() {
			request := publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "my-role-id",
						Users: []string{
							"user-a",
							"user-b",
						},
					}.Build(),
				}.Build(),
			}.Build()

			response, err := roleBindingsServer.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("test-binding"))
			Expect(response.GetObject().GetSpec().GetRole()).To(Equal("my-role-id"))
			Expect(response.GetObject().GetSpec().GetUsers()).To(ConsistOf("user-a", "user-b"))
		})

		It("Lists role bindings", func() {
			_, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "binding-1",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-x",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := roleBindingsServer.List(ctx, publicv1.RoleBindingsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(1)))
			Expect(listResponse.GetItems()).To(HaveLen(1))
			Expect(listResponse.GetItems()[0].GetSpec().GetRole()).To(Equal("role-1"))
		})

		It("Gets a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := roleBindingsServer.Get(ctx, publicv1.RoleBindingsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetSpec().GetRole()).To(Equal("role-1"))
			Expect(getResponse.GetObject().GetSpec().GetUsers()).To(ConsistOf("user-a"))
		})

		It("Updates a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.RoleBindingSpec_builder{
						Users: []string{
							"user-a",
							"user-b",
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.users",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetUsers()).To(ConsistOf("user-a", "user-b"))
		})

		It("Ignores fields not included in the update mask", func() {
			createResponse, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
							"user-b",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-2",
						Users: []string{
							"user-x",
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.role",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRole()).To(Equal("role-2"))
			Expect(updateResponse.GetObject().GetSpec().GetUsers()).To(ConsistOf("user-a", "user-b"))
		})

		It("Deletes a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Delete(ctx, publicv1.RoleBindingsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns error when creating with nil object", func() {
			_, err := roleBindingsServer.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Returns error when updating with nil object", func() {
			_, err := roleBindingsServer.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Returns error when updating without object identifier", func() {
			_, err := roleBindingsServer.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{
				Object: publicv1.RoleBinding_builder{
					Spec: publicv1.RoleBindingSpec_builder{
						Role: "role-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
		})
	})
})
