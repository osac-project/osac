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
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private catalog pricing inputs server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateCatalogPricingInputsServer().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateCatalogPricingInputsServer().SetTenancyLogic(tenancy).Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateCatalogPricingInputsServer().SetLogger(logger).Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server          *PrivateCatalogPricingInputsServer
			computeItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
			clusterItemsDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]
		)

		BeforeEach(func() {
			var err error
			server, err = NewPrivateCatalogPricingInputsServer().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			computeItemsDao, err = dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			clusterItemsDao, err = dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createComputeItem := func(id, name string, published bool) {
			_, err := computeItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
				Id:        id,
				Metadata:  privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
				Title:     "title-" + name,
				Published: published,
				Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
						Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		createClusterItem := func(id, name string, published bool) {
			_, err := clusterItemsDao.Create().SetObject(privatev1.ClusterCatalogItem_builder{
				Id:        id,
				Metadata:  privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
				Title:     "title-" + name,
				Published: published,
				Fields: privatev1.ClusterCatalogItemFields_builder{
					NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{
						Locked: privatev1.ClusterNodeSetMap_builder{
							Items: map[string]*privatev1.ClusterCatalogNodeSet{
								"workers": privatev1.ClusterCatalogNodeSet_builder{
									Size: 3,
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
										Id: "bt1", Name: "large",
									}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		It("returns an empty inventory when there are no catalog items", func() {
			response, err := server.List(ctx, &privatev1.CatalogPricingInputsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(BeEmpty())
			Expect(response.GetTotal()).To(BeZero())
			Expect(response.GetSize()).To(BeZero())
		})

		It("aggregates across both VMaaS and CaaS catalog items, draft and published", func() {
			createComputeItem(uuid.NewString(), "vmaas-published", true)
			createComputeItem(uuid.NewString(), "vmaas-draft", false)
			createClusterItem(uuid.NewString(), "caas-published", true)

			response, err := server.List(ctx, &privatev1.CatalogPricingInputsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(3))) // instance_type, node_count, baremetal_instance_type

			var services []string
			for _, item := range response.GetItems() {
				services = append(services, item.GetService())
			}
			Expect(services).To(ConsistOf("compute_instance", "cluster", "cluster"))

			for _, item := range response.GetItems() {
				if item.GetDimension() == "instance_type" {
					Expect(item.GetCatalogItems()).To(HaveLen(2))
					var publishedFlags []bool
					for _, catalogItem := range item.GetCatalogItems() {
						publishedFlags = append(publishedFlags, catalogItem.GetPublished())
					}
					Expect(publishedFlags).To(ConsistOf(true, false))
				}
			}
		})

		It("paginates the deterministic aggregated list", func() {
			createComputeItem(uuid.NewString(), "vmaas-1", true)
			createClusterItem(uuid.NewString(), "caas-1", true)

			full, err := server.List(ctx, &privatev1.CatalogPricingInputsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(full.GetTotal()).To(Equal(int32(3)))

			page, err := server.List(ctx, privatev1.CatalogPricingInputsListRequest_builder{
				Offset: proto.Int32(1),
				Limit:  proto.Int32(1),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(3)))
			Expect(page.GetSize()).To(Equal(int32(1)))
			Expect(page.GetItems()[0]).To(Equal(full.GetItems()[1]))
		})

		It("applies the CEL filter to the aggregated rows", func() {
			createComputeItem(uuid.NewString(), "vmaas-1", true)
			createClusterItem(uuid.NewString(), "caas-1", true)

			response, err := server.List(ctx, privatev1.CatalogPricingInputsListRequest_builder{
				Filter: proto.String(`this.service == "cluster"`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(2)))
			for _, item := range response.GetItems() {
				Expect(item.GetService()).To(Equal("cluster"))
			}
		})

		It("rejects an invalid filter", func() {
			_, err := server.List(ctx, privatev1.CatalogPricingInputsListRequest_builder{
				Filter: proto.String(`this.service ==`),
			}.Build())
			Expect(err).To(HaveOccurred())
		})

		// AC5: this server never imports the billing package, so it structurally cannot make a provider call.
		// This is proven once, at package scope, by ../catalog_pricing_inputs_aggregation_test.go and by the
		// absence of any billing import in private_catalog_pricing_inputs_server.go.
	})
})
