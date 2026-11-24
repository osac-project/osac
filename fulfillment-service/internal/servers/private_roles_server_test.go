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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Private roles server", func() {
	var (
		rolesServer       *PrivateRolesServer
		permissionCatalog *catalog.Catalog
	)

	BeforeEach(func() {
		var err error

		permissionCatalog, err = catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())

		rolesServer, err = NewPrivateRolesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetPermissionCatalog(permissionCatalog).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Deleting a referenced Role", func() {
		const roleName = "test-default-role"
		referencedStatus := status.New(codes.FailedPrecondition, "Role is still referenced by a Tenant default role or RoleBinding").Proto()
		var (
			roleID   string
			tenants  *PrivateTenantsServer
			bindings *PrivateRoleBindingsServer
		)

		BeforeEach(func() {
			var err error
			tenants, err = NewPrivateTenantsServer().
				SetLogger(logger).SetTenancyLogic(tenancy).SetAttributionLogic(attribution).Build()
			Expect(err).NotTo(HaveOccurred())
			bindings, err = NewPrivateRoleBindingsServer().
				SetLogger(logger).SetTenancyLogic(tenancy).SetAttributionLogic(attribution).Build()
			Expect(err).NotTo(HaveOccurred())

			role, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{Name: roleName, Tenant: auth.SharedTenant}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			roleID = role.GetObject().GetId()
		})

		setDefault := func(ctx context.Context, ref *privatev1.RoleReference) error {
			_, err := tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: privatev1.Tenant_builder{
					Id:   testTenant,
					Spec: privatev1.TenantSpec_builder{DefaultRole: ref}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{defaultRoleField}},
			}.Build())
			return err
		}
		setTenantDefault := func(ctx context.Context) error {
			return setDefault(ctx, privatev1.RoleReference_builder{Id: roleID}.Build())
		}
		createBinding := func(ctx context.Context, tenant, name string) (*privatev1.RoleBindingsCreateResponse, error) {
			return bindings.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Id: roleID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		}
		deleteRole := func(ctx context.Context) error {
			_, err := rolesServer.Delete(ctx, privatev1.RolesDeleteRequest_builder{Id: roleID}.Build())
			return err
		}
		insertFinalizingBinding := func() {
			_, err := suiteTx.Exec(ctx, `
				insert into role_bindings (id, name, tenant, deletion_timestamp, data)
				values ('pending-binding', 'pending-binding', $1, now(),
				        jsonb_build_object('spec', jsonb_build_object('role', jsonb_build_object('id', $2::text))))
			`, testTenant, roleID)
			Expect(err).NotTo(HaveOccurred())
		}

		It("blocks a Role used as a Tenant default until the default is cleared", func() {
			Expect(setTenantDefault(ctx)).To(Succeed())
			Expect(status.Convert(deleteRole(ctx)).Proto()).To(Equal(referencedStatus))

			Expect(setDefault(ctx, nil)).To(Succeed())
			Expect(deleteRole(ctx)).To(Succeed())
		})

		It("blocks a Role used by an active RoleBinding until the binding is deleted", func() {
			binding, err := createBinding(ctx, testTenant, "active-binding")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Convert(deleteRole(ctx)).Proto()).To(Equal(referencedStatus))

			_, err = bindings.Delete(ctx, privatev1.RoleBindingsDeleteRequest_builder{Id: binding.GetObject().GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteRole(ctx)).To(Succeed())
		})

		It("blocks a Role used by a RoleBinding awaiting finalization", func() {
			insertFinalizingBinding()
			Expect(status.Convert(deleteRole(ctx)).Proto()).To(Equal(referencedStatus))
		})

		It("returns an error when the only blocker is in another tenant", func() {
			_, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: "hidden-tenant"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = createBinding(ctx, "hidden-tenant", "hidden-binding")
			Expect(err).NotTo(HaveOccurred())

			Expect(status.Convert(deleteRole(ctx)).Proto()).To(Equal(referencedStatus))

			visibility, err := auth.NewVisibility().AddVisibleTenants(testTenant, auth.SharedTenant).Build()
			Expect(err).NotTo(HaveOccurred())
			scopedTenancy := auth.NewMockTenancyLogic(ctrl)
			scopedTenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
			public, err := NewRolesServer().
				SetLogger(logger).SetTenancyLogic(scopedTenancy).SetAttributionLogic(attribution).
				SetPermissionCatalog(permissionCatalog).Build()
			Expect(err).NotTo(HaveOccurred())

			_, err = public.Delete(ctx, publicv1.RolesDeleteRequest_builder{Id: roleID}.Build())
			Expect(status.Convert(err).Proto()).To(Equal(referencedStatus))
		})

		const racingBinding = "racing-binding"
		createRacingBinding := func(ctx context.Context) error {
			_, err := createBinding(ctx, testTenant, racingBinding)
			return err
		}
		type committedState struct {
			roleExists        bool
			tenantReferences  bool
			bindingReferences bool
		}

		DescribeTable("serializes reference writes with Role Delete",
			func(specCtx SpecContext, first, second func(context.Context) error, secondCode codes.Code, want committedState) {
				Expect(suiteTx.End(ctx)).To(Succeed())
				suiteTx = nil

				secondErr := runRoleDeleteRace(specCtx, first, second)
				Expect(status.Code(secondErr)).To(Equal(secondCode), "%v", secondErr)

				readCtx := database.TxManagerIntoContext(specCtx, tm)
				_, err := database.WithNewTx(readCtx, func(ctx context.Context) (struct{}, error) {
					_, err := rolesServer.Get(ctx, privatev1.RolesGetRequest_builder{Id: roleID}.Build())
					if want.roleExists {
						Expect(err).NotTo(HaveOccurred())
					} else {
						Expect(status.Code(err)).To(Equal(codes.NotFound), "%v", err)
					}

					tenant, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: testTenant}.Build())
					Expect(err).NotTo(HaveOccurred())
					defaultRole := tenant.GetObject().GetSpec().GetDefaultRole()
					if want.tenantReferences {
						Expect(defaultRole.GetId()).To(Equal(roleID))
					} else {
						Expect(defaultRole).To(BeNil())
					}

					filter := fmt.Sprintf("this.metadata.name == %q", racingBinding)
					list, err := bindings.List(ctx, privatev1.RoleBindingsListRequest_builder{Filter: &filter}.Build())
					Expect(err).NotTo(HaveOccurred())
					if want.bindingReferences {
						Expect(list.GetItems()).To(HaveLen(1))
						Expect(list.GetItems()[0].GetSpec().GetRole().GetId()).To(Equal(roleID))
					} else {
						Expect(list.GetItems()).To(BeEmpty())
					}
					return struct{}{}, nil
				})
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("Tenant reference commits first", setTenantDefault, deleteRole, codes.FailedPrecondition,
				committedState{roleExists: true, tenantReferences: true}),
			Entry("RoleBinding commits first", createRacingBinding, deleteRole, codes.FailedPrecondition,
				committedState{roleExists: true, bindingReferences: true}),
			Entry("Role Delete wins over the Tenant update", deleteRole, setTenantDefault, codes.FailedPrecondition,
				committedState{}),
			Entry("Role Delete wins over the RoleBinding create", deleteRole, createRacingBinding, codes.InvalidArgument,
				committedState{}),
		)
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if permission catalog is not set", func() {
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("permission catalog is mandatory"))
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

	Describe("Permission rule validation", func() {
		It("Creates a role with valid permission rules and stores them verbatim", func() {
			response, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get", "list"},
							}.Build(),
							privatev1.PermissionRule_builder{
								Service: "osac.private.volumes",
								Methods: []string{"*"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			permissions := response.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(2))
			Expect(permissions[0].GetService()).To(Equal("osac.public.clusters"))
			Expect(permissions[0].GetMethods()).To(Equal([]string{"get", "list"}))
			Expect(permissions[1].GetService()).To(Equal("osac.private.volumes"))
			Expect(permissions[1].GetMethods()).To(Equal([]string{"*"}))
		})

		DescribeTable("Rejects the creation of roles with invalid permission rules",
			func(rule *privatev1.PermissionRule_builder, expectedError string) {
				_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
					Object: privatev1.Role_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "invalid-role",
						}.Build(),
						Spec: privatev1.RoleSpec_builder{
							Permissions: []*privatev1.PermissionRule{rule.Build()},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(expectedError))
				Expect(err.Error()).ToNot(ContainSubstring(rule.Service), "the error must not echo the rule values")
				for _, method := range rule.Methods {
					Expect(err.Error()).ToNot(ContainSubstring(method), "the error must not echo the rule values")
				}

				listResponse, err := rolesServer.List(ctx, privatev1.RolesListRequest_builder{
					Filter: new("this.metadata.name == 'invalid-role'"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetSize()).To(Equal(int32(0)), "no role row should be created")
			},
			Entry("Unknown service",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.nope",
					Methods: []string{"create"},
				}, `invalid permissions rule 1: unknown service`),
			Entry("Unknown method",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"fake"},
				}, `invalid permissions rule 1: unknown method`),
			Entry("Malformed service wildcard",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clus*",
					Methods: []string{"get"},
				}, `invalid permissions rule 1: unknown service`),
			Entry("Malformed method wildcard",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"crea*"},
				}, `invalid permissions rule 1: unknown method`),
			Entry("Capitalized method",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"Get"},
				}, `invalid permissions rule 1: unknown method`),
		)

		It("Rejects the creation of roles with an empty method list", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "invalid-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("invalid permissions rule 1"))
		})

		It("Rejects the creation of roles with an unknown method in a later rule", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "invalid-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"fake"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("invalid permissions rule 2"))
		})

		It("Updates the permission rules of a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get", "list"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.permissions"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			permissions := updateResponse.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(1))
			Expect(permissions[0].GetMethods()).To(Equal([]string{"get", "list"}))
		})

		It("Rejects updating the permission rules with an unknown service, keeping the role unchanged", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.nope",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.permissions"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring(`unknown service`))
			Expect(err.Error()).ToNot(ContainSubstring(`osac.public.nope`), "the error must not echo the rule values")

			getResponse, err := rolesServer.Get(ctx, privatev1.RolesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			permissions := getResponse.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(1))
			Expect(permissions[0].GetService()).To(Equal("osac.public.clusters"))
		})

		It("Allows updates that don't touch the permission rules", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title: "Original title",
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
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
					Paths: []string{"spec.title"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetSpec().GetPermissions()).To(HaveLen(1))
		})
	})
})

// runRoleDeleteRace commits the first request only after the second is waiting
// for a lock in the spec's isolated database.
func runRoleDeleteRace(ctx SpecContext, first, second func(context.Context) error) error {
	GinkgoHelper()
	const applicationName = "role-delete-race"

	raceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	txCtx := database.TxManagerIntoContext(raceCtx, tm)
	result := make(chan error, 1)
	_, err := database.WithNewTx(txCtx, func(firstCtx context.Context) (struct{}, error) {
		if err := first(firstCtx); err != nil {
			return struct{}{}, err
		}

		DeferCleanup(func() {
			Eventually(result).WithTimeout(5 * time.Second).Should(BeClosed())
		})
		go func() {
			defer close(result)
			result <- tm.Run(raceCtx, func(secondCtx context.Context) error {
				tx, err := database.TxFromContext(secondCtx)
				if err != nil {
					return err
				}
				_, err = tx.Exec(secondCtx, "select set_config('application_name', $1, true)", applicationName)
				if err != nil {
					return err
				}
				return second(secondCtx)
			})
		}()

		Eventually(func() (bool, error) {
			var waiting bool
			err := testPool.QueryRow(raceCtx, `
				select exists (
					select 1 from pg_stat_activity
					where datname = current_database()
					  and application_name = $1
					  and wait_event_type = 'Lock'
				)
			`, applicationName).Scan(&waiting)
			return waiting, err
		}).WithContext(raceCtx).Should(BeTrue(), "the second request should wait for the first transaction")
		return struct{}{}, nil
	})
	Expect(err).NotTo(HaveOccurred(), "the first request should commit")

	var secondErr error
	Eventually(result).WithContext(raceCtx).Should(Receive(&secondErr))
	return secondErr
}
