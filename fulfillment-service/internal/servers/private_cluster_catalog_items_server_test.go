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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Private cluster catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *PrivateClusterCatalogItemsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:       "My cluster catalog item",
					Description: "My description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
					Tenant:      "my-tenant",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My cluster catalog item"))
			Expect(object.GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetTenant()).To(Equal("my-tenant"))
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:       fmt.Sprintf("Catalog item %d", i),
						Description: fmt.Sprintf("Description %d.", i),
						Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with filter", func() {
			const count = 10
			var objects []*privatev1.ClusterCatalogItem
			for i := range count {
				createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, createResponse.GetObject())
			}
			DeferCleanup(func() {
				for _, object := range objects {
					_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
						Id: object.GetId(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			for _, object := range objects {
				getResponse, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetSize()).To(BeNumerically("==", 1))
				Expect(getResponse.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:       "My catalog item",
					Description: "My description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-update",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Updated description."))

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Updated description."))
		})

		It("Update published using field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-published",
					}.Build(),
					Title:     "My catalog item",
					Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:        object.GetId(),
					Published: true,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"published"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetPublished()).To(BeTrue())

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetPublished()).To(BeTrue())
		})

		It("Creates object with field definitions and round-trips them", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Catalog item with fields",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.network.pod_cidr",
							DisplayName:      "Pod CIDR",
							Editable:         true,
							ValidationSchema: `{"type":"string","pattern":"^[0-9./]+$"}`,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:        "spec.node_sets.workers.size",
							DisplayName: "Worker count",
							Editable:    false,
							Default:     structpb.NewNumberValue(3),
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetFieldDefinitions()).To(HaveLen(2))

			fd0 := fetched.GetFieldDefinitions()[0]
			Expect(fd0.GetPath()).To(Equal("spec.network.pod_cidr"))
			Expect(fd0.GetDisplayName()).To(Equal("Pod CIDR"))
			Expect(fd0.GetEditable()).To(BeTrue())
			Expect(fd0.GetValidationSchema()).To(Equal(`{"type":"string","pattern":"^[0-9./]+$"}`))

			fd1 := fetched.GetFieldDefinitions()[1]
			Expect(fd1.GetPath()).To(Equal("spec.node_sets.workers.size"))
			Expect(fd1.GetDisplayName()).To(Equal("Worker count"))
			Expect(fd1.GetEditable()).To(BeFalse())
			Expect(fd1.GetDefault()).ToNot(BeNil())
			Expect(fd1.GetDefault().GetNumberValue()).To(Equal(3.0))
		})

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-cluster-catalog-delete",
						Finalizers: []string{"a"},
					}.Build(),
					Title:    "My catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Blocks delete when referenced by a cluster", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:     "Referenced catalog item",
					Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItem := createResponse.GetObject()

			clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = clustersDao.Create().SetObject(
				privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "ref-cluster",
						Tenant: "system",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
						Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(status.Message()).To(ContainSubstring("in use"))
		})

		It("Rejects duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "First catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(ContainSubstring("dev-sandbox"))
		})

		It("Allows same name across different tenants", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: "system",
					}.Build(),
					Title:    "Catalog item for system tenant",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: "shared",
					}.Build(),
					Title:    "Catalog item for shared tenant",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update to duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "First catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			secondResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "second-item",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id: secondResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(ContainSubstring("first-item"))
		})

		It("Rejects non-editable field definition without default value", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Bad catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: false,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("pull_secret"))
			Expect(status.Message()).To(ContainSubstring("default value"))
		})

		It("Accepts non-editable field definition with default value", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Good catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: false,
							Default:  structpb.NewStringValue("my-secret"),
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Accepts editable field definition without default value", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Editable no default",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects non-editable field definition without default when not first in list", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Bad catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: true,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "spec.ssh_public_key",
							Editable: false,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("ssh_public_key"))
		})

		It("Rejects field definition with invalid validation_schema JSON", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Bad schema catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.network.pod_cidr",
							Editable:         true,
							ValidationSchema: "{not valid json}",
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("pod_cidr"))
			Expect(status.Message()).To(ContainSubstring("invalid validation_schema"))
		})

		It("Accepts field definition with valid validation_schema JSON", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Valid schema catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.network.pod_cidr",
							Editable:         true,
							ValidationSchema: `{"type":"string","pattern":"^[0-9./]+$"}`,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects update that introduces non-editable field without default", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-nodefault",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: false,
						}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"field_definitions"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("pull_secret"))
			Expect(status.Message()).To(ContainSubstring("default value"))
		})

		It("Rejects update that introduces invalid validation_schema", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-badschema",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.network.pod_cidr",
							Editable:         true,
							ValidationSchema: "{bad json}",
						}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"field_definitions"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("invalid validation_schema"))
		})

		It("Accepts update with valid field definitions", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-validfd",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.pull_secret",
							Editable: false,
							Default:  structpb.NewStringValue("locked-secret"),
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:             "spec.network.pod_cidr",
							Editable:         true,
							ValidationSchema: `{"type":"string"}`,
						}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"field_definitions"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetFieldDefinitions()).To(HaveLen(2))
		})

		Describe("ClusterVersion validation on field_definitions", func() {
			var validatedServer *PrivateClusterCatalogItemsServer

			BeforeEach(func() {
				var err error
				validatedServer, err = NewPrivateClusterCatalogItemsServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Rejects create with non-existent version default in field_definitions", func() {
				_, err := validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:    "Bad version catalog item",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "version",
								Editable: true,
								Default:  structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("does-not-exist")}}),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' not found"))
			})

			It("Rejects create with obsolete version default in field_definitions", func() {
				// Seed an obsolete ClusterVersion:
				cvDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = cvDao.Create().
					SetObject(privatev1.ClusterVersion_builder{
						Id: uuid.New(),
						Metadata: privatev1.Metadata_builder{
							Name:   "4-16-0-obsolete",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ClusterVersionSpec_builder{
							Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
							Enabled: proto.Bool(true),
							Version: "4.16.0",
							State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:    "Obsolete version catalog item",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "version",
								Editable: true,
								Default:  structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("4-16-0-obsolete")}}),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})

			It("Rejects update with non-existent version default in field_definitions", func() {
				createResponse, err := validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Title:    "Catalog item for update test",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Id:       createResponse.GetObject().GetId(),
						Title:    "Catalog item for update test",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "version",
								Editable: true,
								Default:  structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("does-not-exist")}}),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' not found"))
			})
		})

		It("Allows empty name without conflict", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "First unnamed item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Title:    "Second unnamed item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
