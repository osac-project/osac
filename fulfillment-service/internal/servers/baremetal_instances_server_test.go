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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Bare metal instances server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewBareMetalInstancesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server        *BareMetalInstancesServer
			catalogItemID string
		)

		BeforeEach(func() {
			var err error

			// Seed a published catalog item.
			catalogServer, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			catalogResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Test catalog item",
					Template:  "test-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItemID = catalogResp.GetObject().GetId()

			server, err = NewBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetSpec().GetCatalogItem()).To(Equal(catalogItemID))
		})

		It("Fails to create without an object", func() {
			_, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Lists objects", func() {
			const count = 3
			for i := range count {
				_, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
					Object: publicv1.BareMetalInstance_builder{
						Spec: publicv1.BareMetalInstanceSpec_builder{
							CatalogItem: catalogItemID,
						}.Build(),
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("bmi-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, publicv1.BareMetalInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Gets object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			getResponse, err := server.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(id))
			Expect(getResponse.GetObject().GetSpec().GetCatalogItem()).To(Equal(catalogItemID))
		})

		It("Updates mutable fields via field mask", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						RunStrategy: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			updateResponse, err := server.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Id: id,
					Spec: publicv1.BareMetalInstanceSpec_builder{
						RunStrategy: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.run_strategy"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRunStrategy()).To(
				Equal(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED))
		})

		It("Fails to update without an object", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Fails to update without an object identifier", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Rejects update that changes immutable catalog_item", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Id: id,
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: "different-catalog-item",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.catalog_item"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Deletes object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Delete(ctx, publicv1.BareMetalInstancesDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})
	})
})
