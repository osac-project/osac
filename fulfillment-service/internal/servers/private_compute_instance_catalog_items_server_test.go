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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private compute instance catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateComputeInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateComputeInstanceCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateComputeInstanceCatalogItemsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateComputeInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *PrivateComputeInstanceCatalogItemsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateComputeInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:       "My CI catalog item",
					Description: "My description.",
					Template:    "my-ci-template-id",
					Published:   true,
					Tenant:      "my-tenant",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My CI catalog item"))
			Expect(object.GetTemplate()).To(Equal("my-ci-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetTenant()).To(Equal("my-tenant"))
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: "my-ci-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ComputeInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: "my-ci-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ComputeInstanceCatalogItemsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with filter", func() {
			const count = 10
			var objects []*privatev1.ComputeInstanceCatalogItem
			for i := range count {
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: "my-ci-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, createResponse.GetObject())
			}
			DeferCleanup(func() {
				for _, object := range objects {
					_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
						Id: object.GetId(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			for _, object := range objects {
				getResponse, err := server.List(ctx, privatev1.ComputeInstanceCatalogItemsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetSize()).To(BeNumerically("==", 1))
				Expect(getResponse.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:       "My CI catalog item",
					Description: "My description.",
					Template:    "my-ci-template-id",
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:       "Original title",
					Description: "Original description.",
					Template:    "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Updated description."))

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Updated description."))
		})

		It("Update published using field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:     "My CI catalog item",
					Template:  "my-ci-template-id",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id:        object.GetId(),
					Published: true,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"published"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetPublished()).To(BeTrue())

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetPublished()).To(BeTrue())
		})

		It("Creates object with field definitions and round-trips them", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "CI catalog item with fields",
					Template: "my-ci-template-id",
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.ssh_public_key",
							DisplayName:      "SSH Key",
							Editable:         true,
							ValidationSchema: `{"type":"string","minLength":1}`,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:        "spec.run_strategy",
							DisplayName: "Run Strategy",
							Editable:    false,
							Default:     structpb.NewNumberValue(16),
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetFieldDefinitions()).To(HaveLen(2))

			fd0 := fetched.GetFieldDefinitions()[0]
			Expect(fd0.GetPath()).To(Equal("spec.ssh_public_key"))
			Expect(fd0.GetDisplayName()).To(Equal("SSH Key"))
			Expect(fd0.GetEditable()).To(BeTrue())
			Expect(fd0.GetValidationSchema()).To(Equal(`{"type":"string","minLength":1}`))

			fd1 := fetched.GetFieldDefinitions()[1]
			Expect(fd1.GetPath()).To(Equal("spec.run_strategy"))
			Expect(fd1.GetDisplayName()).To(Equal("Run Strategy"))
			Expect(fd1.GetEditable()).To(BeFalse())
			Expect(fd1.GetDefault()).ToNot(BeNil())
			Expect(fd1.GetDefault().GetNumberValue()).To(Equal(16.0))
		})

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"a"},
					}.Build(),
					Title:    "My CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Blocks delete when referenced by a compute instance", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:     "Referenced CI catalog item",
					Template:  "my-ci-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItem := createResponse.GetObject()

			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = ciDao.Create().SetObject(
				privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "ref-ci",
						Tenant: "system",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						CatalogItem: catalogItem.GetId(),
						Template:    "my-ci-template-id",
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(status.Message()).To(ContainSubstring("in use"))
		})

		It("Rejects duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "First CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "Second CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(ContainSubstring("dev-sandbox"))
		})

		It("Allows same name across different tenants", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: "system",
					}.Build(),
					Title:    "CI catalog item for system tenant",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: "shared",
					}.Build(),
					Title:    "CI catalog item for shared tenant",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update to duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "First CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			secondResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "second-item",
					}.Build(),
					Title:    "Second CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id: secondResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "Second CI catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(ContainSubstring("first-item"))
		})

		It("Rejects non-editable field definition without default value", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Bad CI catalog item",
					Template: "my-ci-template-id",
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
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Good CI catalog item",
					Template: "my-ci-template-id",
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
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Accepts editable field definition without default value", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Editable no default",
					Template: "my-ci-template-id",
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
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects non-editable field definition without default when not first in list", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Bad catalog item",
					Template: "my-ci-template-id",
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.cores",
							Editable: true,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "spec.memory_gib",
							Editable: false,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("memory_gib"))
		})

		It("Rejects field definition with invalid validation_schema JSON", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Bad schema catalog item",
					Template: "my-ci-template-id",
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.cores",
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
			Expect(status.Message()).To(ContainSubstring("cores"))
			Expect(status.Message()).To(ContainSubstring("invalid validation_schema"))
		})

		It("Accepts field definition with valid validation_schema JSON", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Valid schema catalog item",
					Template: "my-ci-template-id",
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.cores",
							Editable:         true,
							ValidationSchema: `{"type":"number","minimum":1}`,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects update that introduces non-editable field without default", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Valid catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.cores",
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
			Expect(status.Message()).To(ContainSubstring("cores"))
			Expect(status.Message()).To(ContainSubstring("default value"))
		})

		It("Rejects update that introduces invalid validation_schema", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Valid catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "spec.cores",
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
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Valid catalog item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id: id,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "spec.cores",
							Editable: false,
							Default:  structpb.NewNumberValue(4),
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:             "spec.memory_gib",
							Editable:         true,
							ValidationSchema: `{"type":"number"}`,
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

		It("Allows empty name without conflict", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "First unnamed CI item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Title:    "Second unnamed CI item",
					Template: "my-ci-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		Describe("Instance type validation in field_definitions", func() {
			var itServer *PrivateInstanceTypesServer

			// createInstanceTypeWithState creates an instance type and transitions it to the
			// given state. For ACTIVE, no transition is needed. For DEPRECATED or OBSOLETE,
			// the type is first created as ACTIVE and then updated.
			createInstanceTypeWithState := func(name string, state privatev1.InstanceTypeState) {
				_, err := itServer.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				if state == privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE ||
					state == privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_UNSPECIFIED {
					return
				}

				_, err = itServer.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: name,
						Spec: privatev1.InstanceTypeSpec_builder{
							State: state,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.state"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			BeforeEach(func() {
				var err error
				itServer, err = NewPrivateInstanceTypesServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Returns warning when field_definitions default references a DEPRECATED instance type on Create", func() {
				createInstanceTypeWithState("deprecated-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item with deprecated default",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("deprecated-type"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Returns warning when field_definitions default references a DEPRECATED instance type on Update", func() {
				// Create a catalog item without field_definitions first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item to update",
						Template: "my-ci-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				catalogItemId := createResponse.GetObject().GetId()

				createInstanceTypeWithState("deprecated-type-upd",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Id:       catalogItemId,
						Title:    "Catalog item to update",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("deprecated-type-upd"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetWarnings()).To(HaveLen(1))
			})

			It("Rejects Create when field_definitions default references an OBSOLETE instance type", func() {
				createInstanceTypeWithState("obsolete-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item with obsolete default",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("obsolete-type"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Rejects Update when field_definitions default references an OBSOLETE instance type", func() {
				// Create a catalog item first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item for obsolete update",
						Template: "my-ci-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				catalogItemId := createResponse.GetObject().GetId()

				createInstanceTypeWithState("obsolete-type-upd",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Id:       catalogItemId,
						Title:    "Catalog item for obsolete update",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("obsolete-type-upd"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Returns no warnings when field_definitions default references an ACTIVE instance type", func() {
				createInstanceTypeWithState("active-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item with active default",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("active-type"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Rejects Create when field_definitions default references a non-existent instance type", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item with missing type",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.instance_type",
								Editable: true,
								Default:  structpb.NewStringValue("non-existent-type"),
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})

			It("Skips validation when field_definitions has no spec.instance_type path", func() {
				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Title:    "Catalog item without instance type field",
						Template: "my-ci-template-id",
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "spec.ssh_public_key",
								Editable: true,
							}.Build(),
						},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

		})
	})
})
