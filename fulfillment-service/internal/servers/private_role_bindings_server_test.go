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

var _ = Describe("Private role bindings server", func() {
	var roleBindingsServer *PrivateRoleBindingsServer

	BeforeEach(func() {
		var err error

		roleBindingsServer, err = NewPrivateRoleBindingsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			server, err := NewPrivateRoleBindingsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateRoleBindingsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateRoleBindingsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates a role binding", func() {
			response, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-binding",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "admin-role-id",
						Users: []string{
							"admin-user-1",
							"admin-user-2",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("admin-binding"))
			Expect(response.GetObject().GetSpec().GetRole()).To(Equal("admin-role-id"))
			Expect(response.GetObject().GetSpec().GetUsers()).To(ConsistOf("admin-user-1", "admin-user-2"))
		})

		It("Lists role bindings", func() {
			_, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "binding-a",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-a",
						Users: []string{
							"user-1",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "binding-b",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-b",
						Users: []string{
							"user-2",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := roleBindingsServer.List(ctx, privatev1.RoleBindingsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetItems()).To(HaveLen(2))
		})

		It("Updates a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-2",
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
		})

		It("Ignores fields not included in the update mask", func() {
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-a",
							"user-b",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
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
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "to-delete",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-1",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Delete(ctx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "to-signal",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: "role-1",
						Users: []string{
							"user-1",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Signal(ctx, privatev1.RoleBindingsSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
