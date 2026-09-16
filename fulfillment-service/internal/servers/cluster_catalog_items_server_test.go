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
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var _ = Describe("Cluster catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewClusterCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *ClusterCatalogItemsServer

		BeforeEach(func() {
			var err error

			server, err = NewClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(seedClusterCatalogItemTemplate(ctx, testTenant, "", "my-template-id")).To(Succeed())
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("preserves private metadata on mask-less public updates", func(omitMetadata, lock, stale bool) {
			created, err := server.delegate.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: "preserve-finalizers", Finalizers: []string{"cleanup"}}.Build(),
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Title:    "Original", Published: true,
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			object := publicv1.ClusterCatalogItem_builder{
				Id: created.GetObject().GetId(), Title: "Updated", Published: false,
				Template: publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
			}.Build()
			if !omitMetadata {
				version := created.GetObject().GetMetadata().GetVersion()
				if stale {
					version--
				}
				object.SetMetadata(publicv1.Metadata_builder{Name: "preserve-finalizers", Version: version}.Build())
			}
			_, err = server.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{Object: object, Lock: lock}.Build())
			rejected := lock && (stale || omitMetadata)
			if rejected {
				Expect(status.Code(err)).To(Equal(codes.Aborted))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			stored, err := server.delegate.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{Id: object.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"cleanup"}))
			if rejected {
				Expect(stored.GetObject().GetTitle()).To(Equal("Original"))
				Expect(stored.GetObject().GetPublished()).To(BeTrue())
			} else {
				Expect(stored.GetObject().GetTitle()).To(Equal("Updated"))
				Expect(stored.GetObject().GetPublished()).To(BeFalse())
			}
		},
			Entry("metadata supplied", false, false, false),
			Entry("metadata omitted", true, false, false),
			Entry("matching version", false, true, false),
			Entry("stale version", false, true, true),
			Entry("missing version", true, true, false),
		)

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My cluster catalog item",
					Description: "My description.",
					Template:    publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
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
		})
		It("Updates object through the public API", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata:  publicv1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
					Title:     "Original title",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:    createResponse.GetObject().GetId(),
					Title: "Updated title",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetTemplate().GetId()).To(Equal("my-template-id"))
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						Published: true,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						Published: true,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with filter", func() {
			const count = 10
			var objects []*publicv1.ClusterCatalogItem
			for i := range count {
				response, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						Published: true,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			for _, object := range objects {
				response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Lists drafts and published items with optional published filter", func() {
			_, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Published item",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Unpublished item",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
			response, err = server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{Filter: new("this.published")}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetPublished()).To(BeTrue())
		})

		It("Lists drafts and published items filtered by ID", func() {
			publishedResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Target published",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Other published",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			unpublishedResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Target unpublished",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			targetID := publishedResponse.GetObject().GetId()
			unpublishedID := unpublishedResponse.GetObject().GetId()
			response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{
				Filter: new(fmt.Sprintf("this.id == %q || this.id == %q", targetID, unpublishedID)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
		})

		It("Reads unpublished object within normal visibility", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Unpublished item",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItemID := createResponse.GetObject().GetId()

			// Create a cluster that references the unpublished catalog item:
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
						CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: catalogItemID}.Build(),
						Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: catalogItemID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Reads unpublished object without a cluster reference", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Unpublished item",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My catalog item",
					Description: "My description.",
					Template:    publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
					Template:    publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			name := object.GetMetadata().GetName()
			updateResponse, err := server.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:          object.GetId(),
					Metadata:    publicv1.Metadata_builder{Name: name}.Build(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Updated description."))

			getResponse, err := server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Updated description."))
		})

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "My catalog item",
					Template:  publicv1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update cluster_catalog_items set finalizers = '{"a"}' where id = $1`,
				object.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, publicv1.ClusterCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

	})
})

var _ = Describe("Catalog publication and references", func() {
	It("Cluster: unpublishes unusable offerings and validates republishing", func() {
		Expect(seedClusterCatalogItemTemplate(ctx, auth.SharedTenant, "", "template-id")).To(Succeed())
		Expect(seedClusterCatalogItemTemplate(ctx, testTenant, "", "own-template-id")).To(Succeed())
		dependencyDAO, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		dependencyResponse, err := dependencyDAO.Create().SetObject(privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{Name: "dependency", Tenant: auth.SharedTenant}.Build(), Spec: privatev1.ClusterVersionSpec_builder{Version: "4.20", Image: "quay.io/test/release", Enabled: new(true)}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		dependency := dependencyResponse.GetObject()
		server, err := NewClusterCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		request := publicv1.ClusterCatalogItemsCreateRequest_builder{Object: publicv1.ClusterCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: "offering"}.Build(), Title: "Offering", Published: true,
			Template: publicv1.ClusterTemplateReference_builder{Name: "my-cluster-template", Shared: true}.Build(),
			Fields:   publicv1.ClusterCatalogItemFields_builder{Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{Locked: publicv1.ClusterVersionReference_builder{Id: dependency.GetId()}.Build()}.Build()}.Build(),
		}.Build()}.Build()
		original := proto.Clone(request)
		result, err := server.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(request, original)).To(BeTrue())
		item := result.GetObject()
		Expect(item.GetTemplate().GetId()).To(Equal("template-id"))
		dependency.GetSpec().SetEnabled(false)
		_, err = dependencyDAO.Update().SetObject(dependency).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		update := func(object *publicv1.ClusterCatalogItem, paths ...string) error {
			request := publicv1.ClusterCatalogItemsUpdateRequest_builder{Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths}}.Build()
			_, err := server.Update(ctx, request)
			return err
		}
		mixed := proto.Clone(item).(*publicv1.ClusterCatalogItem)
		mixed.SetPublished(false)
		mixed.GetFields().GetVersion().SetLocked(publicv1.ClusterVersionReference_builder{Id: "missing"}.Build())
		Expect(update(mixed, "published", "fields")).ToNot(Succeed())
		item.SetPublished(false)
		// References outside the mask must not affect the merged candidate.
		partial := publicv1.ClusterCatalogItem_builder{Id: item.GetId(), Published: false, Template: publicv1.ClusterTemplateReference_builder{Id: "missing"}.Build()}.Build()
		Expect(update(partial, "published")).To(Succeed())
		item.SetTitle("Edited draft")
		Expect(update(item, "title")).To(Succeed())
		item.SetPublished(true)
		Expect(status.Code(update(item, "published"))).To(Equal(codes.InvalidArgument))
		item.SetPublished(false)
		Expect(status.Code(update(item, "published", "fields"))).To(Equal(codes.OK))
		item.GetFields().GetVersion().SetLocked(publicv1.ClusterVersionReference_builder{Id: "missing"}.Build())
		Expect(update(item, "published", "fields")).ToNot(Succeed())
	})

})
