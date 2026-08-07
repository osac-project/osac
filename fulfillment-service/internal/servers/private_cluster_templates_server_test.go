/*
Copyright (c) 2025 Red Hat Inc.

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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Private cluster templates server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateClusterTemplatesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateClusterTemplatesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateClusterTemplatesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("my-template-%d", i),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClusterTemplatesListRequest_builder{
				Filter: new("this.metadata.name.startsWith('my-template-')"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("my-template-%d", i),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClusterTemplatesListRequest_builder{
				Filter: new("this.metadata.name.startsWith('my-template-')"),
				Limit:  new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.ClusterTemplate
			for i := range count {
				createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("my-template-%d", i),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, createResponse.GetObject())
			}
			DeferCleanup(func() {
				for _, object := range objects {
					_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
						Id: object.GetId(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			// List the objects:
			response, err := server.List(ctx, privatev1.ClusterTemplatesListRequest_builder{
				Filter: new("this.metadata.name.startsWith('my-template-')"),
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.ClusterTemplate
			for i := range count {
				createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("my-filter-template-%d", i),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, createResponse.GetObject())
			}
			DeferCleanup(func() {
				for _, object := range objects {
					_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
						Id: object.GetId(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			// List the objects:
			for _, object := range objects {
				getResponse, err := server.List(ctx, privatev1.ClusterTemplatesListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetSize()).To(BeNumerically("==", 1))
				Expect(getResponse.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-get",
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-update",
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Id: object.GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-update",
					}.Build(),
					Title:       "Your title",
					Description: "Your description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Your description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Your description."))
		})

		It("Update title ony, using field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-title",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Updated title"))
			Expect(object.GetDescription()).To(Equal("Original description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Updated title"))
			Expect(object.GetDescription()).To(Equal("Original description."))
		})

		It("Update description ony, using field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-desc",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"description",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Original title"))
			Expect(object.GetDescription()).To(Equal("Updated description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Original title"))
			Expect(object.GetDescription()).To(Equal("Updated description."))
		})

		It("Update title and description using field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-template-both",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"description",
						"title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Updated title"))
			Expect(object.GetDescription()).To(Equal("Updated description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetTitle()).To(Equal("Updated title"))
			Expect(object.GetDescription()).To(Equal("Updated description."))
		})

		Describe("ClusterVersion validation on spec_defaults", func() {
			var validatedServer *PrivateClusterTemplatesServer

			BeforeEach(func() {
				var err error
				validatedServer, err = NewPrivateClusterTemplatesServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Rejects create with non-existent spec_defaults.version", func() {
				_, err := validatedServer.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Title:       "Bad version template",
						Description: "Template referencing a non-existent version.",
						SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
							Version: &privatev1.ClusterVersionReference{Name: "does-not-exist"},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' not found"))
			})

			It("Rejects update with disabled spec_defaults.version", func() {
				// Seed a disabled ClusterVersion:
				cvDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = cvDao.Create().
					SetObject(privatev1.ClusterVersion_builder{
						Id: uuid.New(),
						Metadata: privatev1.Metadata_builder{
							Name:   "4-18-0-disabled",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ClusterVersionSpec_builder{
							Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
							Enabled: proto.Bool(false),
							Version: "4.18.0",
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a template first:
				createResponse, err := validatedServer.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Title:       "My template",
						Description: "Template to update.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				// Update with a disabled version in spec_defaults:
				_, err = validatedServer.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
					Object: privatev1.ClusterTemplate_builder{
						Id:          object.GetId(),
						Title:       object.GetTitle(),
						Description: object.GetDescription(),
						SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
							Version: &privatev1.ClusterVersionReference{Name: "4-18-0-disabled"},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec_defaults"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is disabled"))
			})
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
				Object: privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-cluster-template-delete",
						Finalizers: []string{"a"},
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})
})
