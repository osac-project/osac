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
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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

	Describe("Dependency locking", func() {
		It("holds the Template lock until authoring commits, then prevents deletion", func() {
			// Separate committed setup lets a second transaction observe the dependency lock.
			db, err := server.NewInstance().Build()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(db.Close)
			pool, err := db.Pool(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(pool.Close)
			transactions, err := database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
			Expect(err).NotTo(HaveOccurred())
			background := context.Background()
			err = transactions.Run(background, func(setup context.Context) error {
				tx, err := database.TxFromContext(setup)
				if err != nil {
					return err
				}
				_, err = tx.Exec(setup, "insert into tenants (id, name, tenant, data) values ($1, $1, $1, '{}')", testTenant)
				if err != nil {
					return err
				}
				return seedComputeCatalogItemTemplate(setup, testTenant, "", "locked-template")
			})
			Expect(err).NotTo(HaveOccurred())
			items, err := NewPrivateComputeInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			err = transactions.Run(background, func(authoring context.Context) {
				_, err := items.Create(authoring, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{Name: "locked-item"}.Build(),
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "locked-template"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				var unlocked int
				err = pool.QueryRow(background, `select count(*) from
	    (select id from compute_instance_templates where id = 'locked-template' for update skip locked) available`).Scan(&unlocked)
				Expect(err).NotTo(HaveOccurred())
				Expect(unlocked).To(BeZero(), "the dependency must remain locked until authoring commits")
			})
			Expect(err).NotTo(HaveOccurred())
			// Direct provisioning can read the same Template without holding an exclusive lock.
			instances, err := NewPrivateComputeInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			err = transactions.Run(background, func(creation context.Context) {
				template, err := instances.resolveCreationSource(creation, privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "locked-template"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(template.GetId()).To(Equal("locked-template"))
				var unlocked int
				err = pool.QueryRow(background, `select count(*) from
	    (select id from compute_instance_templates where id = 'locked-template' for update skip locked) available`).Scan(&unlocked)
				Expect(err).NotTo(HaveOccurred())
				Expect(unlocked).To(Equal(1), "direct Template reads must not serialize provisioning requests")
			})
			Expect(err).NotTo(HaveOccurred())
			templates, err := NewPrivateComputeInstanceTemplatesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			err = transactions.Run(background, func(deletion context.Context) error {
				_, err := templates.Delete(deletion, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: "locked-template"}.Build())
				return err
			})
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
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
			Expect(seedComputeCatalogItemTemplate(ctx, testTenant, "", "my-ci-template-id")).To(Succeed())
			Expect(seedComputeCatalogItemTemplate(ctx, auth.SharedTenant, "", "my-ci-shared-template-id")).To(Succeed())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant: testTenant,
					}.Build(),
					Title:       "My CI catalog item",
					Description: "My description.",
					Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-shared-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My CI catalog item"))
			Expect(object.GetTemplate().GetId()).To(Equal("my-ci-shared-template-id"))
			Expect(object.GetTemplate().GetShared()).To(BeTrue())
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("Rejects a template reference whose ID and name disagree", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
					Title:    "Mismatched template reference",
					Template: privatev1.ComputeInstanceTemplateReference_builder{
						Id: "my-ci-shared-template-id", Name: "different-template-name",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("id and name do not refer to the same resource"))
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    fmt.Sprintf("CI catalog item %d", i),
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My CI catalog item",
					Description: "My description.",
					Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-catalog-update",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
					Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
			name := object.GetMetadata().GetName()
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id:          object.GetId(),
					Metadata:    privatev1.Metadata_builder{Name: name}.Build(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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

		It("rejects changing the template on update", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-ci-catalog-template-immutable"}.Build(),
					Title:    "Catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Id:       createResponse.GetObject().GetId(),
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-shared-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceCatalogItemsGetRequest_builder{Id: createResponse.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTemplate().GetId()).To(Equal("my-ci-template-id"))
		})

		It("Update published using field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-catalog-published",
					}.Build(),
					Title:     "My CI catalog item",
					Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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

		It("Creates object with typed fields and round-trips them", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:    "CI catalog item with fields",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
						UserData: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
						RunStrategy: privatev1.ComputeInstanceRunStrategyFieldPolicy_builder{Locked: func() *privatev1.ComputeInstanceRunStrategy {
							v := privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
							return &v
						}()}.Build(),
					}.Build(),
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
			Expect(fetched.GetFields().GetUserData().GetEditable()).ToNot(BeNil())
			Expect(fetched.GetFields().GetRunStrategy().GetLocked()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
		})

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-ci-catalog-delete",
						Finalizers: []string{"a"},
					}.Build(),
					Title:    "My CI catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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

		It("Allows delete when referenced by a compute instance", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Referenced CI catalog item",
					Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					Published: true,
					Fields:    privatev1.ComputeInstanceCatalogItemFields_builder{}.Build(),
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
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
						Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "First CI catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "Second CI catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
						Tenant: testTenant,
					}.Build(),
					Title:    "CI catalog item for test tenant",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-shared-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update that introduces non-editable field without default", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-catalog-nodefault",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
					Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
						RunStrategy: privatev1.ComputeInstanceRunStrategyFieldPolicy_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"fields"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("oneof"))
		})

		It("Accepts update with valid typed policies", func() {
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.ComputeInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-catalog-validfd",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
					Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
						UserData: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"fields"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetFields()).ToNot(BeNil())
		})

		Describe("Instance type validation in fields", func() {
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
							Vcpus:     4,
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

			It("Returns warning when fields default references a DEPRECATED instance type on Create", func() {
				createInstanceTypeWithState("deprecated-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with deprecated default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "deprecated-type"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Returns warning when fields default references a DEPRECATED instance type on Update", func() {
				// Create a catalog item without fields first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-catalog-deprecated-upd",
						}.Build(),
						Title:    "Catalog item to update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				catalogItemId := createResponse.GetObject().GetId()
				name := createResponse.GetObject().GetMetadata().GetName()
				createInstanceTypeWithState("deprecated-type-upd",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Id:       catalogItemId,
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Title:    "Catalog item to update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "deprecated-type-upd"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetWarnings()).To(HaveLen(1))
			})

			It("Rejects Create when fields default references an OBSOLETE instance type", func() {
				createInstanceTypeWithState("obsolete-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with obsolete default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "obsolete-type"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Rejects Update when fields default references an OBSOLETE instance type", func() {
				// Create a catalog item first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-catalog-obsolete-upd",
						}.Build(),
						Title:    "Catalog item for obsolete update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
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
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "obsolete-type-upd"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Returns no warnings when fields default references an ACTIVE instance type", func() {
				createInstanceTypeWithState("active-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with active default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "active-type"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Rejects Create when fields default references a non-existent instance type", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with missing type",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableInstanceTypeReferenceField_builder{DefaultValue: privatev1.InstanceTypeReference_builder{Name: "non-existent-type"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})

			It("Skips validation when fields has no spec.instance_type path", func() {
				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item without instance type field",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							UserData: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

		})

		Describe("Disk image validation in fields", func() {
			It("Returns warning when fields default references a DEPRECATED disk image on Create", func() {
				createDiskImageWithLifecycle("deprecated-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
					privatev1.DiskImageDeprecation_builder{
						ObsolescenceTimestamp: timestamppb.New(
							time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
					}.Build())

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with deprecated disk image default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "deprecated-di"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("2027"))
			})

			It("Returns warning when fields default references a DEPRECATED disk image on Update", func() {
				// Create a catalog item without fields first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-catalog-di-deprecated-upd",
						}.Build(),
						Title:    "Catalog item to update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				catalogItemId := createResponse.GetObject().GetId()
				name := createResponse.GetObject().GetMetadata().GetName()

				createDiskImageWithLifecycle("deprecated-di-upd",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, nil)

				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Id:       catalogItemId,
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Title:    "Catalog item to update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "deprecated-di-upd"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetWarnings()).To(HaveLen(1))
				Expect(updateResponse.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Rejects Create when fields default references an OBSOLETE disk image", func() {
				createDiskImageWithLifecycle("obsolete-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, nil)

				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with obsolete disk image default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "obsolete-di"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Rejects Update when fields default references an OBSOLETE disk image", func() {
				// Create a catalog item first.
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-catalog-di-obsolete-upd",
						}.Build(),
						Title:    "Catalog item for obsolete disk image update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				catalogItemId := createResponse.GetObject().GetId()

				createDiskImageWithLifecycle("obsolete-di-upd",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, nil)

				_, err = server.Update(ctx, privatev1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Id:       catalogItemId,
						Title:    "Catalog item for obsolete disk image update",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "obsolete-di-upd"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Returns no warnings when fields default references an AVAILABLE disk image", func() {
				createDiskImageWithLifecycle("available-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with available disk image default",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "available-di"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())

				// The stored default must be normalized to an id-keyed {"id": ..., "name": ...}
				// reference object so the deletion-protection trigger (migration 101) matches on the
				// resolved id. A bare string was sent; validation resolves it and rewrites it in place
				// before persist. createDiskImageWithLifecycle sets Id == Name, so both are "available-di".
				normalized := response.GetObject().GetFields().GetDiskImage().GetEditable().GetDefaultValue()
				Expect(normalized).ToNot(BeNil())
				Expect(normalized.GetId()).To(Equal("available-di"))
				Expect(normalized.GetName()).To(Equal("available-di"))
			})

			It("Rejects Create when fields default references a non-existent disk image", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item with missing disk image",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "nonexistent-di"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})

			It("Rejects Create with NotFound when fields default references a disk image in another tenant", func() {
				// The generic List DAO now wraps the caller's CEL filter in parentheses
				// (OSAC-4127), so a top-level OR ("this.id == k || this.metadata.name == k")
				// stays scoped under the tenancy clause and a cross-tenant disk image cannot
				// leak through by name — validation must reject the reference with NotFound.

				// The disk image's tenant must exist first — the reverse-reference trigger
				// rejects a disk image whose tenant is unknown.
				tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
					SetLogger(logger).
					SetTableName("tenants").
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = tenantsDao.Create().SetObject(
					privatev1.Tenant_builder{
						Id: "other-tenant",
						Metadata: privatev1.Metadata_builder{
							Name:   "other-tenant",
							Tenant: "other-tenant",
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Seed an AVAILABLE disk image owned by that third tenant, using the suite-default
				// (universal) tenancy so the write itself is unfiltered.
				diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = diskImagesDao.Create().SetObject(
					privatev1.DiskImage_builder{
						Id: "other-tenant-di",
						Metadata: privatev1.Metadata_builder{
							Name:   "other-tenant-di",
							Tenant: "other-tenant",
						}.Build(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Build a catalog server whose caller can only see shared + the test tenant, so the
				// DAO's tenancy filter hides the "other-tenant" disk image (collapsing to
				// zero rows -> NotFound, without leaking cross-tenant existence).
				restrictedTenancy := auth.NewMockTenancyLogic(ctrl)
				restrictedVisibility, err := auth.NewVisibility().
					AddVisibleTenants(auth.SharedTenant, testTenant).
					Build()
				Expect(err).ToNot(HaveOccurred())
				restrictedTenancy.EXPECT().DetermineVisibility(gomock.Any()).
					Return(restrictedVisibility, nil).AnyTimes()
				restrictedTenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
					Return(collections.NewSet(testTenant), nil).AnyTimes()
				restrictedTenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
					Return(testTenant, nil).AnyTimes()

				restrictedServer, err := NewPrivateComputeInstanceCatalogItemsServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(restrictedTenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = restrictedServer.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item referencing a cross-tenant disk image",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Name: "other-tenant-di"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})

			Context("disk_image name-collision precedence", func() {
				// Preserve the established DiskImage precedence for an unqualified name: the
				// Catalog Item's tenant wins, followed by shared, otherwise the name is ambiguous.

				// buildCatalogServer wires a catalog server whose caller has the given default tenant
				// and can see the shared tenant plus the listed extra tenants.
				buildCatalogServer := func(defaultTenant string, extraVisible ...string) *PrivateComputeInstanceCatalogItemsServer {
					visibility, err := auth.NewVisibility().
						AddVisibleTenant(auth.SharedTenant).
						AddVisibleTenant(defaultTenant).
						AddVisibleTenants(extraVisible...).
						Build()
					Expect(err).ToNot(HaveOccurred())
					mockTenancy := auth.NewMockTenancyLogic(ctrl)
					mockTenancy.EXPECT().DetermineVisibility(gomock.Any()).
						Return(visibility, nil).AnyTimes()
					mockTenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
						Return(collections.NewSet(defaultTenant), nil).AnyTimes()
					mockTenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
						Return(defaultTenant, nil).AnyTimes()
					s, err := NewPrivateComputeInstanceCatalogItemsServer().
						SetLogger(logger).
						SetAttributionLogic(attribution).
						SetTenancyLogic(mockTenancy).
						Build()
					Expect(err).ToNot(HaveOccurred())
					return s
				}

				// storedDiskImageDefault pulls the normalized disk_image field default off a created
				// catalog item so its resolved id/name can be asserted.
				storedDiskImageDefault := func(item *privatev1.ComputeInstanceCatalogItem) *privatev1.DiskImageReference {
					return item.GetFields().GetDiskImage().GetEditable().GetDefaultValue()
				}

				createCatalogItem := func(
					s *PrivateComputeInstanceCatalogItemsServer,
					def *privatev1.DiskImageReference,
				) (*privatev1.ComputeInstanceCatalogItemsCreateResponse, error) {
					return s.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
						Object: privatev1.ComputeInstanceCatalogItem_builder{
							Metadata: privatev1.Metadata_builder{
								Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
							}.Build(),
							Title:    "Catalog item for disk image precedence",
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-shared-template-id"}.Build(),
							Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
								DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: def}.Build()}.Build(),
							}.Build(),
						}.Build(),
					}.Build())
				}

				It("Resolves a name collision to the caller's own-tenant disk image", func() {
					createTenant("my-tenant")
					createAvailableDiskImageInTenant("di-fedora-shared", "fedora", auth.SharedTenant)
					createAvailableDiskImageInTenant("di-fedora-tenant", "fedora", "my-tenant")

					s := buildCatalogServer("my-tenant", "my-tenant")
					resp, err := createCatalogItem(s, privatev1.DiskImageReference_builder{Name: "fedora"}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(resp.GetWarnings()).To(BeEmpty())

					def := storedDiskImageDefault(resp.GetObject())
					Expect(def).ToNot(BeNil())
					Expect(def.GetId()).To(Equal("di-fedora-tenant"))
					Expect(def.GetName()).To(Equal("fedora"))
				})

				It("Falls back to the shared disk image when the Catalog Item tenant has no match", func() {
					createTenant("my-tenant")
					createTenant("other-tenant")
					createAvailableDiskImageInTenant("di-ubuntu-shared", "ubuntu", auth.SharedTenant)
					createAvailableDiskImageInTenant("di-ubuntu-other", "ubuntu", "other-tenant")

					s := buildCatalogServer("my-tenant", "my-tenant", "other-tenant")
					resp, err := createCatalogItem(s, privatev1.DiskImageReference_builder{Name: "ubuntu"}.Build())
					Expect(err).ToNot(HaveOccurred())

					def := storedDiskImageDefault(resp.GetObject())
					Expect(def).ToNot(BeNil())
					Expect(def.GetId()).To(Equal("di-ubuntu-shared"))
				})

				It("Rejects an ambiguous name when neither the Catalog Item tenant nor shared owns it", func() {
					createTenant("tenant-a")
					createTenant("tenant-b")
					createAvailableDiskImageInTenant("di-fedora-a", "fedora", "tenant-a")
					createAvailableDiskImageInTenant("di-fedora-b", "fedora", "tenant-b")

					s := buildCatalogServer(testTenant, "tenant-a", "tenant-b")
					_, err := createCatalogItem(s, privatev1.DiskImageReference_builder{Name: "fedora"}.Build())
					Expect(err).To(HaveOccurred())
					status, ok := grpcstatus.FromError(err)
					Expect(ok).To(BeTrue())
					Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				})

				It("Uses the id and ignores the name when the default already carries an id", func() {
					createTenant("my-tenant")
					createAvailableDiskImageInTenant("di-fedora-shared", "fedora", auth.SharedTenant)
					createAvailableDiskImageInTenant("di-fedora-tenant", "fedora", "my-tenant")

					// The default pins the shared image by id even though the name "fedora" collides
					// with the caller's own-tenant image: the by-id path must win, unambiguously.
					s := buildCatalogServer("my-tenant", "my-tenant")
					def := privatev1.DiskImageReference_builder{Id: "di-fedora-shared", Name: "fedora"}.Build()
					resp, err := createCatalogItem(s, def)
					Expect(err).ToNot(HaveOccurred())

					stored := storedDiskImageDefault(resp.GetObject())
					Expect(stored).ToNot(BeNil())
					Expect(stored.GetId()).To(Equal("di-fedora-shared"))
					Expect(stored.GetName()).To(Equal("fedora"))
				})

				It("Is idempotent when re-validating an already-id-normalized default", func() {
					createDiskImageWithLifecycle("stable-di",
						privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

					// Feed the already-normalized {"id","name"} form (what a prior Create persisted and
					// migration 101 backfills). diskImageDefaultKey takes the by-id path, so it resolves
					// to the same image and re-stores the same id-form default.
					s := buildCatalogServer(testTenant)
					def := privatev1.DiskImageReference_builder{Id: "stable-di", Name: "stable-di"}.Build()
					resp, err := createCatalogItem(s, def)
					Expect(err).ToNot(HaveOccurred())

					stored := storedDiskImageDefault(resp.GetObject())
					Expect(stored).ToNot(BeNil())
					Expect(stored.GetId()).To(Equal("stable-di"))
					Expect(stored.GetName()).To(Equal("stable-di"))
				})
			})

			It("Skips validation when fields has no disk_image path", func() {
				response, err := server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.ComputeInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    "Catalog item without disk image field",
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "my-ci-template-id"}.Build(),
						Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
							UserData: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

		})
	})
})

var _ = Describe("Catalog publication and references", func() {
	It("rejects cross-tenant full dependencies even for an author with total visibility", func() {
		Expect(seedComputeCatalogItemTemplate(ctx, testTenant, "", "tenant-template")).To(Succeed())
		server, err := NewPrivateComputeInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{Object: privatev1.ComputeInstanceCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{Name: "shared-offering", Tenant: auth.SharedTenant}.Build(), Title: "Shared",
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "tenant-template"}.Build(),
		}.Build()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})
})

var _ = Describe("Compute Instance Catalog Item policy application", func() {
	It("applies every Compute policy and deep-clones nested values", func() {
		diskImage := privatev1.DiskImageReference_builder{Id: "image-id", Name: "image"}.Build()
		instanceType := privatev1.InstanceTypeReference_builder{Id: "type-id", Name: "type"}.Build()
		storageTier := privatev1.StorageTierReference_builder{Id: "tier-id", Name: "tier"}.Build()
		computeAttachment := privatev1.ComputeNetworkAttachment_builder{
			Subnet:         policyTestSubnet("subnet"),
			SecurityGroups: []*privatev1.SecurityGroupLocalReference{policyTestSecurityGroup("security-group")},
		}.Build()
		additionalDisk := privatev1.ComputeInstanceDisk_builder{
			SizeGib:     policyTestInt32(20),
			StorageTier: storageTier,
		}.Build()
		userData := "user-data"
		runStrategy := privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
		autoExternalIP := false
		bootSize := int32(30)

		fields := privatev1.ComputeInstanceCatalogItemFields_builder{
			DiskImage:    privatev1.DiskImageReferenceFieldPolicy_builder{Locked: diskImage}.Build(),
			InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{Locked: instanceType}.Build(),
			BootDisk: privatev1.ComputeInstanceBootDiskFieldPolicies_builder{
				SizeGib:     privatev1.Int32FieldPolicy_builder{Locked: &bootSize}.Build(),
				StorageTier: privatev1.StorageTierReferenceFieldPolicy_builder{Locked: storageTier}.Build(),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategyFieldPolicy_builder{Locked: &runStrategy}.Build(),
			UserData:    privatev1.StringFieldPolicy_builder{Locked: &userData}.Build(),
			NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.ComputeNetworkAttachmentList_builder{
					Items: []*privatev1.ComputeNetworkAttachment{computeAttachment},
				}.Build(),
			}.Build(),
			AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{Locked: &autoExternalIP}.Build(),
			AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
				Locked: privatev1.ComputeInstanceDiskList_builder{
					Items: []*privatev1.ComputeInstanceDisk{nil, additionalDisk},
				}.Build(),
			}.Build(),
		}.Build()
		item := privatev1.ComputeInstanceCatalogItem_builder{Fields: fields}.Build()
		spec := &privatev1.ComputeInstanceSpec{}

		Expect(applyComputeInstanceCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetDiskImage()).NotTo(BeIdenticalTo(diskImage))
		Expect(spec.GetDiskImage().GetId()).To(Equal("image-id"))
		Expect(spec.GetInstanceType().GetName()).To(Equal("type"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(bootSize))
		Expect(spec.GetBootDisk().GetStorageTier()).NotTo(BeIdenticalTo(storageTier))
		Expect(spec.GetRunStrategy()).To(Equal(runStrategy))
		Expect(spec.GetUserData()).To(Equal(userData))
		Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetAdditionalDisks()).To(HaveLen(2))
		Expect(spec.GetAdditionalDisks()[0]).To(BeNil())
		Expect(spec.GetAdditionalDisks()[1]).NotTo(BeIdenticalTo(additionalDisk))

		spec.GetDiskImage().SetName("changed")
		spec.GetBootDisk().GetStorageTier().SetName("changed")
		spec.GetNetworkAttachments()[0].GetSubnet().SetName("changed")
		spec.GetNetworkAttachments()[0].GetSecurityGroups()[0].SetName("changed")
		spec.GetAdditionalDisks()[1].GetStorageTier().SetName("changed")
		Expect(diskImage.GetName()).To(Equal("image"))
		Expect(storageTier.GetName()).To(Equal("tier"))
		Expect(computeAttachment.GetSubnet().GetName()).To(Equal("subnet"))
		Expect(computeAttachment.GetSecurityGroups()[0].GetName()).To(Equal("security-group"))
		Expect(additionalDisk.GetStorageTier().GetName()).To(Equal("tier"))
	})
})
