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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private bare metal instance catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateBareMetalInstanceCatalogItemsServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:       "My catalog item",
					Description: "A test catalog item.",
					Template:    "my-template-id",
					Published:   true,
					Tenant:      "my-tenant",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My catalog item"))
			Expect(object.GetTemplate()).To(Equal("my-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetTenant()).To(Equal("my-tenant"))
		})

		It("Lists objects", func() {
			const count = 5
			for i := range count {
				_, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.BareMetalInstanceCatalogItem_builder{
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: "my-template-id",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.BareMetalInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Updates object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Original title",
					Template: "my-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Id:       object.GetId(),
					Title:    "Updated title",
					Template: "my-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
		})

		It("Deletes object without references", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"keep"},
					}.Build(),
					Title:    "My catalog item",
					Template: "my-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Blocks delete when referenced by a bare metal instance", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Referenced catalog item",
					Template:  "my-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItem := createResponse.GetObject()

			bmiDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = bmiDao.Create().SetObject(
				privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "my-bmi",
						Tenant: "system",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItem.GetId(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(status.Message()).To(ContainSubstring("still referenced"))
		})

		It("Creates object with field definitions", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Catalog item with fields",
					Template: "my-template-id",
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:        "spec.run_strategy",
							DisplayName: "Run strategy",
							Editable:    true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetFieldDefinitions()).To(HaveLen(1))
			Expect(response.GetObject().GetFieldDefinitions()[0].GetPath()).To(Equal("spec.run_strategy"))
		})

		It("Rejects non-editable field definition without default value", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Bad catalog item",
					Template: "my-template-id",
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
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Good catalog item",
					Template: "my-template-id",
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
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Accepts editable field definition without default value", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Editable no default",
					Template: "my-template-id",
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
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects non-editable field definition without default when not first in list", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Bad catalog item",
					Template: "my-template-id",
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
			_, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Bad schema catalog item",
					Template: "my-template-id",
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
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Valid schema catalog item",
					Template: "my-template-id",
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
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects update that introduces non-editable field without default", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Valid catalog item",
					Template: "my-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
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
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:    "Valid catalog item",
					Template: "my-template-id",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
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
	})
})
