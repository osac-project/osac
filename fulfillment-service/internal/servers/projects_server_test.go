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
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Public projects server", func() {
	var publicServer *ProjectsServer

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

		// Create public server:
		publicServer, err = NewProjectsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			srv, err := NewProjectsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(srv).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			srv, err := NewProjectsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(srv).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			srv, err := NewProjectsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(srv).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Lists projects", func() {
			// Create the project:
			_, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "my-project",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "My Project",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List projects:
			listResponse, err := publicServer.List(ctx, publicv1.ProjectsListRequest_builder{
				Filter: new("this.metadata.name == 'my-project'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.Size).To(Equal(int32(1)))
			Expect(listResponse.Items).To(HaveLen(1))
			Expect(listResponse.Items[0].Metadata.Name).To(Equal("my-project"))
		})

		It("Gets a project by identifier", func() {
			// Create the project:
			createResponse, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "my-project",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "My Project",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			id := object.GetId()

			// Get the project:
			getResponse, err := publicServer.Get(ctx, publicv1.ProjectsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.Object.Id).To(Equal(id))
			Expect(getResponse.Object.Metadata.Name).To(Equal("my-project"))
		})

		It("Creates a project", func() {
			response, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "new-project",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title:       "New Project",
						Description: new("Test project"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.Object).ToNot(BeNil())
			Expect(response.Object.Id).ToNot(BeEmpty())
			Expect(response.Object.Metadata.Name).To(Equal("new-project"))
			Expect(response.Object.Spec.Title).To(Equal("New Project"))
			Expect(response.Object.Spec.Description).To(HaveValue(Equal("Test project")))
		})

		It("Updates a project", func() {
			// Create the project:
			createResponse, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "my-project",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "My Project",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Update the project:
			updateResponse, err := publicServer.Update(ctx, publicv1.ProjectsUpdateRequest_builder{
				Object: publicv1.Project_builder{
					Id: createResponse.Object.Id,
					Spec: publicv1.ProjectSpec_builder{
						Description: new("Updated description"),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.description",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.Object.Spec.Description).To(HaveValue(Equal("Updated description")))
		})

		It("Deletes a project", func() {
			// Create the project:
			createResponse, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "my-project",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "My Project",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Delete the project:
			_, err = publicServer.Delete(ctx, publicv1.ProjectsDeleteRequest_builder{
				Id: createResponse.Object.Id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Filters projects by parent", func() {
			// Create parent:
			_, err := publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "parent",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "Parent",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Create child:
			_, err = publicServer.Create(ctx, publicv1.ProjectsCreateRequest_builder{
				Object: publicv1.Project_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "parent.child",
						Tenant: "my-tenant",
					}.Build(),
					Spec: publicv1.ProjectSpec_builder{
						Title: "Child",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List child projects by parent name:
			listResp, err := publicServer.List(ctx, publicv1.ProjectsListRequest_builder{
				Filter: new("this.metadata.project == 'parent'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.GetSize()).To(Equal(int32(1)))
			Expect(listResp.GetItems()[0].GetMetadata().GetName()).To(Equal("parent.child"))
		})
	})
})
