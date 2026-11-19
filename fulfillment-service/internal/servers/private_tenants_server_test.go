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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
		// A live row that is still being deleted keeps the ordinary duplicate error.
		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := tenantsServer.Create(spCtx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: "barrier-tenant"}.Build(),
				}.Build(),
			}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
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
		// The finalized unbilled deletion permanently reserves the name; billing history is not required.
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := tenantsServer.Create(spCtx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: "barrier-tenant"}.Build(),
				}.Build(),
			}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(grpcstatus.Convert(err).Message()).To(Equal("tenant name is reserved by a previous deleted tenant"))
		Expect(grpcstatus.Convert(err).Details()[0].(*errdetails.ErrorInfo).GetReason()).
			To(Equal("TENANT_IDENTITY_RETAINED"))
		// A never-used name remains eligible for normal onboarding.
		_, err = tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{Name: "never-used-tenant"}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
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

	It("Deletes a tenant that has a billing assignment without rejecting the delete or "+
		"cascade-deleting the assignment", func() {
		// Create a tenant with no finalizers, so deletion is attempted immediately once its default project is
		// gone.
		createTenantResponse, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "billed-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenant := createTenantResponse.GetObject()

		listProjectsResponse, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'billed-tenant'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		for _, project := range listProjectsResponse.GetItems() {
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
				Id: project.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}

		// Give the tenant a billing assignment, including a closed (historical) interval, so
		// both the current and a past row reference it.
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		account1 := seedTenantBillingAccount(ctx)
		account2 := seedTenantBillingAccount(ctx)
		firstAssignment := seedTenantBillingInterval(ctx, "billed-tenant", account1, "billed-tenant-account-1", true)
		currentAssignment := seedTenantBillingInterval(ctx, "billed-tenant", account2, "billed-tenant-account-2", false)
		historyBefore, err := readTenantBillingInterval(ctx, firstAssignment)
		Expect(err).ToNot(HaveOccurred())
		currentBefore, err := readTenantBillingInterval(ctx, currentAssignment)
		Expect(err).ToNot(HaveOccurred())
		floor := historyBefore.GetStatus().GetEffectiveFromTimestamp().AsTime()
		for _, timestamp := range []*timestamppb.Timestamp{
			historyBefore.GetStatus().GetEffectiveToTimestamp(),
			currentBefore.GetStatus().GetEffectiveFromTimestamp(),
		} {
			if at := timestamp.AsTime(); at.After(floor) {
				floor = at
			}
		}

		// Delete the tenant: it must proceed, not be rejected because an assignment
		// references it. Bound the shared post-lock clock sample with real database time,
		// not the ambient transaction's earlier now() used for the tenant deletion timestamp.
		var beforeDelete, afterDelete time.Time
		Expect(tx.QueryRow(ctx, "select clock_timestamp()").Scan(&beforeDelete)).To(Succeed())
		deleteCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "authenticated-billing-deleter"})
		_, err = tenantsServer.Delete(deleteCtx, privatev1.TenantsDeleteRequest_builder{
			Id: tenant.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.QueryRow(ctx, "select clock_timestamp()").Scan(&afterDelete)).To(Succeed())

		// Neither assignment row was cascade-deleted; history stays intact as a tombstone.
		closedRow, err := readTenantBillingInterval(ctx, firstAssignment)
		Expect(err).ToNot(HaveOccurred())
		Expect(closedRow.GetSpec().GetTenant().GetId()).To(Equal("billed-tenant"))
		Expect(closedRow.GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		Expect(closedRow).To(Equal(historyBefore), "already closed history must not be rewritten by deletion")
		currentRow, err := readTenantBillingInterval(ctx, currentAssignment)
		Expect(err).ToNot(HaveOccurred())
		Expect(currentRow.GetSpec().GetTenant().GetId()).To(Equal("billed-tenant"))
		Expect(currentRow.GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		Expect(currentRow.GetStatus().GetMessage()).To(Equal(database.TenantBillingAssignmentDeletedMessage))
		Expect(currentRow.GetStatus().GetConnectionState()).To(Equal(
			privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

		// Finalized tenants leave live storage; the durable identity retains deletion evidence.
		var deletedAt time.Time
		err = tx.QueryRow(ctx,
			"select deletion_timestamp from deleted_tenant_identities where tenant_name = 'billed-tenant'").Scan(&deletedAt)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedAt.After(time.Unix(1, 0))).To(BeTrue(),
			"the live tenant row must carry the deletion timestamp, not the epoch default")
		closedAt := currentRow.GetStatus().GetEffectiveToTimestamp()
		Expect(closedAt.CheckValid()).To(Succeed())
		Expect(closedAt.AsTime()).To(BeTemporally(">", time.Unix(1, 0)))
		Expect(closedAt.AsTime()).To(BeTemporally(">=", beforeDelete),
			"the interval must close using the post-lock database clock, not transaction now()")
		Expect(closedAt.AsTime()).To(BeTemporally("==", deletedAt), "first acceptance and closure share one explicit sample")
		Expect(closedAt.AsTime()).To(BeTemporally(">=", floor),
			"the close must not precede any historical boundary or the current interval start")
		upperBound := afterDelete
		if floor.After(upperBound) {
			upperBound = floor
		}
		Expect(closedAt.AsTime()).To(BeTemporally("<=", upperBound),
			"the close must not exceed the post-delete database clock or its history-floor clamp")
		var persistedEnd time.Time
		Expect(tx.QueryRow(ctx, "select effective_to from tenant_billing_assignments where id = $1",
			currentAssignment).Scan(&persistedEnd)).To(Succeed())
		Expect(persistedEnd).To(BeTemporally("==", closedAt.AsTime()))
		var archived int
		err = tx.QueryRow(ctx,
			"select count(*) from archived_tenants where name = 'billed-tenant'").Scan(&archived)
		Expect(err).ToNot(HaveOccurred())
		Expect(archived).To(Equal(1))
		marker, found, err := database.GetDeletedTenantIdentity(ctx, tenant.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(marker.DeletedBy).To(Equal(new("authenticated-billing-deleter")))
		Expect(marker.FinalizedAt).NotTo(BeNil())
		Expect(*marker.FinalizedAt).To(BeTemporally(">=", deletedAt))
		privateList, err := tenantsServer.List(ctx, privatev1.TenantsListRequest_builder{
			Filter: new(`this.metadata.name == "billed-tenant"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateList.GetItems()).To(BeEmpty())
		publicServer, err := NewTenantsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		publicList, err := publicServer.List(ctx, publicv1.TenantsListRequest_builder{
			Filter: new(`this.metadata.name == "billed-tenant"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicList.GetTotal()).To(BeZero())
		Expect(publicList.GetItems()).To(BeEmpty())
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := publicServer.Get(c, publicv1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := tenantsServer.Get(c, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		// Repeated Delete after physical removal is NotFound and cannot rewrite retained history.
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := tenantsServer.Delete(c, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		repeated, err := readTenantBillingInterval(ctx, currentAssignment)
		Expect(err).ToNot(HaveOccurred())
		Expect(repeated).To(Equal(currentRow))
		repeatedHistory, err := readTenantBillingInterval(ctx, firstAssignment)
		Expect(err).ToNot(HaveOccurred())
		Expect(repeatedHistory).To(Equal(historyBefore))
		var repeatedDeletedAt time.Time
		Expect(tx.QueryRow(ctx, "select deletion_timestamp from deleted_tenant_identities where tenant_name = 'billed-tenant'").
			Scan(&repeatedDeletedAt)).To(Succeed())
		Expect(repeatedDeletedAt).To(BeTemporally("==", deletedAt))

		_, err = tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{Name: "replacement-tenant"}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		replacementTenant, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: "replacement-tenant"}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(replacementTenant.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
		replacement := seedTenantBillingInterval(ctx, "replacement-tenant", account2, "replacement-account", false)
		active, err := readTenantBillingInterval(ctx, replacement)
		Expect(err).ToNot(HaveOccurred())
		Expect(active.GetSpec().GetTenant().GetId()).To(Equal("replacement-tenant"))
		Expect(active.GetSpec().GetBillingAccount().GetId()).To(Equal(account2))
		Expect(active.GetStatus().HasEffectiveToTimestamp()).To(BeFalse())

		// Billed identities remain reserved even after all intervals close. Isolate the retained-history
		// error so the assertions above can commit rather than being undone by this rejected create.
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := tenantsServer.Create(spCtx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: "billed-tenant"}.Build(),
				}.Build(),
			}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(grpcstatus.Convert(err).Message()).To(Equal("tenant name is reserved by a previous deleted tenant"))
		details := grpcstatus.Convert(err).Details()
		Expect(details).To(HaveLen(1))
		Expect(details[0].(*errdetails.ErrorInfo).GetReason()).To(Equal("TENANT_IDENTITY_RETAINED"))
	})

	// insertBillingHistory gives the tenant one closed and one current billing assignment, so both a
	// past and the active row reference it. It mirrors the helpers of the assignment-only spec above.
	insertBillingHistory := func(tenant string) (closed, current string) {
		GinkgoHelper()
		closed = seedTenantBillingInterval(ctx, tenant, seedTenantBillingAccount(ctx), tenant+"-account-1", true)
		current = seedTenantBillingInterval(ctx, tenant, seedTenantBillingAccount(ctx), tenant+"-account-2", false)
		return
	}

	It("Archives the billed tenant with only closed history after finalizer cleanup", func() {
		created, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "history-only-tenant", Finalizers: []string{finalizers.TenantLifecycle},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		first, second := insertBillingHistory(id)
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		floor, err := dao.LockTenantBillingAssignmentForDeletion(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		at, err := dao.BillingFinalizationTime(ctx, floor)
		Expect(err).ToNot(HaveOccurred())
		Expect(dao.CloseTenantBillingAssignmentOnDeletion(ctx, id, at)).To(Succeed())
		firstBefore, err := readTenantBillingInterval(ctx, first)
		Expect(err).ToNot(HaveOccurred())
		secondBefore, err := readTenantBillingInterval(ctx, second)
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantsServer.Delete(auth.ContextWithSubject(ctx, &auth.Subject{User: "original-deleter"}),
			privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		projects, err := projectsServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new(`this.metadata.tenant == "history-only-tenant"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		for _, project := range projects.GetItems() {
			_, err = projectsServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: project.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		deleting, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		deleting.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = tenantsServer.Update(auth.ContextWithSubject(ctx, &auth.Subject{User: "finalizer-controller"}), privatev1.TenantsUpdateRequest_builder{
			Object: deleting.GetObject(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := tenantsServer.Get(c, privatev1.TenantsGetRequest_builder{Id: id}.Build())
			return err
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		marker, found, err := database.GetDeletedTenantIdentity(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(marker.DeletedBy).To(Equal(new("original-deleter")))
		Expect(marker.FinalizedAt).NotTo(BeNil())
		firstAfter, err := readTenantBillingInterval(ctx, first)
		Expect(err).ToNot(HaveOccurred())
		Expect(firstAfter).To(Equal(firstBefore))
		secondAfter, err := readTenantBillingInterval(ctx, second)
		Expect(err).ToNot(HaveOccurred())
		Expect(secondAfter).To(Equal(secondBefore))
		var archived int
		Expect(tx.QueryRow(ctx, `select count(*) from archived_tenants where name = $1`, id).Scan(&archived)).To(Succeed())
		Expect(archived).To(Equal(1))
	})

	It("Rolls back tenant deletion when closing its active billing interval fails", func() {
		created, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "rollback-tenant", Finalizers: []string{finalizers.TenantLifecycle},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		first, current := insertBillingHistory(id)
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		historyBefore, err := readTenantBillingInterval(ctx, first)
		Expect(err).ToNot(HaveOccurred())
		currentBefore, err := readTenantBillingInterval(ctx, current)
		Expect(err).ToNot(HaveOccurred())
		// Inject a database failure after generic.Delete has set the tenant deletion timestamp. The sequence
		// survives the rollback, so it records whether the billing-close trigger actually ran.
		_, err = tx.Exec(ctx, `
			create sequence billing_close_attempts;
			create function fail_billing_close() returns trigger language plpgsql as $$
			begin
				perform nextval('billing_close_attempts');
				raise exception 'injected billing close failure';
			end $$;
			create trigger fail_billing_close before update on tenant_billing_assignments
			for each row execute function fail_billing_close()`)
		Expect(err).ToNot(HaveOccurred())
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)

		_, err = database.WithNewTx(ctx, func(ctx context.Context) (*privatev1.TenantsDeleteResponse, error) {
			return tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
		// The sequence is not transactional: if it advanced, the billing-close trigger really ran even
		// though the deletion rolled back.
		var attempts int
		var called bool
		Expect(testPool.QueryRow(ctx, `select last_value, is_called from billing_close_attempts`).
			Scan(&attempts, &called)).To(Succeed())
		Expect(called).To(BeTrue(), "the billing-close trigger must have run before the rollback")
		Expect(attempts).To(Equal(1))
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			retained, err := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return struct{}{}, err
			}
			Expect(retained.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
			historyAfter, err := readTenantBillingInterval(ctx, first)
			if err != nil {
				return struct{}{}, err
			}
			Expect(historyAfter).To(Equal(historyBefore))
			currentAfter, err := readTenantBillingInterval(ctx, current)
			if err != nil {
				return struct{}{}, err
			}
			Expect(currentAfter).To(Equal(currentBefore), "the interval must remain open when deletion rolls back")
			return struct{}{}, nil
		})
		Expect(err).ToNot(HaveOccurred())
		// Removing only the injected trigger proves it caused the rejection: the same deletion now succeeds
		// and closes the active interval.
		_, err = testPool.Exec(ctx, `drop trigger fail_billing_close on tenant_billing_assignments;
			drop function fail_billing_close(); drop sequence billing_close_attempts`)
		Expect(err).ToNot(HaveOccurred())
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (*privatev1.TenantsDeleteResponse, error) {
			return tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			currentAfter, err := readTenantBillingInterval(ctx, current)
			if err != nil {
				return struct{}{}, err
			}
			Expect(currentAfter.GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil(),
				"the same deletion must close the active interval")
			return struct{}{}, nil
		})
		Expect(err).ToNot(HaveOccurred())
	})

	// expectIntactTenant asserts, inside the current transaction, that the tenant row is still live,
	// that both billing assignment rows survived, and that nothing was archived.
	expectIntactTenant := func(tenant, closed, current string) {
		GinkgoHelper()
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var count int
		err = tx.QueryRow(ctx, "select count(*) from tenants where name = $1", tenant).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
		closedRow, err := readTenantBillingInterval(ctx, closed)
		Expect(err).ToNot(HaveOccurred())
		Expect(closedRow.GetSpec().GetTenant().GetId()).To(Equal(tenant))
		Expect(closedRow.GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		currentRow, err := readTenantBillingInterval(ctx, current)
		Expect(err).ToNot(HaveOccurred())
		Expect(currentRow.GetSpec().GetTenant().GetId()).To(Equal(tenant))
		Expect(currentRow.GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		err = tx.QueryRow(ctx, "select count(*) from archived_tenants where name = $1", tenant).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
	}

	It("Cannot delete a tenant that has projects and billing assignments (default constraint order)", func() {
		// Create a tenant with no finalizers, so deletion immediately attempts the archive. The
		// default project remains, and both a closed and a current billing assignment reference the
		// tenant, so the archive delete violates both foreign keys.
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "blocked-tenant",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		tenant := createResponse.GetObject()

		closed, current := insertBillingHistory("blocked-tenant")

		// The delete must be rejected because of the project, not accepted because of the
		// assignment history:
		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := tenantsServer.Delete(c, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
			return err
		})
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(Equal(
			"tenant 'blocked-tenant' cannot be deleted because it still has projects",
		))

		expectIntactTenant("blocked-tenant", closed, current)
	})

	It("Cannot delete a tenant with both historical and current billing while a project remains", func() {
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "forced-tenant",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		tenant := createResponse.GetObject()

		closed, current := insertBillingHistory("forced-tenant")

		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(c context.Context) error {
			_, err := tenantsServer.Delete(c, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
			return err
		})
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(Equal(
			"tenant 'forced-tenant' cannot be deleted because it still has projects",
		))

		expectIntactTenant("forced-tenant", closed, current)
	})

	It("Cannot archive a soft-deleted tenant through a finalizer-clearing update while it still has "+
		"projects and billing assignments", func() {
		// Create a tenant with finalizers, so Delete only soft-deletes it:
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "finalizer-tenant",
						Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()

		closed, current := insertBillingHistory("finalizer-tenant")
		historyBefore, err := readTenantBillingInterval(ctx, closed)
		Expect(err).ToNot(HaveOccurred())

		// The soft delete succeeds because the finalizers are present:
		_, err = tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		closedOnDelete, err := readTenantBillingInterval(ctx, current)
		Expect(err).ToNot(HaveOccurred())
		Expect(closedOnDelete.GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil(), "do not wait for finalizers before closing billing")
		Expect(closedOnDelete.GetStatus().GetMessage()).To(Equal(database.TenantBillingAssignmentDeletedMessage))

		// Commit the soft-deleted state and run the finalizer-clearing update in its own
		// transaction, so its rejection rolls back completely and can be verified against the
		// committed state below:
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)

		// Clearing the last finalizers triggers the archive attempt, which the project blocker
		// must reject without losing already closed billing history:
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (*privatev1.TenantsUpdateResponse, error) {
			getResponse, getErr := tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: id,
			}.Build())
			if getErr != nil {
				return nil, getErr
			}
			object := proto.Clone(getResponse.GetObject()).(*privatev1.Tenant)
			object.GetMetadata().SetFinalizers(nil)
			return tenantsServer.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
				Lock:       true,
			}.Build())
		})
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(Equal(
			"tenant 'finalizer-tenant' cannot be deleted because it still has projects",
		))

		// From an independent transaction the tenant is still present, still soft-deleted, with
		// its finalizers intact (the clearing update rolled back) and no archived copy:
		read, err := database.WithNewTx(ctx, func(ctx context.Context) (*privatev1.TenantsGetResponse, error) {
			return tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: id,
			}.Build())
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(read.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf(
			finalizers.TenantLifecycle, finalizers.TenantOnboarding,
		))
		Expect(read.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())

		var archived int
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			tx, txErr := database.TxFromContext(ctx)
			if txErr != nil {
				return struct{}{}, txErr
			}
			var count int
			err := tx.QueryRow(ctx,
				"select count(*) from archived_tenants where name = 'finalizer-tenant'").Scan(&count)
			if err != nil {
				return struct{}{}, err
			}
			archived = count
			closedRow, err := readTenantBillingInterval(ctx, closed)
			if err != nil {
				return struct{}{}, err
			}
			Expect(closedRow.GetSpec().GetTenant().GetId()).To(Equal("finalizer-tenant"))
			Expect(closedRow).To(Equal(historyBefore))
			currentRow, err := readTenantBillingInterval(ctx, current)
			if err != nil {
				return struct{}{}, err
			}
			Expect(currentRow.GetSpec().GetTenant().GetId()).To(Equal("finalizer-tenant"))
			Expect(currentRow).To(Equal(closedOnDelete), "a failed finalizer update cannot reopen billing")
			return struct{}{}, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(archived).To(BeZero())
	})

	It("Leaves no committed damage when a project-blocked billed delete is rejected", func() {
		createResponse, err := tenantsServer.Create(
			ctx,
			privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "committed-tenant",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		tenant := createResponse.GetObject()

		closed, current := insertBillingHistory("committed-tenant")
		closedBefore, err := readTenantBillingInterval(ctx, closed)
		Expect(err).ToNot(HaveOccurred())
		currentBefore, err := readTenantBillingInterval(ctx, current)
		Expect(err).ToNot(HaveOccurred())

		// Commit the setup and release the suite transaction, following the pattern of the tenant
		// billing assignments specs:
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)

		_, err = database.WithNewTx(ctx, func(ctx context.Context) (*privatev1.TenantsDeleteResponse, error) {
			return tenantsServer.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenant.GetId(),
			}.Build())
		})
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(Equal(
			"tenant 'committed-tenant' cannot be deleted because it still has projects",
		))

		// From an independent transaction the tenant is still there without a deletion timestamp
		// (the rejected delete left no trace), both assignments remain, and nothing was archived:
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			tx, txErr := database.TxFromContext(ctx)
			if txErr != nil {
				return struct{}{}, txErr
			}
			var deletedAt time.Time
			err := tx.QueryRow(ctx,
				"select deletion_timestamp from tenants where name = 'committed-tenant'").Scan(&deletedAt)
			if err != nil {
				return struct{}{}, err
			}
			Expect(deletedAt.After(time.Unix(1, 0))).To(BeFalse(),
				"the tenant must not carry a deletion timestamp after the rejected delete")
			closedRow, err := readTenantBillingInterval(ctx, closed)
			if err != nil {
				return struct{}{}, err
			}
			Expect(closedRow).To(Equal(closedBefore), "closed history must survive unchanged")
			currentRow, err := readTenantBillingInterval(ctx, current)
			if err != nil {
				return struct{}{}, err
			}
			Expect(currentRow).To(Equal(currentBefore),
				"the rejected delete must leave the active interval fully unchanged")
			var count int
			err = tx.QueryRow(ctx,
				"select count(*) from archived_tenants where name = 'committed-tenant'").Scan(&count)
			if err != nil {
				return struct{}{}, err
			}
			Expect(count).To(BeZero())
			return struct{}{}, nil
		})
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

	It("Returns generated break-glass credentials on Create", func() {
		response, err := tenantsServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "secret-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		creds := response.GetObject().GetStatus().GetBreakGlassCredentials()
		Expect(creds).ToNot(BeNil())
		Expect(creds.GetUsername()).To(Equal("secret-tenant-osac-break-glass"))
		Expect(creds.GetPassword()).ToNot(BeEmpty())
		Expect(response.GetObject().GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
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

// These fixtures seed canonical objects directly, not the assignment RPC or a legacy SQL writer.
func seedTenantBillingAccount(ctx context.Context) string {
	GinkgoHelper()
	accounts, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	Expect(err).NotTo(HaveOccurred())
	id := uuid.New()
	name, err := billingAccountName(id)
	Expect(err).NotTo(HaveOccurred())
	_, err = accounts.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
		Spec:     privatev1.BillingAccountSpec_builder{}.Build(),
		Status: privatev1.BillingAccountStatus_builder{ProviderKey: "m360", ProviderAccountReference: uuid.New(),
			State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
			StateObservationTimestamp: timestamppb.Now(),
		}.Build(),
	}.Build()).Do(ctx)
	Expect(err).NotTo(HaveOccurred())
	return id
}

func seedTenantBillingInterval(ctx context.Context, tenant, account, name string, closed bool) string {
	GinkgoHelper()
	assignments, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	Expect(err).NotTo(HaveOccurred())
	accountName, err := billingAccountName(account)
	Expect(err).NotTo(HaveOccurred())
	from := timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	state := privatev1.TenantBillingAssignmentStatus_builder{
		ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
		EffectiveFromTimestamp: from,
	}.Build()
	if closed {
		state.SetEffectiveToTimestamp(from)
	}
	id := uuid.New()
	_, err = assignments.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant, Creator: "fixture",
			Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
		}.Build(),
		Spec: privatev1.TenantBillingAssignmentSpec_builder{
			Tenant:         privatev1.TenantReference_builder{Id: tenant, Name: tenant}.Build(),
			BillingAccount: privatev1.BillingAccountReference_builder{Id: account, Name: accountName}.Build(),
		}.Build(), Status: state,
	}.Build()).Do(ctx)
	Expect(err).NotTo(HaveOccurred())
	return id
}

func readTenantBillingInterval(ctx context.Context, id string) (*privatev1.TenantBillingAssignment, error) {
	assignments, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	if err != nil {
		return nil, err
	}
	response, err := assignments.Get().SetId(id).Do(ctx)
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}
