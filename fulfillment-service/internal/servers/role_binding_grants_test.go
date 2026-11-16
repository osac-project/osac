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
	"github.com/osac-project/osac/fulfillment-service/internal/authz"
	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var _ = Describe("RoleBinding grant resolution with PostgreSQL", func() {
	const (
		// heldFinalizer marks a row so that DAO deletion stamps a deletion timestamp
		// instead of removing it.
		heldFinalizer = "test/hold"

		clustersService    = "osac.public.clusters"
		clustersGetMethod  = "osac.public.v1.Clusters/Get"
		clustersListMethod = "osac.public.v1.Clusters/List"
	)

	var (
		permissionCatalog *catalog.Catalog
		rolesServer       *PrivateRolesServer
		bindingsServer    *PrivateRoleBindingsServer
		rolesDAO          *dao.GenericDAO[*privatev1.Role]
		bindingsDAO       *dao.GenericDAO[*privatev1.RoleBinding]
		usersDAO          *dao.GenericDAO[*privatev1.User]
		store             authz.DAOStore
		resolver          *authz.Resolver
	)

	BeforeEach(func() {
		var err error
		permissionCatalog, err = catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())
		rolesServer, err = NewPrivateRolesServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetPermissionCatalog(permissionCatalog).Build()
		Expect(err).ToNot(HaveOccurred())
		bindingsServer, err = NewPrivateRoleBindingsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		rolesDAO, err = dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		bindingsDAO, err = dao.NewGenericDAO[*privatev1.RoleBinding]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		usersDAO, err = dao.NewGenericDAO[*privatev1.User]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		store = authz.DAOStore{Bindings: bindingsDAO, Roles: rolesDAO, Users: usersDAO}
		resolver, err = authz.NewResolver(store, permissionCatalog)
		Expect(err).ToNot(HaveOccurred())
	})

	// roleSeed describes a Role row persisted directly through the DAO. Unset fields
	// take the test defaults: the test tenant and "get" access to clusters. bare
	// persists the row without a spec, for tests that only need the row to exist.
	type roleSeed struct {
		name       string
		tenant     string
		methods    []string
		finalizers []string
		bare       bool
	}

	createRole := func(seed roleSeed) *privatev1.Role {
		tenant := seed.tenant
		if tenant == "" {
			tenant = testTenant
		}
		role := privatev1.Role_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       seed.name,
				Tenant:     tenant,
				Finalizers: seed.finalizers,
			}.Build(),
		}
		if !seed.bare {
			methods := seed.methods
			if methods == nil {
				methods = []string{"get"}
			}
			role.Spec = privatev1.RoleSpec_builder{Permissions: []*privatev1.PermissionRule{
				privatev1.PermissionRule_builder{Service: clustersService, Methods: methods}.Build(),
			}}.Build()
		}
		response, err := rolesDAO.Create().SetObject(role.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// bindingSeed describes a RoleBinding row persisted directly through the DAO,
	// bypassing the server's role reference resolution: roleID stores the reference
	// as-is while roleName leaves a legacy name-only reference. Unset tenant defaults
	// to the test tenant, and a seed without role or subjects stores no spec at all.
	type bindingSeed struct {
		name             string
		tenant           string
		project          string
		roleID           string
		roleName         string
		userID           string
		serviceAccountID string
		finalizers       []string
	}

	createBinding := func(seed bindingSeed) *privatev1.RoleBinding {
		tenant := seed.tenant
		if tenant == "" {
			tenant = testTenant
		}
		var role *privatev1.RoleReference
		if seed.roleID != "" {
			role = privatev1.RoleReference_builder{Id: seed.roleID}.Build()
		}
		if seed.roleName != "" {
			role = privatev1.RoleReference_builder{Name: seed.roleName}.Build()
		}
		var users []*privatev1.UserReference
		if seed.userID != "" {
			users = []*privatev1.UserReference{privatev1.UserReference_builder{Id: seed.userID}.Build()}
		}
		var serviceAccounts []*privatev1.ServiceAccountReference
		if seed.serviceAccountID != "" {
			serviceAccounts = []*privatev1.ServiceAccountReference{
				privatev1.ServiceAccountReference_builder{Id: seed.serviceAccountID}.Build(),
			}
		}
		binding := privatev1.RoleBinding_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       seed.name,
				Tenant:     tenant,
				Project:    seed.project,
				Finalizers: seed.finalizers,
			}.Build(),
		}
		if role != nil || users != nil || serviceAccounts != nil {
			binding.Spec = privatev1.RoleBindingSpec_builder{
				Role: role, Users: users, ServiceAccounts: serviceAccounts,
			}.Build()
		}
		response, err := bindingsDAO.Create().SetObject(binding.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// userSeed describes a User row persisted directly through the DAO in the test tenant.
	type userSeed struct {
		name       string
		username   string
		finalizers []string
	}

	createUser := func(seed userSeed) *privatev1.User {
		response, err := usersDAO.Create().SetObject(privatev1.User_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       seed.name,
				Tenant:     testTenant,
				Finalizers: seed.finalizers,
			}.Build(),
			Spec: privatev1.UserSpec_builder{Username: seed.username}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// createProject persists a Project row in the test tenant.
	createProject := func(name string) {
		projectsDAO, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDAO.Create().SetObject(privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	// userSubject and serviceSubject build the subject grants are resolved for.
	userSubject := func(username string) authz.Subject {
		return authz.Subject{Username: username, Tenants: collections.NewSet(testTenant)}
	}
	serviceSubject := func(id string) authz.Subject {
		return authz.Subject{ServiceAccountID: id, Tenants: collections.NewSet(testTenant)}
	}

	// grantsFor resolves the subject's grants in the test tenant through the shared resolver.
	grantsFor := func(subject authz.Subject) *authz.Grants {
		grants, err := resolver.Resolve(ctx, testTenant, subject)
		Expect(err).ToNot(HaveOccurred())
		return grants
	}

	// bindRoleByName creates a binding through the server API using a name-only role
	// reference, so the server resolves and persists the full reference the way a real
	// client's create does.
	bindRoleByName := func(name, roleName, userID string) *privatev1.RoleBinding {
		response, err := bindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Name: roleName}.Build(),
					Users: []*privatev1.UserReference{
						privatev1.UserReference_builder{Id: userID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// updateBindingStatus sets the binding's status state through the server API.
	updateBindingStatus := func(id string, state privatev1.RoleBindingState) *privatev1.RoleBinding {
		response, err := bindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
			Object: privatev1.RoleBinding_builder{Id: id,
				Status: privatev1.RoleBindingStatus_builder{State: state}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// updateBindingFinalizers replaces the binding's finalizers through the server API.
	// The version must match the stored object; a stale version is rejected with Aborted.
	updateBindingFinalizers := func(id string, version int32, finalizers ...string) (*privatev1.RoleBinding, error) {
		response, err := bindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
			Object: privatev1.RoleBinding_builder{Id: id,
				Metadata: privatev1.Metadata_builder{Version: version, Finalizers: finalizers}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		if err != nil {
			return nil, err
		}
		return response.GetObject(), nil
	}

	It("does not use project metadata to scope tenant grants", func() {
		readTenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		readBindings, err := dao.NewGenericDAO[*privatev1.RoleBinding]().SetLogger(logger).SetTenancyLogic(readTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		readRoles, err := dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(readTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		readUsers, err := dao.NewGenericDAO[*privatev1.User]().SetLogger(logger).SetTenancyLogic(readTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		readResolver, err := authz.NewResolver(authz.DAOStore{
			Bindings: readBindings, Roles: readRoles, Users: readUsers,
		}, permissionCatalog)
		Expect(err).ToNot(HaveOccurred())

		createProject("project-1")
		role := createRole(roleSeed{name: "project-reader"})
		projectBinding := createBinding(bindingSeed{
			name: "project-grant", project: "project-1",
			roleID: role.GetId(), serviceAccountID: "project-service",
		})
		tenantBinding := createBinding(bindingSeed{
			name: "tenant-grant", roleID: role.GetId(), serviceAccountID: "project-service",
		})

		grants, err := readResolver.Resolve(ctx, testTenant, serviceSubject("project-service"))
		Expect(err).ToNot(HaveOccurred())
		Expect(grants.Methods).To(ContainElement(clustersGetMethod))
		Expect(grants.Bindings).To(HaveLen(2))
		Expect(grants.Bindings).To(ContainElement(HaveField("BindingID", projectBinding.GetId())))
		Expect(grants.Bindings).To(ContainElement(HaveField("BindingID", tenantBinding.GetId())))
	})

	When("creating a binding", func() {
		It("rejects role references that do not identify exactly one role", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			for _, roleRef := range []*privatev1.RoleReference{
				// A shared tenant without a name resolves to nothing.
				privatev1.RoleReference_builder{Tenant: auth.SharedTenant}.Build(),
				// A name that matches no role.
				privatev1.RoleReference_builder{Name: "missing-role"}.Build(),
				// An ID paired with a mismatched name.
				privatev1.RoleReference_builder{Id: role.GetId(), Name: "wrong-name"}.Build(),
			} {
				_, err := bindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
					Object: privatev1.RoleBinding_builder{
						Metadata: privatev1.Metadata_builder{Name: "invalid-grant", Tenant: testTenant}.Build(),
						Spec:     privatev1.RoleBindingSpec_builder{Role: roleRef}.Build(),
					}.Build(),
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			}
		})

		It("persists the resolved role reference next to the requested name", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})

			binding, err := bindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "alice-grant", Tenant: testTenant}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Name: "stable-reader"}.Build(),
						Users: []*privatev1.UserReference{
							privatev1.UserReference_builder{Id: user.GetId()}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(binding.GetObject().GetSpec().GetRole().GetId()).To(Equal(role.GetId()))
			Expect(binding.GetObject().GetSpec().GetRole().GetName()).To(Equal("stable-reader"))
			Expect(binding.GetObject().GetSpec().GetRole().GetTenant()).To(Equal(testTenant))
			Expect(binding.GetObject().GetSpec().GetRole().GetShared()).To(BeFalse())

			stored, err := bindingsDAO.Get().SetId(binding.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetRole().GetId()).To(Equal(role.GetId()))
		})
	})

	When("updating a binding", func() {
		It("keeps the original role ID when the update repeats the name", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			binding := bindRoleByName("alice-grant", "stable-reader", user.GetId())

			updated, err := bindingsServer.Update(ctx, privatev1.RoleBindingsUpdateRequest_builder{
				Object: privatev1.RoleBinding_builder{Id: binding.GetId(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role: privatev1.RoleReference_builder{Name: "stable-reader"}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.role"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetRole().GetId()).To(Equal(role.GetId()))
			Expect(updated.GetObject().GetSpec().GetRole().GetTenant()).To(Equal(testTenant))
		})

		It("preserves the resolved role reference across status and finalizer updates", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			binding := bindRoleByName("alice-grant", "stable-reader", user.GetId())

			statusUpdated := updateBindingStatus(binding.GetId(), privatev1.RoleBindingState_ROLE_BINDING_STATE_READY)
			Expect(statusUpdated.GetSpec().GetRole().GetId()).To(Equal(role.GetId()))

			finalizerUpdated, err := updateBindingFinalizers(binding.GetId(),
				statusUpdated.GetMetadata().GetVersion(), heldFinalizer)
			Expect(err).ToNot(HaveOccurred())
			Expect(finalizerUpdated.GetSpec().GetRole().GetId()).To(Equal(role.GetId()))
		})

		It("rejects a finalizer update from a stale version", func() {
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			createRole(roleSeed{name: "stable-reader"})
			binding := bindRoleByName("alice-grant", "stable-reader", user.GetId())
			version := binding.GetMetadata().GetVersion()

			_, err := updateBindingFinalizers(binding.GetId(), version, heldFinalizer)
			Expect(err).ToNot(HaveOccurred())
			_, err = updateBindingFinalizers(binding.GetId(), version, "test/other")
			Expect(status.Code(err)).To(Equal(codes.Aborted))
		})

		It("preserves a legacy name-only reference on status and finalizer updates", func() {
			createRole(roleSeed{name: "legacy-shared-role", tenant: auth.SharedTenant, bare: true})
			legacy := createBinding(bindingSeed{name: "legacy-binding", roleName: "legacy-shared-role"})

			statusUpdated := updateBindingStatus(legacy.GetId(), privatev1.RoleBindingState_ROLE_BINDING_STATE_READY)
			Expect(statusUpdated.GetSpec().GetRole().GetId()).To(BeEmpty())

			finalizerUpdated, err := updateBindingFinalizers(legacy.GetId(),
				statusUpdated.GetMetadata().GetVersion(), heldFinalizer)
			Expect(err).ToNot(HaveOccurred())
			Expect(finalizerUpdated.GetSpec().GetRole().GetId()).To(BeEmpty())
			Expect(finalizerUpdated.GetSpec().GetRole().GetName()).To(Equal("legacy-shared-role"))
		})
	})

	When("resolving grants", func() {
		It("grants the role methods to the bound user", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			createBinding(bindingSeed{name: "alice-grant", roleID: role.GetId(), userID: user.GetId()})

			Expect(grantsFor(userSubject("alice")).Methods).To(ContainElement(clustersGetMethod))
		})

		It("grants the role methods to the bound service account", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			createBinding(bindingSeed{name: "service-grant", roleID: role.GetId(), serviceAccountID: "service-1"})

			Expect(grantsFor(serviceSubject("service-1")).Methods).To(ContainElement(clustersGetMethod))
		})

		It("grants nothing to an unbound service account", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			createBinding(bindingSeed{name: "service-grant", roleID: role.GetId(), serviceAccountID: "service-1"})

			Expect(grantsFor(serviceSubject("service-2")).Bindings).To(BeEmpty())
		})
	})

	When("the bound role changes", func() {
		It("reflects updated permissions on the next resolve", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			createBinding(bindingSeed{name: "alice-grant", roleID: role.GetId(), userID: user.GetId()})

			_, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{Id: role.GetId(), Spec: privatev1.RoleSpec_builder{
					Permissions: []*privatev1.PermissionRule{
						privatev1.PermissionRule_builder{Service: clustersService, Methods: []string{"list"}}.Build(),
					}}.Build()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.permissions"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			Expect(grantsFor(userSubject("alice")).Methods).To(ContainElement(clustersListMethod))
			Expect(grantsFor(userSubject("alice")).Methods).ToNot(ContainElement(clustersGetMethod))
		})

		It("keeps grants attached to the original role ID when the role is deleted and recreated by name", func() {
			role := createRole(roleSeed{name: "stable-reader"})
			user := createUser(userSeed{name: "alice-row", username: "alice"})
			bindRoleByName("alice-grant", "stable-reader", user.GetId())
			createBinding(bindingSeed{name: "service-grant", roleID: role.GetId(), serviceAccountID: "service-1"})

			_, err := rolesServer.Delete(ctx, privatev1.RolesDeleteRequest_builder{Id: role.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			createRole(roleSeed{name: "stable-reader", tenant: auth.SharedTenant, methods: []string{"*"}})

			userGrants := grantsFor(userSubject("alice"))
			Expect(userGrants.Methods).To(BeEmpty())
			Expect(userGrants.Bindings).To(BeEmpty())
			Expect(grantsFor(serviceSubject("service-1")).Bindings).To(BeEmpty())
		})
	})

	When("rows are held by finalizers after deletion", func() {
		// heldRows bundles one grant chain - user, role, and binding - with every row
		// held by a finalizer so DAO deletion only stamps it.
		type heldRows struct {
			user    *privatev1.User
			role    *privatev1.Role
			binding *privatev1.RoleBinding
		}

		DescribeTable("stops granting from rows held by finalizers after deletion",
			func(deleteHeld func(rows heldRows) *privatev1.Metadata) {
				user := createUser(userSeed{name: "held-user", username: "held-alice", finalizers: []string{heldFinalizer}})
				role := createRole(roleSeed{name: "held-reader", finalizers: []string{heldFinalizer}})
				binding := createBinding(bindingSeed{
					name: "held-binding", roleID: role.GetId(), userID: user.GetId(),
					finalizers: []string{heldFinalizer},
				})
				Expect(grantsFor(userSubject("held-alice")).Bindings).To(HaveLen(1))

				stored := deleteHeld(heldRows{user: user, role: role, binding: binding})

				Expect(stored.GetDeletionTimestamp()).ToNot(BeNil())
				Expect(grantsFor(userSubject("held-alice")).Bindings).To(BeEmpty())
			},
			Entry("the binding row", func(rows heldRows) *privatev1.Metadata {
				_, err := bindingsDAO.Delete().SetId(rows.binding.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				stored, err := bindingsDAO.Get().SetId(rows.binding.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				return stored.GetObject().GetMetadata()
			}),
			Entry("the role row", func(rows heldRows) *privatev1.Metadata {
				_, err := rolesDAO.Delete().SetId(rows.role.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				stored, err := rolesDAO.Get().SetId(rows.role.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				return stored.GetObject().GetMetadata()
			}),
			Entry("the user row", func(rows heldRows) *privatev1.Metadata {
				_, err := usersDAO.Delete().SetId(rows.user.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				stored, err := usersDAO.Get().SetId(rows.user.GetId()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				return stored.GetObject().GetMetadata()
			}),
		)

		It("lists only the tenant's bindings through the store", func() {
			user := createUser(userSeed{name: "held-user", username: "held-alice", finalizers: []string{heldFinalizer}})
			role := createRole(roleSeed{name: "held-reader", finalizers: []string{heldFinalizer}})
			binding := createBinding(bindingSeed{
				name: "held-binding", roleID: role.GetId(), userID: user.GetId(),
				finalizers: []string{heldFinalizer},
			})
			createBinding(bindingSeed{name: "foreign-held-binding", tenant: auth.SharedTenant})

			page, err := store.ListBindings(ctx, testTenant, 0, 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(page).To(HaveLen(1))
			Expect(page[0].GetId()).To(Equal(binding.GetId()))
		})
	})

	When("resolving a reference to a shared role", func() {
		var shared *privatev1.Role

		BeforeEach(func() {
			shared = createRole(roleSeed{name: "explicit-shared-reader", tenant: auth.SharedTenant, bare: true})
		})

		// bindReference creates a binding through the server API carrying the given
		// role reference and returns the stored binding.
		bindReference := func(ref *privatev1.RoleReference) (*privatev1.RoleBinding, error) {
			response, err := bindingsServer.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "selection", Tenant: testTenant}.Build(),
					Spec:     privatev1.RoleBindingSpec_builder{Role: ref}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		}

		DescribeTable("accepts references that resolve to the shared role",
			func(refFor func() *privatev1.RoleReference) {
				binding, err := bindReference(refFor())
				Expect(err).ToNot(HaveOccurred())
				resolved := binding.GetSpec().GetRole()
				Expect(resolved.GetId()).To(Equal(shared.GetId()))
				Expect(resolved.GetName()).To(Equal("explicit-shared-reader"))
				Expect(resolved.GetTenant()).To(Equal(auth.SharedTenant))
				Expect(resolved.GetShared()).To(BeTrue())
			},
			Entry("an unqualified name", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Name: "explicit-shared-reader"}.Build()
			}),
			Entry("an explicit shared tenant", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Name: "explicit-shared-reader", Tenant: auth.SharedTenant}.Build()
			}),
			Entry("a shared selector", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Name: "explicit-shared-reader", Shared: true}.Build()
			}),
			Entry("the role ID", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Id: shared.GetId()}.Build()
			}),
		)

		DescribeTable("rejects references that do not identify the shared role",
			func(refFor func() *privatev1.RoleReference) {
				_, err := bindReference(refFor())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			},
			Entry("conflicting tenant and shared selectors", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Name: "explicit-shared-reader", Tenant: testTenant, Shared: true}.Build()
			}),
			Entry("a tenant that does not own the role", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Id: shared.GetId(), Tenant: testTenant}.Build()
			}),
			Entry("a name that does not match the ID", func() *privatev1.RoleReference {
				return privatev1.RoleReference_builder{Id: shared.GetId(), Name: "other"}.Build()
			}),
		)
	})
})
