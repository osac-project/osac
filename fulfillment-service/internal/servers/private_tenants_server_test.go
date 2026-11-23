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
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private tenants server (Tenant API)", func() {
	var (
		tenantsServer  *PrivateTenantsServer
		projectsServer *PrivateProjectsServer
	)

	BeforeEach(func() {
		var err error

		// Create the projects server:
		projectsServer, err = NewPrivateProjectsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create server:
		tenantsServer, err = NewPrivateTenantsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Tenant default Role", func() {
		const (
			sharedRoleName = "test-shared-default"
			localRoleName  = "test-local-default"
			sharedRoleID   = "test-shared-role"
			localRoleID    = "test-local-role"
			foreignRoleID  = "test-foreign-role"
			foreignTenant  = "foreign-tenant"
		)

		updateRequest := func(tenant, path string, spec *privatev1.TenantSpec) *privatev1.TenantsUpdateRequest {
			return privatev1.TenantsUpdateRequest_builder{
				Object:     privatev1.Tenant_builder{Id: tenant, Spec: spec}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
			}.Build()
		}
		setDefault := func(tenant string, ref *privatev1.RoleReference) (*privatev1.TenantsUpdateResponse, error) {
			return tenantsServer.Update(ctx, updateRequest(tenant, defaultRoleField,
				privatev1.TenantSpec_builder{DefaultRole: ref}.Build()))
		}
		readSpec := func(tenant string) *privatev1.TenantSpec {
			response, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant}.Build())
			Expect(err).NotTo(HaveOccurred())
			return response.GetObject().GetSpec()
		}

		BeforeEach(func() {
			_, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: foreignTenant}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = suiteTx.Exec(ctx, `
				insert into roles (id, name, tenant, data)
				values ($1, $2, $3, '{}'), ($4, $5, $6, '{}'), ($7, 'test-foreign-default', $8, '{}')
			`, sharedRoleID, sharedRoleName, auth.SharedTenant, localRoleID, localRoleName, testTenant,
				foreignRoleID, foreignTenant)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("stores the canonical selection",
			func(tenant string, selector, want privatev1.RoleReference_builder) {
				response, err := setDefault(tenant, selector.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(response.GetObject().GetSpec().GetDefaultRole(), want.Build())).To(BeTrue())
				Expect(proto.Equal(readSpec(tenant).GetDefaultRole(), want.Build())).To(BeTrue())
			},
			Entry("selects a local name", testTenant,
				privatev1.RoleReference_builder{Name: localRoleName},
				privatev1.RoleReference_builder{Id: localRoleID, Name: localRoleName, Tenant: testTenant}),
			Entry("falls back to the shared name on a local miss", testTenant,
				privatev1.RoleReference_builder{Name: sharedRoleName},
				privatev1.RoleReference_builder{Id: sharedRoleID, Name: sharedRoleName, Tenant: auth.SharedTenant, Shared: true}),
			Entry("selects an explicitly shared name", testTenant,
				privatev1.RoleReference_builder{Name: sharedRoleName, Shared: true},
				privatev1.RoleReference_builder{Id: sharedRoleID, Name: sharedRoleName, Tenant: auth.SharedTenant, Shared: true}),
			Entry("selects an explicitly scoped name", testTenant,
				privatev1.RoleReference_builder{Name: localRoleName, Tenant: testTenant},
				privatev1.RoleReference_builder{Id: localRoleID, Name: localRoleName, Tenant: testTenant}),
			Entry("sets the shared Tenant default by shared Role ID", auth.SharedTenant,
				privatev1.RoleReference_builder{Id: sharedRoleID},
				privatev1.RoleReference_builder{Id: sharedRoleID, Name: sharedRoleName, Tenant: auth.SharedTenant, Shared: true}),
		)

		DescribeTable("preserves the stored default after rejecting a selection",
			func(tenant string, selector privatev1.RoleReference_builder, want grpccodes.Code) {
				initial, err := setDefault(tenant, privatev1.RoleReference_builder{Id: sharedRoleID}.Build())
				Expect(err).NotTo(HaveOccurred())
				_, err = setDefault(tenant, selector.Build())
				Expect(grpcstatus.Code(err)).To(Equal(want), "%v", err)
				Expect(proto.Equal(readSpec(tenant).GetDefaultRole(), initial.GetObject().GetSpec().GetDefaultRole())).To(BeTrue())
			},
			Entry("empty reference", testTenant, privatev1.RoleReference_builder{}, grpccodes.InvalidArgument),
			Entry("missing ID", testTenant, privatev1.RoleReference_builder{Id: "missing"}, grpccodes.FailedPrecondition),
			Entry("missing name", testTenant, privatev1.RoleReference_builder{Name: "missing"}, grpccodes.FailedPrecondition),
			Entry("explicit local name cannot fall back to shared", testTenant,
				privatev1.RoleReference_builder{Name: sharedRoleName, Tenant: testTenant}, grpccodes.FailedPrecondition),
			Entry("foreign scope", testTenant,
				privatev1.RoleReference_builder{Id: foreignRoleID, Tenant: foreignTenant}, grpccodes.InvalidArgument),
			Entry("foreign ID without a tenant selector", testTenant,
				privatev1.RoleReference_builder{Id: foreignRoleID}, grpccodes.InvalidArgument),
			Entry("conflicting selectors", testTenant,
				privatev1.RoleReference_builder{Id: sharedRoleID, Tenant: testTenant, Shared: true}, grpccodes.InvalidArgument),
			Entry("scope mismatch", testTenant,
				privatev1.RoleReference_builder{Id: sharedRoleID, Tenant: testTenant}, grpccodes.InvalidArgument),
			Entry("project mismatch", testTenant,
				privatev1.RoleReference_builder{Id: localRoleID, Project: "other-project"}, grpccodes.InvalidArgument),
			Entry("name mismatch", testTenant,
				privatev1.RoleReference_builder{Id: sharedRoleID, Name: "another-role"}, grpccodes.InvalidArgument),
			Entry("shared Tenant rejects a non-shared Role ID", auth.SharedTenant,
				privatev1.RoleReference_builder{Id: localRoleID}, grpccodes.InvalidArgument),
			Entry("shared Tenant rejects an explicit non-shared scope", auth.SharedTenant,
				privatev1.RoleReference_builder{Name: localRoleName, Tenant: testTenant}, grpccodes.InvalidArgument),
		)

		It("rejects a deleting Role", func() {
			initial, err := setDefault(testTenant, privatev1.RoleReference_builder{Id: sharedRoleID}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = suiteTx.Exec(ctx, "update roles set deletion_timestamp = now() where id = $1", localRoleID)
			Expect(err).NotTo(HaveOccurred())

			_, err = setDefault(testTenant, privatev1.RoleReference_builder{Id: localRoleID}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("is being deleted"))
			Expect(proto.Equal(readSpec(testTenant).GetDefaultRole(), initial.GetObject().GetSpec().GetDefaultRole())).To(BeTrue())
		})

		It("validates a masked name against the stored ID", func() {
			initial, err := setDefault(testTenant, privatev1.RoleReference_builder{Id: localRoleID}.Build())
			Expect(err).NotTo(HaveOccurred())

			_, err = tenantsServer.Update(ctx, updateRequest(testTenant, "spec.default_role.name",
				privatev1.TenantSpec_builder{
					DefaultRole: privatev1.RoleReference_builder{Name: sharedRoleName}.Build(),
				}.Build()))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(proto.Equal(readSpec(testTenant).GetDefaultRole(), initial.GetObject().GetSpec().GetDefaultRole())).To(BeTrue())
		})

		It("updates domains while ignoring an empty Role reference outside the mask", func() {
			initial, err := setDefault(testTenant, privatev1.RoleReference_builder{Id: localRoleID}.Build())
			Expect(err).NotTo(HaveOccurred())

			_, err = tenantsServer.Update(ctx, updateRequest(testTenant, "spec.domains", privatev1.TenantSpec_builder{
				Domains:     []string{"example.com"},
				DefaultRole: &privatev1.RoleReference{},
			}.Build()))
			Expect(err).NotTo(HaveOccurred())
			spec := readSpec(testTenant)
			Expect(spec.GetDomains()).To(Equal([]string{"example.com"}))
			Expect(proto.Equal(spec.GetDefaultRole(), initial.GetObject().GetSpec().GetDefaultRole())).To(BeTrue())
		})

		DescribeTable("clears the stored default",
			func(tenant string) {
				_, err := setDefault(tenant, privatev1.RoleReference_builder{Id: sharedRoleID}.Build())
				Expect(err).NotTo(HaveOccurred())

				_, err = setDefault(tenant, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(readSpec(tenant).HasDefaultRole()).To(BeFalse())
			},
			Entry("ordinary Tenant", testTenant),
			Entry("shared Tenant", auth.SharedTenant),
		)
	})

	It("Creates a tenant", func() {
		request := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()

		response, err := tenantsServer.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object).ToNot(BeNil())
		Expect(response.Object.Id).ToNot(BeEmpty())
		Expect(response.Object.Metadata.Name).To(Equal("my-tenant"))
	})

	It("Lists tenants", func() {
		createReq := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()
		_, err := tenantsServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		listResp, err := tenantsServer.List(ctx, &privatev1.TenantsListRequest{
			Filter: new("this.metadata.name == 'my-tenant'"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Size).To(Equal(int32(1)))
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Metadata.Name).To(Equal("my-tenant"))
	})

	It("Gets a tenant by ID", func() {
		createReq := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := tenantsServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		getResp, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: createResp.Object.Id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Id).To(Equal(createResp.Object.Id))
		Expect(getResp.Object.Metadata.Name).To(Equal("my-tenant"))
	})

	It("Cannot delete a tenant that still has projects, and no finalizers", func() {
		// Create a tenant, without finalizers, the system will automatically create the empty project:
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		tenant := createResponse.GetObject()

		// Try to delete the tenant, and verify that it fails because it still has the default project:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(Equal(
			"tenant 'my-tenant' cannot be deleted because it still has projects",
		))
	})

	It("Can delete a tenant that still has projects, and finalizers", func() {
		// Create a tenant with a finalizer, so the system will not immediately delete it.
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-tenant",
						Finalizers: []string{
							"my-finalizer",
						},
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		tenant := createResponse.GetObject()

		// Try to delete the tenant, and verify that it succeeds:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the tenant still exists, but with the deletion timestamp set:
		getResponse, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenant = getResponse.GetObject()
		Expect(tenant.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
	})

	It("Can delete a tenant after deleting all the projects", func() {
		// Create a tenant, with finalizers, the system will automatically create the empty project:
		createReq := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := tenantsServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Delete the default project:
		listProjectsResponse, err := projectsServer.List(
			ctx,
			privatev1.ProjectsListRequest_builder{
				Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.name == ''"),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		projects := listProjectsResponse.GetItems()
		Expect(projects).To(HaveLen(1))
		project := projects[0]
		_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: project.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Try to delete the tenant, and verify that it succeeds:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: createResp.Object.Id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("archives a tenant only after both cleanup barriers are removed and rejects stale locked updates", func() {
		created, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       "barrier-tenant",
					Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		id := created.GetObject().GetId()
		projects, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'barrier-tenant'"),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, project := range projects.GetItems() {
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: project.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		deleting, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		stale := proto.Clone(deleting.GetObject()).(*privatev1.Tenant)
		first := proto.Clone(stale).(*privatev1.Tenant)
		first.GetMetadata().SetFinalizers([]string{finalizers.TenantOnboarding})
		mask := &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}
		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{Object: first, UpdateMask: mask, Lock: true}.Build())
		Expect(err).NotTo(HaveOccurred())
		remaining, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf(finalizers.TenantOnboarding))
		stale.GetMetadata().SetFinalizers(nil)
		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{Object: stale, UpdateMask: mask, Lock: true}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Aborted))
		remaining.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{Object: remaining.GetObject(), UpdateMask: mask, Lock: true}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})

	It("Updates a tenant", func() {
		createReq := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := tenantsServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		updateReq := privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: createResp.Object.Id,
				Status: privatev1.TenantStatus_builder{
					State: privatev1.TenantState_TENANT_STATE_SYNCED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{
					"status.state",
				},
			},
		}.Build()
		updateResp, err := tenantsServer.Update(ctx, updateReq)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Status.State).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	})

	It("Rejects creation of a tenant with an empty name", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal(
			"field 'metadata.name' is mandatory",
		))
	})

	It("Rejects creation of a tenant with an identifier different from the name", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: "your-tenant",
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal(
			"field 'id' must be empty or equal to field 'metadata.name'",
		))
	})

	It("Uses the name as the identifier if no identifier is provided", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetObject().GetId()).To(Equal("my-tenant"))
	})

	It("Rejects an explicit tenant different than the name", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-tenant",
					Tenant: "your-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal(
			"field 'metadata.tenant' must be empty or equal to field 'metadata.name'",
		))
	})

	It("Uses the name as the tenant if no tenant is provided", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal("my-tenant"))
	})

	It("Rejects update of the name of a tenant", func() {
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		id := object.GetId()
		updateResponse, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Name: "your-name",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{
					"metadata.name",
				},
			},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(updateResponse).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal(
			"field 'metadata.name' is immutable",
		))
	})

	It("Rejects creation of a tenant with a duplicate name", func() {
		// Try to create the tenant once, should succeed:
		request := privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build()
		_, err := tenantsServer.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())

		// Try again with the same request, should fail:
		response, err := tenantsServer.Create(ctx, request)
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
		Expect(status.Message()).To(Equal(
			"tenant 'my-tenant' already exists",
		))
	})

	It("Rejects update of the tenant of a tenant", func() {
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		id := object.GetId()
		updateResponse, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Tenant: "your-tenant",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{
					"metadata.tenant",
				},
			},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(updateResponse).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal(
			"field 'metadata.tenant' is immutable",
		))
	})

	// Domain validation has been migrated to protovalidate in tenant_type.proto.
	// Domain validation is now tested via integration tests in it/it_tenant_domain_validation_test.go
	// which verify the end-to-end validation flow including interceptor and update_mask handling.

	It("Automatically creates the default project when a tenant is created", func() {
		// Create a tenant:
		_, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the default project was created:
		listProjectsResponse, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.name == ''"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projects := listProjectsResponse.GetItems()
		Expect(projects).To(HaveLen(1))
		project := projects[0]
		Expect(project.GetMetadata().GetName()).To(Equal(""))
		Expect(project.GetMetadata().GetTenant()).To(Equal("my-tenant"))
	})

	It("Can't delete a tenant that only has the default project", func() {
		// Create a tenant:
		createTenantResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenant := createTenantResponse.GetObject()

		// Try to delete the tenant, and verify that it fails because it still has the default project:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		Expect(err).To(HaveOccurred())
	})

	It("Can delete a tenant after deleting all projects", func() {
		// Create a tenant:
		createTenantResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenant := createTenantResponse.GetObject()

		// Find and delete the projects:
		listProjectsResponse, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projects := listProjectsResponse.GetItems()
		for _, project := range projects {
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: project.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}

		// Delete the tenant:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("External billing id", func() {
		createTenant := func(name, billingID string) *privatev1.Tenant {
			GinkgoHelper()
			spec := &privatev1.TenantSpec{}
			if billingID != "" {
				spec.SetExternalBillingId(billingID)
			}
			response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			return response.GetObject()
		}

		It("Stores the id on create and returns it from Get and List", func() {
			created := createTenant("billing-tenant", "acct-1")
			Expect(created.GetSpec().GetExternalBillingId()).To(Equal("acct-1"))

			var stored *string
			err := suiteTx.QueryRow(ctx, `
				select data #>> '{spec,external_billing_id}'
				from tenants
				where name = $1
			`, "billing-tenant").Scan(&stored)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored).NotTo(BeNil())
			Expect(*stored).To(Equal("acct-1"))

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetExternalBillingId()).To(Equal("acct-1"))

			listed, err := tenantsServer.List(ctx, &privatev1.TenantsListRequest{
				Filter: new("this.metadata.name == 'billing-tenant'"),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
			Expect(listed.GetItems()[0].GetSpec().GetExternalBillingId()).To(Equal("acct-1"))
		})

		It("Creates a tenant when the id is omitted", func() {
			created := createTenant("billing-tenant", "")
			Expect(created.GetSpec().HasExternalBillingId()).To(BeFalse())

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().HasExternalBillingId()).To(BeFalse())
		})

		It("Replaces the id when an update sends a new value", func() {
			created := createTenant("billing-tenant", "acct-1")
			updated := proto.Clone(created).(*privatev1.Tenant)
			updated.GetSpec().SetExternalBillingId("acct-2")
			_, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: updated,
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetExternalBillingId()).To(Equal("acct-2"))
		})

		It("Keeps the id when an update sends the same value", func() {
			created := createTenant("billing-tenant", "acct-1")
			_, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: proto.Clone(created).(*privatev1.Tenant),
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetExternalBillingId()).To(Equal("acct-1"))
		})

		It("Keeps the id when an update mask does not name it", func() {
			created := createTenant("billing-tenant", "acct-1")
			updated := proto.Clone(created).(*privatev1.Tenant)
			updated.GetSpec().ClearExternalBillingId()
			_, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: updated,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"metadata.finalizers"},
				},
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetExternalBillingId()).To(Equal("acct-1"))
		})

		It("Clears the id when an update omits it", func() {
			created := createTenant("billing-tenant", "acct-1")
			updated := proto.Clone(created).(*privatev1.Tenant)
			updated.GetSpec().ClearExternalBillingId()
			_, err := tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: updated,
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().HasExternalBillingId()).To(BeFalse())
		})

		It("Accepts an id of 256 characters and rejects a longer one", func() {
			interceptor, err := validation.NewProtovalidateInterceptor().SetLogger(logger).Build()
			Expect(err).NotTo(HaveOccurred())
			create := func(name, billingID string) error {
				spec := &privatev1.TenantSpec{}
				spec.SetExternalBillingId(billingID)
				request := privatev1.TenantsCreateRequest_builder{
					Object: privatev1.Tenant_builder{
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Spec:     spec,
					}.Build(),
				}.Build()
				_, err := interceptor.UnaryServer(ctx, request, &grpc.UnaryServerInfo{
					FullMethod: "/osac.private.v1.Tenants/Create",
				}, func(ctx context.Context, req any) (any, error) {
					return tenantsServer.Create(ctx, req.(*privatev1.TenantsCreateRequest))
				})
				return err
			}

			Expect(create("billing-ok", strings.Repeat("a", 256))).To(Succeed())
			err = create("billing-long", strings.Repeat("a", 257))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			_, err = tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: "billing-long"}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))

			created := createTenant("billing-update", "acct-1")
			updated := proto.Clone(created).(*privatev1.Tenant)
			updated.GetSpec().SetExternalBillingId(strings.Repeat("b", 257))
			_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: updated,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.external_billing_id"},
				},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			got, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetExternalBillingId()).To(Equal("acct-1"))
		})
	})

	It("Deletes the quota outbox with the accepted delete and archives the tenant after quota cleanup", func() {
		limitsServer, err := NewPrivateQuotaLimitsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		thresholdsServer, err := NewPrivateQuotaThresholdsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		requestsServer, err := NewPrivateQuotaLimitRequestsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// The lifecycle finalizer keeps the accepted delete soft, like a live
		// tenant whose reconciler still has to run:
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       "quota-tenant",
					Finalizers: []string{finalizers.TenantLifecycle},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantID := createResponse.GetObject().GetId()

		projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDao.Create().SetObject(privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "team",
				Tenant: "quota-tenant",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Quota rows in every scope: a tenant override, a project limit, a
		// project threshold and a pending limit request:
		override, err := limitsServer.Create(ctx, privatev1.QuotaLimitsCreateRequest_builder{
			Object: privatev1.QuotaLimit_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "quota-tenant"}.Build(),
				Spec: privatev1.QuotaLimitSpec_builder{
					Dimension: "compute.vcpus",
					Value:     proto.Int64(10),
					Scope:     privatev1.QuotaScope_QUOTA_SCOPE_TENANT.Enum(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projectLimit, err := limitsServer.Create(ctx, privatev1.QuotaLimitsCreateRequest_builder{
			Object: privatev1.QuotaLimit_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "quota-tenant", Project: "team"}.Build(),
				Spec: privatev1.QuotaLimitSpec_builder{
					Dimension: "compute.vcpus",
					Value:     proto.Int64(4),
					Scope:     privatev1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		threshold, err := thresholdsServer.Create(ctx, privatev1.QuotaThresholdsCreateRequest_builder{
			Object: privatev1.QuotaThreshold_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "quota-tenant", Project: "team"}.Build(),
				Spec: privatev1.QuotaThresholdSpec_builder{
					Dimension: "compute.vcpus",
					Percent:   int32Ptr(3),
					Scope:     privatev1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		limitRequest, err := requestsServer.Create(ctx, privatev1.QuotaLimitRequestsCreateRequest_builder{
			Object: privatev1.QuotaLimitRequest_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "quota-tenant"}.Build(),
				Spec:     privatev1.QuotaLimitRequestSpec_builder{Dimension: "compute.vcpus", RequestedValue: proto.Int64(20)}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// An unsent quota warning, which has no API of its own:
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into quota_outbox (tenant, project, dimension, data) values ($1, '', 'compute.vcpus', '{}')`,
			"quota-tenant")
		Expect(err).ToNot(HaveOccurred())

		// Accept the tenant deletion:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: tenantID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// The unsent warnings are gone with the accepted delete:
		var outboxCount int
		err = tx.QueryRow(ctx,
			`select count(*) from quota_outbox where tenant = $1`, "quota-tenant").Scan(&outboxCount)
		Expect(err).ToNot(HaveOccurred())
		Expect(outboxCount).To(Equal(0))

		// The tenant reconciler deletes the tenant's quota rows through the
		// quota API before the projects are archived:
		for _, limitID := range []string{override.GetObject().GetId(), projectLimit.GetObject().GetId()} {
			_, err = limitsServer.Delete(ctx, privatev1.QuotaLimitsDeleteRequest_builder{Id: limitID}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		_, err = thresholdsServer.Delete(ctx, privatev1.QuotaThresholdsDeleteRequest_builder{
			Id: threshold.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = requestsServer.Delete(ctx, privatev1.QuotaLimitRequestsDeleteRequest_builder{
			Id: limitRequest.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// With the quota rows gone the projects can be archived, child projects
		// before the root project they reference, like the project reconciler
		// orders them:
		projects, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'quota-tenant'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(projects.GetItems()).To(HaveLen(2))
		for _, project := range projects.GetItems() {
			if project.GetMetadata().GetName() == "" {
				continue
			}
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: project.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		for _, project := range projects.GetItems() {
			if project.GetMetadata().GetName() != "" {
				continue
			}
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: project.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}

		// Clearing the finalizer archives the tenant, which the quota foreign
		// keys used to block:
		deleting, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
		Expect(err).ToNot(HaveOccurred())
		clearing := proto.Clone(deleting.GetObject()).(*privatev1.Tenant)
		clearing.GetMetadata().SetFinalizers(nil)
		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object:     clearing,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})

})

var _ = Describe("Break-glass credentials secret reference", func() {
	var tenantsServer *PrivateTenantsServer

	BeforeEach(func() {
		var err error
		tenantsServer, err = NewPrivateTenantsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects a nonexistent break_glass_credentials_secret", func() {
		_, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "missing-secret-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
						Id: "nonexistent-secret",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("there is no secret"))
	})

	It("Rejects an empty break_glass_credentials_secret reference", func() {
		_, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "empty-secret-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("must specify id or name"))
	})

	It("Accepts a pre-existing break_glass_credentials_secret by id", func() {
		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:   "existing-bg-secret",
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Metadata: privatev1.Metadata_builder{
				Name:   "existing-bg",
				Tenant: testTenant,
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "preexisting-secret-tenant",
				}.Build(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
						Id: "existing-bg-secret",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ref := response.GetObject().GetSpec().GetBreakGlassCredentialsSecret()
		Expect(ref.GetId()).To(Equal("existing-bg-secret"))
		Expect(ref.GetName()).To(Equal("existing-bg"))
		Expect(response.GetObject().GetStatus().GetBreakGlassCredentials()).To(BeNil())
	})

	It("Deletes the break-glass secret on soft-delete even when projects remain", func() {
		// A finalizer makes Delete a soft-delete so the secret removal commits.
		// Without one, hard-delete hits projects_tenant_fk, aborts the shared
		// test transaction, and rolls the secret delete back.
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       "secret-blocked-tenant",
					Finalizers: []string{"test-finalizer"},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:   "tenant-bg-secret",
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Metadata: privatev1.Metadata_builder{
				Name:   breakGlassCredentialsSecretName,
				Tenant: "secret-blocked-tenant",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: createResponse.GetObject().GetId(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
						Id: "tenant-bg-secret",
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.break_glass_credentials_secret"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = secretsDao.Get().SetId("tenant-bg-secret").Do(ctx)
		Expect(err).To(HaveOccurred())
		var notFound *dao.ErrNotFound
		Expect(errors.As(err, &notFound)).To(BeTrue())

		getResponse, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(getResponse.GetObject().GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
	})

	It("Can soft-delete a tenant when the break-glass secret was already deleted", func() {
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       "predeleted-secret-tenant",
					Finalizers: []string{"test-finalizer"},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:   "predeleted-bg-secret",
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Metadata: privatev1.Metadata_builder{
				Name:   breakGlassCredentialsSecretName,
				Tenant: "predeleted-secret-tenant",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: createResponse.GetObject().GetId(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
						Id: "predeleted-bg-secret",
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.break_glass_credentials_secret"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = secretsDao.Delete().SetId("predeleted-bg-secret").Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		getResponse, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(getResponse.GetObject().GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
	})

	It("Allows Update to clear a stale break-glass secret reference after the secret was deleted", func() {
		createResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "stale-ref-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:   "stale-bg-secret",
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Metadata: privatev1.Metadata_builder{
				Name:   breakGlassCredentialsSecretName,
				Tenant: "stale-ref-tenant",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: createResponse.GetObject().GetId(),
				Spec: privatev1.TenantSpec_builder{
					BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
						Id: "stale-bg-secret",
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.break_glass_credentials_secret"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = secretsDao.Delete().SetId("stale-bg-secret").Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: privatev1.Tenant_builder{
				Id: createResponse.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		getResponse, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
	})
})
