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
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func refIDs[T interface{ GetId() string }](refs []T) []string {
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.GetId()
	}
	return ids
}

func seedRoleBindingTestReferences() {
	roles, err := dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	Expect(err).ToNot(HaveOccurred())
	for _, id := range []string{"admin-role-id", "role-a", "role-b", "role-1", "role-2", "my-role-id"} {
		_, err := roles.Create().SetObject(privatev1.Role_builder{
			Id: id, Metadata: privatev1.Metadata_builder{Name: id, Tenant: testTenant}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}
	users, err := dao.NewGenericDAO[*privatev1.User]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	Expect(err).ToNot(HaveOccurred())
	for _, id := range []string{"admin-user-1", "admin-user-2", "human-user", "user-1", "user-2", "user-a", "user-b", "user-x"} {
		_, err := users.Create().SetObject(privatev1.User_builder{
			Id: id, Metadata: privatev1.Metadata_builder{Name: id, Tenant: testTenant}.Build(),
			Spec: privatev1.UserSpec_builder{Username: id}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}
}

var _ = Describe("Private role bindings server", func() {
	var roleBindingsServer *PrivateRoleBindingsServer

	BeforeEach(func() {
		var err error
		seedRoleBindingTestReferences()

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

	Describe("User reference validation", func() {
		var users *dao.GenericDAO[*privatev1.User]

		BeforeEach(func() {
			var err error
			users, err = dao.NewGenericDAO[*privatev1.User]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = users.Create().SetObject(privatev1.User_builder{
				Id: "foreign-user", Metadata: privatev1.Metadata_builder{Name: "foreign-user", Tenant: auth.SharedTenant}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = users.Create().SetObject(privatev1.User_builder{
				Id: "deleted-user", Metadata: privatev1.Metadata_builder{
					Name: "deleted-user", Tenant: testTenant, Finalizers: []string{"test/hold"},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = users.Delete().SetId("deleted-user").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		})

		// Both paths exercise the private handler directly, without a reference interceptor.
		DescribeTable("canonicalizes subjects after tenant assignment and update-mask merging", func(update bool) {
			created, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "canonical-users"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Name: "user-a"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := "user-a"
			if update {
				_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
					Object: privatev1.RoleBinding_builder{
						Id:       created.GetObject().GetId(),
						Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
						Spec: privatev1.RoleBindingSpec_builder{Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Name: "user-b"}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.users"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id = "user-b"
			}
			stored, err := roleBindingsServer.Get(ctx, privatev1.RoleBindingsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			ref := stored.GetObject().GetSpec().GetUsers()[0]
			Expect(ref.GetId()).To(Equal(id))
			Expect(ref.GetName()).To(Equal(id))
			Expect(ref.GetTenant()).To(Equal(testTenant))
			Expect(ref.GetShared()).To(BeFalse())
		}, Entry("Create", false), Entry("Update", true))

		DescribeTable("rejects invalid subjects on Create and Update", func(ref *privatev1.UserReference) {
			newBinding := func(name string, subject *privatev1.UserReference) *privatev1.RoleBinding {
				return privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{subject},
					}.Build(),
				}.Build()
			}
			_, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: newBinding("invalid-user", ref),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			valid, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: newBinding("valid-user", privatev1.UserReference_builder{Id: "user-a"}.Build()),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			candidate := newBinding("ignored", ref)
			candidate.SetId(valid.GetObject().GetId())
			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: candidate, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.users"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			stored, err := roleBindingsServer.Get(ctx, privatev1.RoleBindingsGetRequest_builder{Id: valid.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetUsers()[0].GetId()).To(Equal("user-a"))
		},
			Entry("missing identity", privatev1.UserReference_builder{}.Build()),
			Entry("unknown ID", privatev1.UserReference_builder{Id: "missing-user"}.Build()),
			Entry("deleted User", privatev1.UserReference_builder{Id: "deleted-user"}.Build()),
			Entry("ID/name mismatch", privatev1.UserReference_builder{Id: "user-a", Name: "user-b"}.Build()),
			Entry("wrong explicit tenant", privatev1.UserReference_builder{Id: "user-a", Tenant: auth.SharedTenant}.Build()),
			Entry("wrong shared selector", privatev1.UserReference_builder{Id: "user-a", Shared: true}.Build()),
			Entry("foreign ID without selectors", privatev1.UserReference_builder{Id: "foreign-user"}.Build()),
			Entry("foreign ID with explicit tenant", privatev1.UserReference_builder{Id: "foreign-user", Tenant: auth.SharedTenant}.Build()),
		)

		It("allows a shared User only in a shared binding", func() {
			response, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "shared-user-binding", Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Name: "tenant-reader", Shared: true}.Build(),
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Name: "foreign-user", Shared: true}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			ref := response.GetObject().GetSpec().GetUsers()[0]
			Expect(ref.GetId()).To(Equal("foreign-user"))
			Expect(ref.GetTenant()).To(Equal(auth.SharedTenant))
			Expect(ref.GetShared()).To(BeTrue())
		})

		It("preserves an unchanged subject after the User is deleted", func() {
			created, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "deleted-subject"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "user-a"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = users.Delete().SetId("user-a").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Id:       created.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{Labels: map[string]string{"updated": "true"}}.Build(),
				}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
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
						Role: privatev1.RoleReference_builder{Id: "admin-role-id"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "admin-user-1"}.Build(),
							privatev1.UserReference_builder{Id: "admin-user-2"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("admin-binding"))
			Expect(response.GetObject().GetSpec().GetRole().GetId()).To(Equal("admin-role-id"))
			Expect(refIDs(response.GetObject().GetSpec().GetUsers())).To(ConsistOf("admin-user-1", "admin-user-2"))
		})

		DescribeTable("rejects bindings without a Role", func(name string, role *privatev1.RoleReference,
			users []*privatev1.UserReference, serviceAccounts []*privatev1.ServiceAccountReference) {
			_, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:            role,
						Users:           users,
						ServiceAccounts: serviceAccounts,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("a role reference must specify an ID or name"))
		},
			Entry("nil Role with a user", "roleless-user-nil", nil,
				[]*privatev1.UserReference{privatev1.UserReference_builder{Id: "human-user"}.Build()}, nil),
			Entry("empty Role with a user", "roleless-user-empty", privatev1.RoleReference_builder{}.Build(),
				[]*privatev1.UserReference{privatev1.UserReference_builder{Id: "human-user"}.Build()}, nil),
			Entry("nil Role with a service account", "roleless-service-account", nil, nil,
				[]*privatev1.ServiceAccountReference{privatev1.ServiceAccountReference_builder{Id: "service-account"}.Build()}),
			Entry("nil Role without subjects", "roleless-no-subjects", nil, nil, nil),
			Entry("qualified Role without an ID or name", "roleless-qualified",
				privatev1.RoleReference_builder{Tenant: testTenant}.Build(), nil, nil),
		)

		It("rejects clearing the Role from a human binding", func() {
			created, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "clear-human-role"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Id: "role-a"}.Build(),
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "human-user"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object:     privatev1.RoleBinding_builder{Id: created.GetObject().GetId()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.role"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

			stored, err := roleBindingsServer.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetRole().GetId()).To(Equal("role-a"))
		})

		It("keeps updating a binding whose unchanged Role reference no longer resolves", func() {
			created, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "deleted-role-update"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "human-user"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			roles, err := dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = roles.Delete().SetId("role-1").Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			stored, err := roleBindingsServer.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			stored.GetObject().GetMetadata().SetLabels(map[string]string{"updated": "true"})
			updated, err := roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: stored.GetObject(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetRole().GetId()).To(Equal("role-1"))
			Expect(updated.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("updated", "true"))

			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{Id: created.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "another-user"}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.users"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("role 'role-1' not found"))

			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{Id: created.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "missing-role"}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.role"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("role 'missing-role' not found"))
		})

		It("rechecks the Role when service account subjects are added", func() {
			created, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "user-only-binding"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-a"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "human-user"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{Id: created.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						ServiceAccounts: []*privatev1.ServiceAccountReference{
							privatev1.ServiceAccountReference_builder{Id: "account-id"}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.service_accounts"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("service accounts may only receive predefined OSAC roles"))
		})

		It("checks account readiness after accepting an assignable shared Role", func() {
			roles, err := dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			sharedRole, err := roles.List().SetFilter("this.metadata.name == 'tenant-automation'").SetLimit(1).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(sharedRole.GetItems()).To(HaveLen(1))

			_, err = roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "inactive-account-binding"}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: sharedRole.GetItems()[0].GetId()}.Build(),
						ServiceAccounts: []*privatev1.ServiceAccountReference{
							privatev1.ServiceAccountReference_builder{
								Id: "00000000-0000-4000-8000-000000000001",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("service account must be active and provisioned"))
		})

		It("Lists role bindings", func() {
			_, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "binding-a",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-a"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-1"}.Build(),
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
						Role: privatev1.RoleReference_builder{Id: "role-b"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-2"}.Build(),
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
						Role: privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-a"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-2"}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.role",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRole().GetId()).To(Equal("role-2"))
		})

		It("Ignores fields not included in the update mask", func() {
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-binding",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-a"}.Build(),
							privatev1.UserReference_builder{Id: "user-b"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := roleBindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-2"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-x"}.Build(),
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
			Expect(updateResponse.GetObject().GetSpec().GetRole().GetId()).To(Equal("role-2"))
			Expect(refIDs(updateResponse.GetObject().GetSpec().GetUsers())).To(ConsistOf("user-a", "user-b"))
		})

		It("Deletes a role binding", func() {
			createResponse, err := roleBindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "to-delete",
					}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-1"}.Build(),
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
						Role: privatev1.RoleReference_builder{Id: "role-1"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: "user-1"}.Build(),
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
