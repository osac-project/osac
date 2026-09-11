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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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

		// Create server (without notifier for testing):
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

	Context("with default networking provisioner", func() {
		var (
			provisionerServer *PrivateTenantsServer
			provisioner       *DefaultNetworkingProvisioner
		)

		BeforeEach(func() {
			var err error
			provisioner, err = NewDefaultNetworkingProvisioner().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			provisionerServer, err = NewPrivateTenantsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetDefaultNetworkingProvisioner(provisioner).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates default networking resources when NetworkClass defaults exist", func() {
			ncDao := provisioner.networkClassDao
			nc := privatev1.NetworkClass_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "test-nc",
					Tenant: "system",
				}.Build(),
				IsDefault:     new(true),
				FabricManager: new("netris"),
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.0.0.0/16",
						SubnetIpv4Cidr:         "10.0.1.0/24",
					}.Build(),
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build()
			_, err := ncDao.Create().SetObject(nc).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			request := privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "net-tenant",
					}.Build(),
				}.Build(),
			}.Build()

			response, err := provisionerServer.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'net-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(HaveLen(1))
			Expect(vnList.GetItems()[0].GetMetadata().GetLabels()).To(
				HaveKeyWithValue("osac.openshift.io/default", "true"))
		})

		It("creates tenant without default networking when no NetworkClass exists", func() {
			request := privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "plain-tenant",
					}.Build(),
				}.Build(),
			}.Build()

			response, err := provisionerServer.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'plain-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(BeEmpty())
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
