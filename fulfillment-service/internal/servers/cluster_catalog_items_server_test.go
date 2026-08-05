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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
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
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:       "My cluster catalog item",
					Description: "My description.",
					Template:    "my-template-id",
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My cluster catalog item"))
			Expect(object.GetTemplate()).To(Equal("my-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  "my-template-id",
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
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  "my-template-id",
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
						Title:     fmt.Sprintf("Catalog item %d", i),
						Template:  "my-template-id",
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

		It("List excludes unpublished objects", func() {
			_, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Published item",
					Template:  "my-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "my-template-id",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := server.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetTitle()).To(Equal("Published item"))
		})

		It("List with user filter excludes unpublished objects", func() {
			publishedResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Target published",
					Template:  "my-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Other published",
					Template:  "my-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			unpublishedResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Target unpublished",
					Template:  "my-template-id",
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
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(targetID))
		})

		It("Get returns unpublished item when caller has a referencing cluster", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "my-template-id",
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
						CatalogItem: catalogItemID,
						Template:    "my-template-id",
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: catalogItemID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Unpublished item"))
		})

		It("Get returns not found for unpublished object", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "my-template-id",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Title:       "My catalog item",
					Description: "My description.",
					Template:    "my-template-id",
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
					Title:       "Original title",
					Description: "Original description.",
					Template:    "my-template-id",
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:          object.GetId(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    "my-template-id",
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
					Title:     "My catalog item",
					Template:  "my-template-id",
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
