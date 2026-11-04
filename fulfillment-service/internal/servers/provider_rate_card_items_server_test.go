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
	"context"
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// widgetScenario scripts a service outside the built-in mappings, exercising exact-name selection and row mapping.
// Catalog cross-reference against the mapped VMaaS/CaaS services is covered separately, further down.
func widgetScenario() billingfake.Scenario {
	return billingfake.Scenario{
		Services: []billingfake.ServiceSpec{{
			ID:         "svc-1",
			Name:       "widget_service",
			Attributes: []billing.PriceableAttribute{{ID: "dim-1", Name: "widget_size"}},
			Rates: map[string]billingfake.RateCardSpec{
				"USD": {Rows: []billing.RateRow{
					{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.5",
						AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "large"}}},
					{DimensionID: "dim-1", PriceMethod: "FLAT_RATE", UnitRate: "5"},
				}},
			},
		}},
	}
}

var _ = Describe("Provider rate card items server", func() {
	build := func(p *billing.Provider, reg prometheus.Registerer) *ProviderRateCardItemsServer {
		private, err := NewPrivateProviderRateCardItemsServer().
			SetLogger(logger).SetProvider(p).SetTenancyLogic(tenancy).SetMetricsRegisterer(reg).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err := NewProviderRateCardItemsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return public
	}

	It("requires a provider", func() {
		_, err := NewPrivateProviderRateCardItemsServer().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).To(MatchError("provider is mandatory"))
	})

	It("fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED and makes no provider call when billing is off", func() {
		fake := billingfake.New(widgetScenario())
		server := build(billing.NewProvider("", fake, nil), prometheus.NewRegistry())
		_, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			CurrencyCode: proto.String("USD"),
		}.Build())
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
		Expect(fake.Calls()).To(BeEmpty())
	})

	DescribeTable("validates currency_code before calling the provider", func(currency *string, want codes.Code) {
		fake := billingfake.New(widgetScenario())
		server := build(fake.Provider(), prometheus.NewRegistry())
		interceptor, err := validation.NewProtovalidateInterceptor().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		request := publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: currency,
		}.Build()
		_, err = interceptor.UnaryServer(ctx, request, &grpc.UnaryServerInfo{},
			func(ctx context.Context, request any) (any, error) {
				return server.List(ctx, request.(*publicv1.ProviderRateCardItemsListRequest))
			})
		Expect(status.Code(err)).To(Equal(want))
		if want == codes.InvalidArgument {
			Expect(err.Error()).To(ContainSubstring("currency_code"))
			Expect(fake.Calls()).To(BeEmpty())
		}
	},
		Entry("omitted", nil, codes.InvalidArgument),
		Entry("empty", proto.String(""), codes.InvalidArgument),
		Entry("too short", proto.String("US"), codes.InvalidArgument),
		Entry("too long", proto.String("USDX"), codes.InvalidArgument),
		Entry("three characters", proto.String("USD"), codes.OK),
		Entry("no invented uppercase constraint", proto.String("usd"), codes.OK),
	)

	It("returns only the documented fields and omits an unsupported row, counting it by reason", func() {
		fake := billingfake.New(widgetScenario())
		registry := prometheus.NewRegistry()
		server := build(fake.Provider(), registry)
		response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetSize()).To(Equal(int32(1)))
		Expect(response.GetTotal()).To(Equal(int32(1)))
		item := response.GetItems()[0]
		Expect(item.GetService()).To(Equal("widget_service"))
		Expect(item.GetCurrencyCode()).To(Equal("USD"))
		Expect(item.GetRate().GetAmount()).To(Equal("0.5"))
		Expect(item.GetRate().GetUnit()).To(Equal("second"))
		Expect(item.GetResourceConditions()).To(HaveLen(1))
		Expect(item.GetResourceConditions()[0].GetDimension()).To(Equal("widget_size"))
		Expect(item.GetResourceConditions()[0].GetOperator()).To(
			Equal(publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS))
		Expect(item.GetResourceConditions()[0].GetValue()).To(Equal("large"))
		Expect(item.GetCatalogItemMatches()).To(BeEmpty())

		// Never a provider rule id, rate-card id, service id or native account id: the whole message has no such
		// field to carry one (AC7), so a reflective round trip through the wire type is itself the proof.
		Expect(proto.Equal(item, &publicv1.ProviderRateCardItem{
			Service: "widget_service", CurrencyCode: "USD",
			ResourceConditions: item.GetResourceConditions(), Rate: item.GetRate(),
		})).To(BeTrue())

		families, err := registry.Gather()
		Expect(err).ToNot(HaveOccurred())
		found := false
		for _, family := range families {
			if family.GetName() != unsupportedRateCardItemsMetricName {
				continue
			}
			for _, metric := range family.GetMetric() {
				found = true
				Expect(metric.GetCounter().GetValue()).To(Equal(1.0))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("serves repeated Lists of one service and currency from the rate-card cache", func() {
		fake := billingfake.New(widgetScenario())
		registry := prometheus.NewRegistry()
		server := build(fake.Provider(), registry)
		request := publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
		}.Build()
		first, err := server.List(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		second, err := server.List(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(second.GetTotal()).To(Equal(first.GetTotal()))
		Expect(second.GetSize()).To(Equal(first.GetSize()))

		// One provider read serves both Lists: the service, attribute and rate-card sweeps run once.
		Expect(fake.Succeeded("FindServiceByName")).To(Equal(1))
		Expect(fake.Succeeded("ListPriceableAttributes")).To(Equal(1))
		Expect(fake.Succeeded("GetServiceRateCard")).To(Equal(1))

		// The unsupported-row counter keeps its per-response meaning: the omitted FLAT_RATE row is counted again
		// when the second response is served from the cache.
		families, err := registry.Gather()
		Expect(err).ToNot(HaveOccurred())
		for _, family := range families {
			if family.GetName() != unsupportedRateCardItemsMetricName {
				continue
			}
			for _, metric := range family.GetMetric() {
				Expect(metric.GetCounter().GetValue()).To(Equal(2.0))
			}
		}
	})

	DescribeTable("rejects every nonempty tenant_id as unsupported before any provider call",
		func(tenantID string, state billing.AccountState, configured bool) {
			scenario := widgetScenario()
			scenario.Accounts = []billingfake.AccountSpec{{ID: "acct-1", ExternalID: "tenant-a", State: state}}
			fake := billingfake.New(scenario)
			provider := fake.Provider()
			if !configured {
				provider = billing.NewProvider("", fake, nil)
			}
			server := build(provider, prometheus.NewRegistry())
			_, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
				Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"), TenantId: proto.String(tenantID),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(fake.Calls()).To(BeEmpty())
		},
		Entry("unknown tenant", "no-such-tenant", billing.AccountActive, true),
		Entry("inactive provider account", "tenant-a", billing.AccountInactive, true),
		Entry("active provider account", "tenant-a", billing.AccountActive, true),
		Entry("whitespace is still nonempty", " ", billing.AccountUnknown, true),
		Entry("billing disabled", "tenant-a", billing.AccountActive, false),
	)

	DescribeTable("lists every mapped service when service is omitted, independently of catalog inventory",
		func(inventoryServices []string) {
			scenario := billingfake.Scenario{}
			for _, service := range []string{serviceComputeInstance, serviceCluster} {
				spec := widgetScenario().Services[0]
				spec.ID = "svc-" + service
				spec.Name = service
				scenario.Services = append(scenario.Services, spec)
			}
			fake := billingfake.New(scenario)
			server := build(fake.Provider(), prometheus.NewRegistry())
			if inventoryServices != nil {
				_, err := server.private.inventoryCache.get(ctx, catalogInventoryKey{},
					func(context.Context) ([]*privatev1.CatalogPricingInput, error) {
						var inventory []*privatev1.CatalogPricingInput
						for _, service := range inventoryServices {
							inventory = append(inventory, privatev1.CatalogPricingInput_builder{
								Service: service, Dimension: "widget_size", Value: "large",
							}.Build())
						}
						return inventory, nil
					})
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
				CurrencyCode: proto.String("USD"), TenantId: proto.String(""),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			var services []string
			for _, item := range response.GetItems() {
				services = append(services, item.GetService())
			}
			Expect(services).To(ConsistOf(serviceComputeInstance, serviceCluster))
			Expect(fake.Count("GetServiceRateCard")).To(Equal(2))
			Expect(fake.Count("ListAccounts")).To(BeZero())
		},
		Entry("empty inventory from both catalog DAOs", nil),
		Entry("inventory with only compute instances", []string{serviceComputeInstance}),
		Entry("inventory with both services", []string{serviceComputeInstance, serviceCluster}),
	)

	It("returns an empty list for a service with no matching provider name", func() {
		fake := billingfake.New(widgetScenario())
		server := build(fake.Provider(), prometheus.NewRegistry())
		response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("no_such_service"), CurrencyCode: proto.String("USD"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
		Expect(response.GetTotal()).To(Equal(int32(0)))
	})

	It("returns UNAVAILABLE, not a guessed row, on a provider failure", func() {
		scenario := widgetScenario()
		scenario.Failures = map[string]billingfake.Failure{"GetServiceRateCard": {Reason: billing.ReasonTimeout}}
		fake := billingfake.New(scenario)
		server := build(fake.Provider(), prometheus.NewRegistry())
		_, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
		}.Build())
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	It("paginates the in-memory row list using offset and limit", func() {
		scenario := widgetScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "1",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "a"}}},
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "2",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "b"}}},
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "3",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "c"}}},
		}}
		fake := billingfake.New(scenario)
		server := build(fake.Provider(), prometheus.NewRegistry())
		response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
			Offset: proto.Int32(1), Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(3)))
		Expect(response.GetSize()).To(Equal(int32(1)))
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetRate().GetAmount()).To(Equal("2"))
	})

	DescribeTable("preserves the platform pagination contract through the public wrapper",
		func(offset int32, limit *int32, wantSize int, wantCode codes.Code) {
			const total = 1205
			rows := make([]billing.RateRow, total)
			for i := range rows {
				rows[i] = billing.RateRow{
					DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "1",
					AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: fmt.Sprint(i)}},
				}
			}
			scenario := widgetScenario()
			scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: rows}
			fake := billingfake.New(scenario)
			server := build(fake.Provider(), prometheus.NewRegistry())
			response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
				Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
				Offset: proto.Int32(offset), Limit: limit,
			}.Build())
			Expect(status.Code(err)).To(Equal(wantCode))
			if wantCode != codes.OK {
				Expect(response).To(BeNil())
				return
			}
			Expect(response.GetTotal()).To(Equal(int32(total)))
			Expect(response.GetSize()).To(Equal(int32(wantSize)))
			Expect(response.GetItems()).To(HaveLen(wantSize))
			if wantSize > 0 {
				Expect(response.GetItems()[0].GetResourceConditions()[0].GetValue()).To(Equal(fmt.Sprint(max(offset, 0))))
			}
		},
		Entry("omitted limit defaults to 100", int32(0), nil, 100, codes.OK),
		Entry("explicit zero is count-only", int32(0), proto.Int32(0), 0, codes.OK),
		Entry("negative limit is invalid", int32(0), proto.Int32(-1), 0, codes.InvalidArgument),
		Entry("negative offset clamps to zero", int32(-1), proto.Int32(2), 2, codes.OK),
		Entry("limit capped at 1000", int32(0), proto.Int32(2147483647), 1000, codes.OK),
		Entry("offset and large limit do not overflow", int32(1200), proto.Int32(2147483647), 5, codes.OK),
		Entry("offset past end is empty", int32(2147483647), nil, 0, codes.OK),
	)

	It("applies the CEL filter to the rows, before pagination", func() {
		scenario := widgetScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "1",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "a"}}},
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "2",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "b"}}},
		}}
		fake := billingfake.New(scenario)
		server := build(fake.Provider(), prometheus.NewRegistry())
		response, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
			Filter: proto.String(`this.rate.amount == "2"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(response.GetSize()).To(Equal(int32(1)))
		Expect(response.GetItems()[0].GetRate().GetAmount()).To(Equal("2"))
	})

	It("rejects an invalid filter", func() {
		fake := billingfake.New(widgetScenario())
		server := build(fake.Provider(), prometheus.NewRegistry())
		_, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
			Filter: proto.String(`this.rate.amount ==`),
		}.Build())
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})

	// 'order' stays an accepted-but-unapplied no-op (OSAC-6891), matching docs/API.md's system-wide statement that
	// order is "defined in the proto files but not yet implemented"; implementing it would be scope this story
	// does not cover.
	It("accepts but does not apply 'order'", func() {
		scenario := widgetScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "1",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "a"}}},
			{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "2",
				AttributeValues: []billing.AttributeValue{{Name: "widget_size", Value: "b"}}},
		}}
		fake := billingfake.New(scenario)
		server := build(fake.Provider(), prometheus.NewRegistry())
		unordered, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ordered, err := server.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("widget_service"), CurrencyCode: proto.String("USD"),
			Order: proto.String("rate.amount desc"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(ordered, unordered)).To(BeTrue())
	})
})

var _ = Describe("Provider rate card items server catalog cross-reference", func() {
	buildIndex := func(computeItems []*privatev1.ComputeInstanceCatalogItem,
		clusterItems []*privatev1.ClusterCatalogItem) catalogDimensionIndex {
		return buildCatalogDimensionIndex(aggregateCatalogPricingInputs(computeItems, clusterItems))
	}

	It("matches a CaaS row against a cluster catalog item's locked baremetal instance type", func() {
		clusterItem := privatev1.ClusterCatalogItem_builder{
			Id:       "k1",
			Metadata: privatev1.Metadata_builder{Name: "caas-large"}.Build(),
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
		}.Build()
		idx := buildIndex(nil, []*privatev1.ClusterCatalogItem{clusterItem})

		item := pricing.RateCardItem{
			Service: "cluster",
			Conditions: []pricing.RateCondition{
				{Dimension: "baremetal_instance_type", Operator: pricing.OperatorEquals, Value: "large"},
			},
		}
		Expect(matchCatalogItems(item, idx)).To(ConsistOf("caas-large"))
	})

	It("matches a VMaaS disk-dimension row against a catalog item's locked additional disk size", func() {
		computeItem := privatev1.ComputeInstanceCatalogItem_builder{
			Id:       "c1",
			Metadata: privatev1.Metadata_builder{Name: "vm-fast-disk"}.Build(),
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
					Locked: privatev1.ComputeInstanceDiskList_builder{
						Items: []*privatev1.ComputeInstanceDisk{
							privatev1.ComputeInstanceDisk_builder{SizeGib: int32Ptr(100)}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		idx := buildIndex([]*privatev1.ComputeInstanceCatalogItem{computeItem}, nil)

		item := pricing.RateCardItem{
			Service: "compute_instance",
			Conditions: []pricing.RateCondition{
				{Dimension: "additional_disk_size_gib", Operator: pricing.OperatorEquals, Value: "100"},
			},
		}
		Expect(matchCatalogItems(item, idx)).To(ConsistOf("vm-fast-disk"))
	})

	It("matches an ANY condition against every catalog item offering any value for that dimension", func() {
		openItem := privatev1.ComputeInstanceCatalogItem_builder{
			Id:       "c1",
			Metadata: privatev1.Metadata_builder{Name: "vm-open"}.Build(),
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
					Editable: privatev1.EditableInstanceTypeReferenceField_builder{}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		lockedItem := privatev1.ComputeInstanceCatalogItem_builder{
			Id:       "c2",
			Metadata: privatev1.Metadata_builder{Name: "vm-locked"}.Build(),
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
					Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "small"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		idx := buildIndex([]*privatev1.ComputeInstanceCatalogItem{openItem, lockedItem}, nil)

		item := pricing.RateCardItem{
			Service:    "compute_instance",
			Conditions: []pricing.RateCondition{{Dimension: "instance_type", Operator: pricing.OperatorAny}},
		}
		Expect(matchCatalogItems(item, idx)).To(ConsistOf("vm-open", "vm-locked"))
	})

	It("returns an empty, valid result for a row matching no catalog item", func() {
		idx := buildIndex(nil, nil)
		item := pricing.RateCardItem{
			Service: "compute_instance",
			Conditions: []pricing.RateCondition{
				{Dimension: "instance_type", Operator: pricing.OperatorEquals, Value: "ghost"},
			},
		}
		Expect(matchCatalogItems(item, idx)).To(BeEmpty())
	})

	It("requires every one of a row's conditions to match the same catalog item", func() {
		item := privatev1.ComputeInstanceCatalogItem_builder{
			Id:       "c1",
			Metadata: privatev1.Metadata_builder{Name: "vm-small-fast"}.Build(),
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
					Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "small"}.Build(),
				}.Build(),
				AdditionalDisks: privatev1.ComputeInstanceDiskListFieldPolicy_builder{
					Locked: privatev1.ComputeInstanceDiskList_builder{
						Items: []*privatev1.ComputeInstanceDisk{
							privatev1.ComputeInstanceDisk_builder{SizeGib: int32Ptr(50)}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		idx := buildIndex([]*privatev1.ComputeInstanceCatalogItem{item}, nil)

		matching := pricing.RateCardItem{
			Service: "compute_instance",
			Conditions: []pricing.RateCondition{
				{Dimension: "instance_type", Operator: pricing.OperatorEquals, Value: "small"},
				{Dimension: "additional_disk_size_gib", Operator: pricing.OperatorEquals, Value: "50"},
			},
		}
		Expect(matchCatalogItems(matching, idx)).To(ConsistOf("vm-small-fast"))

		notMatching := pricing.RateCardItem{
			Service: "compute_instance",
			Conditions: []pricing.RateCondition{
				{Dimension: "instance_type", Operator: pricing.OperatorEquals, Value: "small"},
				{Dimension: "additional_disk_size_gib", Operator: pricing.OperatorEquals, Value: "100"},
			},
		}
		Expect(matchCatalogItems(notMatching, idx)).To(BeEmpty())
	})

	It("cross-references a CaaS row end-to-end through the server's List, using both catalog DAOs", func() {
		clusterItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
			SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = clusterItemsDao.Create().SetObject(privatev1.ClusterCatalogItem_builder{
			Id:       uuid.NewString(),
			Metadata: privatev1.Metadata_builder{Name: "caas-large", Tenant: testTenant}.Build(),
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

		scenario := billingfake.Scenario{
			Services: []billingfake.ServiceSpec{{
				ID:         "svc-cluster",
				Name:       "cluster",
				Attributes: []billing.PriceableAttribute{{ID: "dim-1", Name: "baremetal_instance_type"}},
				Rates: map[string]billingfake.RateCardSpec{"USD": {Rows: []billing.RateRow{
					{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "4",
						AttributeValues: []billing.AttributeValue{{Name: "baremetal_instance_type", Value: "large"}}},
				}}},
			}},
		}
		fake := billingfake.New(scenario)
		private, err := NewPrivateProviderRateCardItemsServer().
			SetLogger(logger).SetProvider(fake.Provider()).SetTenancyLogic(tenancy).
			SetMetricsRegisterer(prometheus.NewRegistry()).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err := NewProviderRateCardItemsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())

		response, err := public.List(ctx, publicv1.ProviderRateCardItemsListRequest_builder{
			Service: proto.String("cluster"), CurrencyCode: proto.String("USD"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetCatalogItemMatches()).To(ConsistOf("caas-large"))
	})
})
