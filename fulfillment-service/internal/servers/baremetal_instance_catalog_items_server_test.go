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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Bare metal instance catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *BareMetalInstanceCatalogItemsServer

		BeforeEach(func() {
			var err error
			server, err = NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:       "My BMI catalog item",
					Description: "My description.",
					Template:    "my-bmi-template-id",
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My BMI catalog item"))
			Expect(object.GetTemplate()).To(Equal("my-bmi-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
		})

		It("Fails to create without an object", func() {
			_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Lists only published objects", func() {
			const publishedCount = 3
			for i := range publishedCount {
				_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Title:     fmt.Sprintf("Published item %d", i),
						Template:  "my-bmi-template-id",
						Published: true,
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}
			_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "my-bmi-template-id",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := server.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(publishedCount))
			for _, item := range response.GetItems() {
				Expect(item.GetPublished()).To(BeTrue())
			}
		})

		It("Rejects an invalid filter", func() {
			_, err := server.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{
				Filter: new("!!!invalid!!!"),
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Gets a published object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Published item",
					Template:  "my-bmi-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			getResponse, err := server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(id))
		})

		It("Returns not found for an unpublished object with no references", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "my-bmi-template-id",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})

		It("Updates an object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Original title",
					Template:  "my-bmi-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			updateResponse, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:        id,
					Title:     "Updated title",
					Template:  "my-bmi-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
		})

		It("Fails to update without an object", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Fails to update without an object identifier", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Deletes an object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Item to delete",
					Template:  "my-bmi-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})

		It("Fails to delete an object that is referenced by a bare metal instance", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Referenced item",
					Template:  "my-bmi-template-id",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItemID := createResponse.GetObject().GetId()

			instancesServer, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = instancesServer.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItemID,
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.FailedPrecondition))
		})

		It("Returns not found for a nonexistent object", func() {
			_, err := server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: "00000000-0000-0000-0000-000000000000",
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})
	})
})
