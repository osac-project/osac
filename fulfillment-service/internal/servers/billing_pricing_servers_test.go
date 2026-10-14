/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPriceEstimateUsesSelectedConfiguration(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{{
		Service:      billing.ServiceComputeInstance,
		CurrencyCode: "USD",
		Conditions: []billing.RateCondition{
			{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
			{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"},
		},
		Amount: "10.25",
		Unit:   billing.RateUnitMonth,
	}}}
	computeItems := &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}
	query, estimate := newBillingPricingTestServers(t, provider, computeItems, auth.AllTenants)

	configuration := billingPricingTestConfiguration()
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")
	response, err := estimate.Create(ctx, publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: configuration,
	}.Build())
	if err != nil {
		t.Fatalf("PriceEstimates.Create() error = %v", err)
	}
	if response.GetCurrencyCode() != "USD" || response.GetStatus() != publicv1.PriceEstimateStatus_PRICE_ESTIMATE_STATUS_CURRENT {
		t.Fatalf("estimate metadata = (%q, %v), want USD and CURRENT", response.GetCurrencyCode(), response.GetStatus())
	}
	if response.GetMonthlyAmount() != "10.25" || response.GetHourlyAmount() != "0.014041" {
		t.Fatalf("normalized estimate = (%q/hour, %q/month), want (0.014041, 10.25)", response.GetHourlyAmount(), response.GetMonthlyAmount())
	}
	if len(response.GetComponents()) != 1 || response.GetComponents()[0].GetRateAmount() != "10.25" || response.GetComponents()[0].GetRateUnit() != "month" {
		t.Fatalf("estimate components = %v, want the provider's monthly rate preserved", response.GetComponents())
	}
	if computeItems.calls != 1 || provider.calls != 1 {
		t.Fatalf("catalog/provider calls = (%d, %d), want (1, 1)", computeItems.calls, provider.calls)
	}
	if len(query.service.resolver.cache) != 1 {
		t.Fatalf("rate card cache size = %d, want 1", len(query.service.resolver.cache))
	}
}

func TestPriceEstimateSumsSelectedAdditionalDiskRates(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{
		{
			Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "10", Unit: billing.RateUnitHour,
			Conditions: []billing.RateCondition{
				{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
				{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"},
			},
		},
		{
			Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3", Unit: billing.RateUnitMonth,
			Conditions: []billing.RateCondition{
				{Dimension: "additional_disk_size_gib", Operator: billing.RateMatchEquals, Value: "50"},
				{Dimension: "additional_storage_tier", Operator: billing.RateMatchEquals, Value: "premium"},
			},
		},
		{
			Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "5", Unit: billing.RateUnitMonth,
			Conditions: []billing.RateCondition{
				{Dimension: "additional_disk_size_gib", Operator: billing.RateMatchEquals, Value: "100"},
				{Dimension: "additional_storage_tier", Operator: billing.RateMatchEquals, Value: "archive"},
			},
		},
	}}

	firstDisk := publicv1.ComputeInstanceDisk_builder{
		SizeGib:     billingTestInt32(50),
		StorageTier: publicv1.StorageTierReference_builder{Name: "premium"}.Build(),
	}.Build()
	secondDisk := publicv1.ComputeInstanceDisk_builder{
		SizeGib:     billingTestInt32(100),
		StorageTier: publicv1.StorageTierReference_builder{Name: "archive"}.Build(),
	}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		DiskImage:    billingPricingTestComputeCatalogItem().GetFields().GetDiskImage(),
		InstanceType: billingPricingTestComputeCatalogItem().GetFields().GetInstanceType(),
		AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{Locked: publicv1.ComputeInstanceDiskList_builder{
			Items: []*publicv1.ComputeInstanceDisk{firstDisk, secondDisk},
		}.Build()}.Build(),
	}.Build()
	computeItems := &billingPricingTestComputeCatalog{item: publicv1.ComputeInstanceCatalogItem_builder{
		Title:  "Small VM",
		Fields: fields,
	}.Build()}
	_, estimate := newBillingPricingTestServers(t, provider, computeItems, auth.AllTenants)

	configuration := publicv1.BillingPricingConfiguration_builder{ComputeInstance: publicv1.ComputeInstanceSpec_builder{
		CatalogItem:     publicv1.ComputeInstanceCatalogItemReference_builder{Id: "catalog-vm"}.Build(),
		DiskImage:       publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		InstanceType:    publicv1.InstanceTypeReference_builder{Name: "large"}.Build(),
		AdditionalDisks: []*publicv1.ComputeInstanceDisk{firstDisk, secondDisk},
	}.Build()}.Build()
	response, err := estimate.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: configuration,
	}.Build())
	if err != nil {
		t.Fatalf("PriceEstimates.Create() error = %v", err)
	}
	if len(response.GetComponents()) != 3 {
		t.Fatalf("estimate has %d components, want base and both disks: %v", len(response.GetComponents()), response.GetComponents())
	}
	if response.GetHourlyAmount() != "10.010959" || response.GetMonthlyAmount() != "7308" {
		t.Fatalf("estimate total = (%q/hour, %q/month), want (10.010959, 7308)", response.GetHourlyAmount(), response.GetMonthlyAmount())
	}
}

func TestPriceEstimateAcceptsNegativeHourlyCreditRate(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{{
		Service:      billing.ServiceComputeInstance,
		CurrencyCode: "USD",
		Conditions: []billing.RateCondition{
			{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
			{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"},
		},
		Amount: "-1",
		Unit:   billing.RateUnitHour,
	}}}
	_, estimate := newBillingPricingTestServers(t, provider, &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}, auth.AllTenants)

	response, err := estimate.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if err != nil {
		t.Fatalf("PriceEstimates.Create() error = %v, want a credit estimate", err)
	}
	if response.GetHourlyAmount() != "-1" || response.GetMonthlyAmount() != "-730" {
		t.Fatalf("credit estimate = (%q/hour, %q/month), want (-1, -730)", response.GetHourlyAmount(), response.GetMonthlyAmount())
	}
}

func TestPriceEstimateRejectsMissingComponentRate(t *testing.T) {
	t.Parallel()

	version := publicv1.ClusterVersionReference_builder{Name: "4.18"}.Build()
	clusterItems := &billingPricingTestClusterCatalog{item: publicv1.ClusterCatalogItem_builder{
		Id:       "catalog-cluster",
		Title:    "Standard cluster",
		Template: publicv1.ClusterTemplateReference_builder{Id: "template-standard", Name: "standard"}.Build(),
		Fields: publicv1.ClusterCatalogItemFields_builder{
			Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
		}.Build(),
	}.Build()}
	_, estimate := newBillingPricingTestServersWithClusterCatalog(t, &billingPricingTestProvider{}, clusterItems, auth.AllTenants)
	configuration := publicv1.BillingPricingConfiguration_builder{Cluster: publicv1.ClusterSpec_builder{
		CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: "catalog-cluster"}.Build(),
		Version:     version,
	}.Build()}.Build()

	_, err := estimate.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: configuration,
	}.Build())
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_COVERAGE_UNAVAILABLE" {
		t.Fatalf("PriceEstimates.Create() error = %v, want FailedPrecondition BILLING_COVERAGE_UNAVAILABLE", err)
	}
}

func TestPriceEstimateMultipliesSelectedClusterNodeSetQuantity(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{
		{
			Service: billing.ServiceCluster, CurrencyCode: "USD", Amount: "4", Unit: billing.RateUnitMonth,
			Conditions: []billing.RateCondition{
				{Dimension: "cluster_template", Operator: billing.RateMatchEquals, Value: "standard"},
				{Dimension: "release_image", Operator: billing.RateMatchEquals, Value: "4.18"},
			},
		},
		{
			Service: billing.ServiceCluster, CurrencyCode: "USD", Amount: "2", Unit: billing.RateUnitHour,
			QuantityDimension: "node_count",
			Conditions: []billing.RateCondition{
				{Dimension: "cluster_template", Operator: billing.RateMatchEquals, Value: "standard"},
				{Dimension: "release_image", Operator: billing.RateMatchEquals, Value: "4.18"},
				{Dimension: "baremetal_instance_type", Operator: billing.RateMatchEquals, Value: "cpu"},
				{Dimension: "node_count", Operator: billing.RateMatchEquals, Value: "10"},
			},
		},
		{
			Service: billing.ServiceCluster, CurrencyCode: "USD", Amount: "6", Unit: billing.RateUnitMonth,
			Conditions: []billing.RateCondition{
				{Dimension: "cluster_template", Operator: billing.RateMatchEquals, Value: "standard"},
				{Dimension: "release_image", Operator: billing.RateMatchEquals, Value: "4.18"},
				{Dimension: "baremetal_instance_type", Operator: billing.RateMatchEquals, Value: "gpu"},
				{Dimension: "node_count", Operator: billing.RateMatchEquals, Value: "2"},
			},
		},
	}}
	cpuType := publicv1.BareMetalInstanceTypeReference_builder{Name: "cpu"}.Build()
	gpuType := publicv1.BareMetalInstanceTypeReference_builder{Name: "gpu"}.Build()
	version := publicv1.ClusterVersionReference_builder{Name: "4.18"}.Build()
	nodeSets := map[string]*publicv1.ClusterNodeSet{
		"compute": publicv1.ClusterNodeSet_builder{Size: billingTestInt32(10), BaremetalInstanceType: cpuType}.Build(),
		"gpu":     publicv1.ClusterNodeSet_builder{Size: billingTestInt32(2), BaremetalInstanceType: gpuType}.Build(),
	}
	fields := publicv1.ClusterCatalogItemFields_builder{
		Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
		NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{Items: map[string]*publicv1.ClusterCatalogNodeSet{
			"compute": publicv1.ClusterCatalogNodeSet_builder{Size: 10, BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Name: "cpu"}.Build()}.Build(),
			"gpu":     publicv1.ClusterCatalogNodeSet_builder{Size: 2, BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Name: "gpu"}.Build()}.Build(),
		}}.Build()}.Build(),
	}.Build()
	clusterItems := &billingPricingTestClusterCatalog{item: publicv1.ClusterCatalogItem_builder{
		Title:    "Standard cluster",
		Template: publicv1.ClusterTemplateReference_builder{Name: "standard"}.Build(),
		Fields:   fields,
	}.Build()}
	_, estimate := newBillingPricingTestServersWithClusterCatalog(t, provider, clusterItems, auth.AllTenants)
	configuration := publicv1.BillingPricingConfiguration_builder{Cluster: publicv1.ClusterSpec_builder{
		CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: "catalog-cluster"}.Build(),
		Version:     version,
		NodeSets:    nodeSets,
	}.Build()}.Build()

	response, err := estimate.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: configuration,
	}.Build())
	if err != nil {
		t.Fatalf("PriceEstimates.Create() error = %v", err)
	}
	if len(response.GetComponents()) != 3 {
		t.Fatalf("estimate has %d components, want base and both node sets: %v", len(response.GetComponents()), response.GetComponents())
	}
	if response.GetHourlyAmount() != "20.013698" || response.GetMonthlyAmount() != "14610" {
		t.Fatalf("estimate total = (%q/hour, %q/month), want (20.013698, 14610)", response.GetHourlyAmount(), response.GetMonthlyAmount())
	}
}

func TestPricingQueryReportsCoverageGapWithoutAmount(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{}
	computeItems := &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}
	query, estimate := newBillingPricingTestServers(t, provider, computeItems, auth.AllTenants)

	response, err := query.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PricingQueriesCreateRequest_builder{
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if err != nil {
		t.Fatalf("PricingQueries.Create() error = %v", err)
	}
	if response.GetCurrencyCode() != "USD" || len(response.GetComponents()) != 1 {
		t.Fatalf("query response = %v, want one USD component", response)
	}
	component := response.GetComponents()[0]
	if component.GetCoverage() != publicv1.PricingCoverageState_PRICING_COVERAGE_STATE_GAP {
		t.Fatalf("coverage = %v, want GAP", component.GetCoverage())
	}
	if component.HasAmount() || component.HasUnit() {
		t.Fatalf("gap result includes a price: %v", component)
	}
	_, err = estimate.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_COVERAGE_GAP" {
		t.Fatalf("PriceEstimates.Create() error = %v, want FailedPrecondition BILLING_COVERAGE_GAP", err)
	}
}

func TestPricingQueryRejectsTenantOutsideAssignmentScopeBeforeLookups(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "1", Unit: billing.RateUnitHour}}}
	computeItems := &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}
	query, _ := newBillingPricingTestServers(t, provider, computeItems, collections.NewSet[string]())

	_, err := query.Create(context.Background(), publicv1.PricingQueriesCreateRequest_builder{
		TenantId:      stringPointer("tenant-b"),
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("PricingQueries.Create() error = %v, want PermissionDenied", err)
	}
	if computeItems.calls != 0 || provider.calls != 0 {
		t.Fatalf("catalog/provider calls = (%d, %d), want no lookups before tenant authorization", computeItems.calls, provider.calls)
	}
}

func TestPriceEstimateRejectsRetainedRateAfterRefreshFailure(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{{
		Service:      billing.ServiceComputeInstance,
		CurrencyCode: "USD",
		Conditions: []billing.RateCondition{
			{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
			{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"},
		},
		Amount: "4.50",
		Unit:   billing.RateUnitHour,
	}}}
	_, estimate := newBillingPricingTestServers(t, provider, &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}, auth.AllTenants)
	configuration := billingPricingTestConfiguration()
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")
	if _, err := estimate.Create(ctx, publicv1.PriceEstimatesCreateRequest_builder{Configuration: configuration}.Build()); err != nil {
		t.Fatalf("initial PriceEstimates.Create() error = %v", err)
	}

	provider.err = errors.New("provider unavailable")
	now := time.Now().Add(catalogPricingCacheTTL + time.Second)
	estimate.service.resolver.now = func() time.Time { return now }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	response, err := estimate.Create(canceled, publicv1.PriceEstimatesCreateRequest_builder{Configuration: configuration}.Build())
	if response != nil {
		t.Fatalf("stale PriceEstimates.Create() response = %v, want nil", response)
	}
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_COVERAGE_UNAVAILABLE" {
		t.Fatalf("stale PriceEstimates.Create() error = %v, want FailedPrecondition BILLING_COVERAGE_UNAVAILABLE", err)
	}
}

func TestPricingQueryHidesCatalogItemsThatAreNotVisible(t *testing.T) {
	t.Parallel()

	provider := &billingPricingTestProvider{items: []billing.RateCardItem{{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "1", Unit: billing.RateUnitHour}}}
	computeItems := &billingPricingTestComputeCatalog{err: status.Error(codes.PermissionDenied, "catalog item is hidden")}
	query, _ := newBillingPricingTestServers(t, provider, computeItems, auth.AllTenants)

	_, err := query.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PricingQueriesCreateRequest_builder{
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if status.Code(err) != codes.NotFound {
		t.Fatalf("PricingQueries.Create() error = %v, want hidden catalog item as NotFound", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want no lookup for a hidden catalog item", provider.calls)
	}
}

func TestPriceEstimateFailsWhenBillingIsNotConfigured(t *testing.T) {
	t.Parallel()

	computeItems := &billingPricingTestComputeCatalog{item: billingPricingTestComputeCatalogItem()}
	servers, err := NewBillingPricingServers(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		billingPricingTestTenancy{assignable: auth.AllTenants},
		nil,
		computeItems,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBillingPricingServers() error = %v", err)
	}
	_, err = servers.PriceEstimates.Create(config.TenantIntoContext(context.Background(), "tenant-a"), publicv1.PriceEstimatesCreateRequest_builder{
		Configuration: billingPricingTestConfiguration(),
	}.Build())
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_NOT_CONFIGURED" {
		t.Fatalf("PriceEstimates.Create() error = %v, want BILLING_NOT_CONFIGURED", err)
	}
	if computeItems.calls != 0 {
		t.Fatalf("catalog calls = %d, want no catalog reads when billing is disabled", computeItems.calls)
	}
}

type billingPricingTestTenancy struct {
	assignable collections.Set[string]
}

func (t billingPricingTestTenancy) DetermineAssignableTenants(context.Context) (collections.Set[string], error) {
	return t.assignable, nil
}

func (billingPricingTestTenancy) DetermineDefaultTenant(context.Context) (string, error) {
	return "tenant-a", nil
}

func (billingPricingTestTenancy) DetermineVisibility(context.Context) (*auth.Visibility, error) {
	return auth.TotalVisibility(), nil
}

type billingPricingTestAccounts struct{}

func (billingPricingTestAccounts) GetAssignedAccount(context.Context, string) (billing.AssignedAccount, error) {
	return billing.AssignedAccount{
		ProviderKey:              "m360",
		ProviderAccountReference: "provider-account-1",
		CurrencyCode:             "USD",
		ConnectionState:          "CONNECTED",
	}, nil
}

type billingPricingTestProvider struct {
	items []billing.RateCardItem
	err   error
	calls int
}

func (p *billingPricingTestProvider) ListRateCardItems(ctx context.Context, _ billing.RateCardQuery) ([]billing.RateCardItem, error) {
	p.calls++
	if p.err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, p.err
	}
	return p.items, nil
}

type billingPricingTestComputeCatalog struct {
	item  *publicv1.ComputeInstanceCatalogItem
	err   error
	calls int
}

func (c *billingPricingTestComputeCatalog) Get(context.Context, *publicv1.ComputeInstanceCatalogItemsGetRequest) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{Object: c.item}.Build(), nil
}

type billingPricingTestClusterCatalog struct {
	item  *publicv1.ClusterCatalogItem
	err   error
	calls int
}

func (c *billingPricingTestClusterCatalog) Get(context.Context, *publicv1.ClusterCatalogItemsGetRequest) (*publicv1.ClusterCatalogItemsGetResponse, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return publicv1.ClusterCatalogItemsGetResponse_builder{Object: c.item}.Build(), nil
}

func (c *billingPricingTestClusterCatalog) templatePricingDefaults(context.Context, string) (map[string]string, error) {
	return nil, nil
}

func newBillingPricingTestServers(
	t *testing.T,
	provider *billingPricingTestProvider,
	computeItems *billingPricingTestComputeCatalog,
	assignable collections.Set[string],
) (*PricingQueriesServer, *PriceEstimatesServer) {
	return newBillingPricingTestServersWithCatalogs(t, provider, computeItems, nil, assignable)
}

func newBillingPricingTestServersWithClusterCatalog(
	t *testing.T,
	provider *billingPricingTestProvider,
	clusterItems *billingPricingTestClusterCatalog,
	assignable collections.Set[string],
) (*PricingQueriesServer, *PriceEstimatesServer) {
	return newBillingPricingTestServersWithCatalogs(t, provider, nil, clusterItems, assignable)
}

func newBillingPricingTestServersWithCatalogs(
	t *testing.T,
	provider *billingPricingTestProvider,
	computeItems *billingPricingTestComputeCatalog,
	clusterItems *billingPricingTestClusterCatalog,
	assignable collections.Set[string],
) (*PricingQueriesServer, *PriceEstimatesServer) {
	t.Helper()
	resolver := NewCatalogPricingResolver("m360", provider, billingPricingTestAccounts{})
	servers, err := NewBillingPricingServers(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		billingPricingTestTenancy{assignable: assignable},
		resolver,
		computeItems,
		clusterItems,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBillingPricingServers() error = %v", err)
	}
	return servers.PricingQueries, servers.PriceEstimates
}

func billingPricingTestComputeCatalogItem() *publicv1.ComputeInstanceCatalogItem {
	return publicv1.ComputeInstanceCatalogItem_builder{
		Title: "Small VM",
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
				Locked: publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
			}.Build(),
			InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
				Editable: publicv1.EditableInstanceTypeReferenceField_builder{
					DefaultValue: publicv1.InstanceTypeReference_builder{Name: "small"}.Build(),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

func billingPricingTestConfiguration() *publicv1.BillingPricingConfiguration {
	return publicv1.BillingPricingConfiguration_builder{
		ComputeInstance: publicv1.ComputeInstanceSpec_builder{
			CatalogItem:  publicv1.ComputeInstanceCatalogItemReference_builder{Id: "catalog-vm"}.Build(),
			DiskImage:    publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
			InstanceType: publicv1.InstanceTypeReference_builder{Name: "large"}.Build(),
		}.Build(),
	}.Build()
}

func stringPointer(value string) *string {
	return &value
}

func billingTestInt32(value int32) *int32 {
	return &value
}
