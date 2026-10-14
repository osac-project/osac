/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCatalogPricingSeparatesBaseAndAdditionalDiskAmounts(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{
		{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "10", Unit: billing.RateUnitHour,
			Conditions: []billing.RateCondition{{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"}}},
		{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3", Unit: billing.RateUnitHour,
			Conditions: []billing.RateCondition{{Dimension: "additional_disk_size_gib", Operator: billing.RateMatchEquals, Value: "50"}}},
		{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "5", Unit: billing.RateUnitHour,
			Conditions: []billing.RateCondition{{Dimension: "additional_disk_size_gib", Operator: billing.RateMatchEquals, Value: "100"}}},
	}}
	request := CatalogPricingRequest{Service: billing.ServiceComputeInstance, Dimensions: map[string]string{"instance_type": "large"}, Supported: true,
		Components: []billing.CatalogPricingComponentRequest{
			{Dimensions: map[string]string{"additional_disk_size_gib": "50"}},
			{Dimensions: map[string]string{"additional_disk_size_gib": "100"}},
		}}
	got := NewCatalogPricingResolver("m360", provider, store).Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{request})
	if len(got) != 1 || got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT || len(got[0].GetComponents()) != 3 {
		t.Fatalf("catalog pricing = %v, want base and two priced disks", got)
	}
	if !got[0].HasAsOf() {
		t.Fatal("current catalog pricing has no as_of timestamp")
	}
	for i, want := range []string{"10", "3", "5"} {
		if got[0].GetComponents()[i].GetAmount() != want {
			t.Fatalf("component %d amount = %q, want %q", i, got[0].GetComponents()[i].GetAmount(), want)
		}
	}
}

func TestCatalogPricingAsOfIsUnsetOnStaleFallback(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4.50", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	resolver.now = func() time.Time { return now }
	request := CatalogPricingRequest{Service: billing.ServiceComputeInstance, Supported: true}

	current := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{request})[0]
	if !current.HasAsOf() || !current.GetAsOf().AsTime().Equal(now) {
		t.Fatalf("current as_of = %v, want %v", current.GetAsOf(), now)
	}

	now = now.Add(catalogPricingCacheTTL + time.Second)
	provider.err = errors.New("provider unavailable")
	stale := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{request})[0]
	if stale.GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_STALE {
		t.Fatalf("stale status = %v, want STALE", stale.GetStatus())
	}
	if len(stale.GetComponents()) != 1 || stale.GetComponents()[0].GetAmount() != "4.50" {
		t.Fatalf("stale components = %v, want retained amount", stale.GetComponents())
	}
	if stale.HasAsOf() {
		t.Fatalf("stale pricing as_of = %v, want unset", stale.GetAsOf())
	}
}

func TestCatalogPricingDoesNotRepeatClusterTemplateRateForNodeSets(t *testing.T) {
	request := CatalogPricingRequest{Service: billing.ServiceCluster, Dimensions: map[string]string{"cluster_template": "standard"}, Supported: true,
		Components: []billing.CatalogPricingComponentRequest{
			{Dimensions: map[string]string{"cluster_template": "standard", "baremetal_instance_type": "cpu", "node_count": "3"}},
			{Dimensions: map[string]string{"cluster_template": "standard", "baremetal_instance_type": "gpu", "node_count": "2"}},
		}}
	components := catalogPricingComponents(request)
	if len(components) != 3 || components[0].Dimensions["node_count"] != "" {
		t.Fatalf("cluster pricing components = %+v, want one template and two node sets", components)
	}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceCluster, CurrencyCode: "USD", Amount: "4", Unit: billing.RateUnitMonth,
		Conditions: []billing.RateCondition{{Dimension: "cluster_template", Operator: billing.RateMatchEquals, Value: "standard"}},
	}}}
	for _, node := range components[1:] {
		if _, err := billing.ResolveRateCardItem(provider.items, request.Service, "USD", node.Dimensions); err == nil {
			t.Fatal("template-only rate unexpectedly covers a node set")
		}
	}
}

func TestClearCatalogItemPricing(t *testing.T) {
	pricing := privatev1.CatalogItemPricing_builder{
		Status: privatev1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT,
	}.Build()
	computeItem := privatev1.ComputeInstanceCatalogItem_builder{Pricing: pricing}.Build()
	clusterItem := privatev1.ClusterCatalogItem_builder{Pricing: pricing}.Build()
	bareMetalItem := privatev1.BareMetalInstanceCatalogItem_builder{Pricing: pricing}.Build()
	tests := []struct {
		name       string
		item       interface{ ClearPricing() }
		getPricing func() *privatev1.CatalogItemPricing
	}{
		{
			name:       "compute instance",
			item:       computeItem,
			getPricing: computeItem.GetPricing,
		},
		{
			name:       "cluster",
			item:       clusterItem,
			getPricing: clusterItem.GetPricing,
		},
		{
			name:       "bare metal instance",
			item:       bareMetalItem,
			getPricing: bareMetalItem.GetPricing,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearCatalogItemPricing(test.item)
			if got := test.getPricing(); got != nil {
				t.Fatalf("pricing = %v, want nil", got)
			}
		})
	}
}
