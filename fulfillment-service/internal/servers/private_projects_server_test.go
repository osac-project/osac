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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private projects server", func() {
	var privateServer *PrivateProjectsServer

	BeforeEach(func() {
		var err error

		// Create the tenants used in the tests:
		tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTableName("tenants").
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createTenant := func(name string) {
			_, err = tenantsDao.Create().
				SetObject(privatev1.Tenant_builder{
					Id: name,
					Metadata: privatev1.Metadata_builder{
						Name:   name,
						Tenant: name,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		createTenant("my-tenant")
		createTenant("your-tenant")

		// Create server (without notifier for testing):
		privateServer, err = NewPrivateProjectsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates a project", func() {
		// Create request:
		request := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title:       "My Project",
					Description: new("Test project"),
				}.Build(),
			}.Build(),
		}.Build()

		// Create project:
		response, err := privateServer.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object).ToNot(BeNil())
		Expect(response.Object.Id).ToNot(BeEmpty())
		Expect(response.Object.Metadata.Name).To(Equal("my-project"))
		Expect(response.Object.Spec.Title).To(Equal("My Project"))
		Expect(response.Object.Spec.Description).To(HaveValue(Equal("Test project")))
	})

	It("Lists projects", func() {
		// Create a project first:
		createReq := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build()
		_, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// List projects:
		listResp, err := privateServer.List(ctx, &privatev1.ProjectsListRequest{
			Filter: new("this.metadata.name == 'my-project'"),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Size).To(Equal(int32(1)))
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Metadata.Name).To(Equal("my-project"))
	})

	It("Lists projects by tenant", func() {
		// Create projects in different tenants:
		createProject := func(tenant string) {
			_, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
				Object: privatev1.Project_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("my-project-in-%s", tenant),
						Tenant: tenant,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		createProject("my-tenant")
		createProject("your-tenant")

		// List projects for one of the tenants, excluding the default project:
		listResponse, err := privateServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.name != ''"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetSize()).To(BeNumerically("==", 1))
		items := listResponse.GetItems()
		Expect(items).To(HaveLen(1))
		item := items[0]
		Expect(item.GetMetadata().GetTenant()).To(Equal("my-tenant"))
	})

	It("Lists top-level projects (no parent)", func() {
		// Create parent and child:
		_, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "parent",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Parent",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "parent.child",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "Child",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// List only top-level projects (metadata.project is empty), excluding the default project:
		listResp, err := privateServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.project == '' && this.metadata.name != ''"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.GetSize()).To(Equal(int32(1)))
		Expect(listResp.GetItems()[0].GetMetadata().GetName()).To(Equal("parent"))
	})

	It("Gets a project by ID", func() {
		// Create a project:
		createReq := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Get the project:
		getResp, err := privateServer.Get(ctx, privatev1.ProjectsGetRequest_builder{
			Id: createResp.Object.Id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Id).To(Equal(createResp.Object.Id))
		Expect(getResp.Object.Metadata.Name).To(Equal("my-project"))
	})

	It("Deletes a project", func() {
		// Create a project:
		createReq := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Delete the project:
		_, err = privateServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: createResp.Object.Id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("Can delete the default project", func() {
		// Find the default project:
		listResponse, err := privateServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.name == ''"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projects := listResponse.GetItems()
		Expect(projects).To(HaveLen(1))
		project := projects[0]

		// Delete it:
		_, err = privateServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: project.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("Can re-create the default project after it was deleted", func() {
		// Find the default project:
		listResponse, err := privateServer.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new("this.metadata.tenant == 'my-tenant' && this.metadata.name == ''"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		projects := listResponse.GetItems()
		Expect(projects).To(HaveLen(1))
		project := projects[0]

		// Delete it:
		_, err = privateServer.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: project.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Re-create it:
		createResponse, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "my-tenant",
					Name:   "",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		project = createResponse.GetObject()

		// Verify that the the project is its own parent:
		Expect(project.GetMetadata().GetProject()).To(BeEmpty())
	})

	It("Rejects a project that references itself as the parent", func() {
		_, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "my-project",
					Tenant:  "my-tenant",
					Project: "my-project",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("metadata.project"))
	})

	It("Updates a project", func() {
		// Create a project:
		createReq := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Update the project status:
		updateReq := privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: createResp.Object.Id,
				Status: privatev1.ProjectStatus_builder{
					State: privatev1.ProjectState_PROJECT_STATE_ACTIVE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{
					"status.state",
				},
			},
		}.Build()
		updateResp, err := privateServer.Update(ctx, updateReq)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Status.State).To(Equal(privatev1.ProjectState_PROJECT_STATE_ACTIVE))
	})

	It("Updates project description", func() {
		// Create a project:
		createReq := privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
				Spec: privatev1.ProjectSpec_builder{
					Title: "My Project",
				}.Build(),
			}.Build(),
		}.Build()
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Update description:
		updateReq := privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: createResp.Object.Id,
				Spec: privatev1.ProjectSpec_builder{
					Description: new("Updated description"),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{
					"spec.description",
				},
			},
		}.Build()
		updateResp, err := privateServer.Update(ctx, updateReq)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Spec.Description).To(HaveValue(Equal("Updated description")))
	})

	It("Rejects update of the name of a project", func() {
		createResponse, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		id := object.GetId()
		updateResponse, err := privateServer.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Name: "your-project",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"metadata.name"},
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

	It("Rejects update of the tenant of a project", func() {
		createResponse, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		id := object.GetId()
		updateResponse, err := privateServer.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Tenant: "your-tenant",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"metadata.tenant"},
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

	It("Silently ignores update of the creator of a project", func() {
		createResponse, err := privateServer.Create(ctx, privatev1.ProjectsCreateRequest_builder{
			Object: privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "my-project",
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		id := object.GetId()
		originalCreator := object.GetMetadata().GetCreator()

		// Attempt to update creator - should be silently ignored
		updateResponse, err := privateServer.Update(ctx, privatev1.ProjectsUpdateRequest_builder{
			Object: privatev1.Project_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Creator: "attacker-user",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"metadata.creator"},
			},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse).ToNot(BeNil())
		Expect(updateResponse.Object.GetMetadata().GetCreator()).To(Equal(originalCreator))
	})
})
