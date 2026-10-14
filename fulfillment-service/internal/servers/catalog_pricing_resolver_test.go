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
	"fmt"
	"testing"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"github.com/prometheus/client_golang/prometheus"
)

type catalogPricingAccountStoreStub struct {
	account billing.AssignedAccount
	err     error
	tenant  string
	calls   int
}

func (s *catalogPricingAccountStoreStub) GetAssignedAccount(_ context.Context, tenantID string) (billing.AssignedAccount, error) {
	s.tenant = tenantID
	s.calls++
	return s.account, s.err
}

type catalogPricingRateCardProviderStub struct {
	items        []billing.RateCardItem
	err          error
	failures     int
	failureErr   error
	calls        int
	queries      []billing.RateCardQuery
	account      billing.Account
	accountErr   error
	accountSet   bool
	accountCalls int
}

func (s *catalogPricingRateCardProviderStub) ListRateCardItems(_ context.Context, query billing.RateCardQuery) ([]billing.RateCardItem, error) {
	s.calls++
	s.queries = append(s.queries, query)
	if s.calls <= s.failures {
		return nil, s.failureErr
	}
	return s.items, s.err
}

func (s *catalogPricingRateCardProviderStub) GetAccount(_ context.Context, reference string) (billing.Account, error) {
	s.accountCalls++
	if s.accountErr != nil {
		return billing.Account{}, s.accountErr
	}
	if !s.accountSet {
		return billing.Account{Reference: reference, State: billing.AccountStateActive}, nil
	}
	if s.account.Reference == "" {
		s.account.Reference = reference
	}
	return s.account, nil
}

func TestCatalogPricingResolverReturnsCurrentPrice(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey:              "m360",
		ProviderAccountReference: "native-account",
		CurrencyCode:             "USD",
		ConnectionState:          "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{
		{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4.50", Unit: billing.RateUnitHour},
		{
			Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
			Conditions: []billing.RateCondition{{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"}},
		},
	}}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:      billing.ServiceComputeInstance,
		Dimensions:   map[string]string{"image_ref": "fedora-16"},
		Description:  "Compute instance",
		PriceMayVary: true,
		Supported:    true,
	}})

	if len(got) != 1 || got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT {
		t.Fatalf("Resolve() = %v, want one current pricing result", got)
	}
	if got[0].GetCurrencyCode() != "USD" || len(got[0].GetComponents()) != 1 || got[0].GetComponents()[0].GetAmount() != "3.00" {
		t.Fatalf("Resolve() pricing = %v, want USD 3.00", got[0])
	}
	if !got[0].GetPriceMayVary() || provider.calls != 1 || store.tenant != "tenant-a" || store.calls != 1 {
		t.Fatalf("Resolve() price_may_vary=%t account calls=%d tenant=%q provider calls=%d, want true and one tenant-scoped lookup", got[0].GetPriceMayVary(), store.calls, store.tenant, provider.calls)
	}
	if len(provider.queries) != 1 || provider.queries[0] != (billing.RateCardQuery{
		AccountReference: "native-account", Service: billing.ServiceComputeInstance, CurrencyCode: "USD",
	}) {
		t.Fatalf("rate-card queries = %+v, want tenant account/service/currency", provider.queries)
	}
	resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:   billing.ServiceComputeInstance,
		Supported: true,
	}})
	if provider.calls != 1 {
		t.Fatalf("provider calls after cached Resolve() = %d, want 1", provider.calls)
	}
}

func TestCatalogPricingResolverReturnsUnavailableWithoutConnectedAssignment(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{
		account: billing.AssignedAccount{CurrencyCode: "USD", ConnectionState: "PENDING"},
	})

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{Service: billing.ServiceComputeInstance}})
	if got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_UNAVAILABLE || len(got[0].GetComponents()) != 0 {
		t.Fatalf("Resolve() = %v, want unavailable without components", got[0])
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestCatalogPricingResolverAcceptsStoredConnectedEnumValue(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "2",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:   billing.ServiceComputeInstance,
		Supported: true,
	}})
	if got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT {
		t.Fatalf("Resolve() status = %v, want current for stored CONNECTED enum value", got[0].GetStatus())
	}
}

func TestCatalogPricingResolverUsesExpiredCacheAsStaleOnRefreshFailure(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	currentTime := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	resolver.now = func() time.Time { return currentTime }
	request := []CatalogPricingRequest{{
		Service:     billing.ServiceComputeInstance,
		Dimensions:  map[string]string{},
		Description: "Compute instance",
		Supported:   true,
	}}

	first := resolver.Resolve(context.Background(), "tenant-a", request)
	if first[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT {
		t.Fatalf("first Resolve() status = %v, want current", first[0].GetStatus())
	}

	currentTime = currentTime.Add(catalogPricingCacheTTL + time.Second)
	provider.err = errors.New("provider unavailable")
	second := resolver.Resolve(context.Background(), "tenant-a", request)
	if second[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_STALE || len(second[0].GetComponents()) != 1 {
		t.Fatalf("second Resolve() = %v, want stale pricing with a component", second[0])
	}
}

func TestCatalogPricingResolverBoundsCacheByEvictingOldestEntry(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, nil)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	resolver.now = func() time.Time { return now }

	var oldestKey catalogRateCardKey
	var newestKey catalogRateCardKey
	for i := 0; i < catalogPricingCacheMaxEntries+1; i++ {
		now = now.Add(time.Second)
		account := billing.AssignedAccount{
			ProviderAccountReference: fmt.Sprintf("account-%d", i),
			CurrencyCode:             "USD",
		}
		key := catalogRateCardKey{
			providerKey: "m360",
			account:     account.ProviderAccountReference,
			currency:    account.CurrencyCode,
			service:     billing.ServiceComputeInstance,
		}
		if i == 0 {
			oldestKey = key
		}
		newestKey = key
		if items, stale, _ := resolver.getCatalogRateCardItems(context.Background(), account, billing.ServiceComputeInstance); len(items) == 0 || stale {
			t.Fatalf("cache lookup %d = (%v, stale=%t), want fresh rate card", i, items, stale)
		}
	}

	if got := len(resolver.cache); got != catalogPricingCacheMaxEntries {
		t.Fatalf("catalog rate-card cache size = %d, want limit %d", got, catalogPricingCacheMaxEntries)
	}
	if _, exists := resolver.cache[oldestKey]; exists {
		t.Fatalf("oldest cache key %q was retained after reaching the limit", oldestKey.account)
	}
	if _, exists := resolver.cache[newestKey]; !exists {
		t.Fatalf("newest cache key %q was evicted", newestKey.account)
	}
}

func TestCatalogPricingResolverReturnsUnavailableForCoverageGap(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-14"}},
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:    billing.ServiceComputeInstance,
		Dimensions: map[string]string{"image_ref": "fedora-16"},
		Supported:  true,
	}})
	if got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_UNAVAILABLE || len(got[0].GetComponents()) != 0 {
		t.Fatalf("Resolve() = %v, want unavailable without components", got[0])
	}
}

func TestCatalogPricingCanBeBypassedForInternalCatalogInventoryReads(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got, err := resolveCatalogPricing(
		withoutCatalogPricing(context.Background()),
		nil,
		nil,
		resolver,
		"",
		[]CatalogPricingRequest{{Service: billing.ServiceComputeInstance, Supported: true}},
	)
	if err != nil || got != nil || store.calls != 0 || provider.calls != 0 {
		t.Fatalf("internal inventory lookup got=%v account calls=%d provider calls=%d, want bypassed pricing", got, store.calls, provider.calls)
	}
}

func TestResolveCatalogPricingUsesTenantContextInsteadOfRequestedTenant(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "tenant-a-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	tenancy := billingPricingTestTenancy{assignable: collections.NewSet("tenant-a", "tenant-b")}
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	got, err := resolveCatalogPricing(ctx, nil, tenancy, resolver, "tenant-b", []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance,
	}})
	if err != nil {
		t.Fatalf("resolveCatalogPricing() error = %v", err)
	}
	if len(got) != 1 || got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT {
		t.Fatalf("resolveCatalogPricing() = %v, want one current price", got)
	}
	if store.tenant != "tenant-a" {
		t.Fatalf("account lookup tenant = %q, want active tenant context %q", store.tenant, "tenant-a")
	}
}

func TestResolveCatalogPricingReturnsUnavailableWithoutTenantContext(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "tenant-a-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	tenancy := billingPricingTestTenancy{assignable: collections.NewSet("tenant-a", "tenant-b")}

	got, err := resolveCatalogPricing(context.Background(), nil, tenancy, resolver, "tenant-b", []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance, PriceMayVary: true,
	}})
	if err != nil {
		t.Fatalf("resolveCatalogPricing() error = %v", err)
	}
	if len(got) != 1 || got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_UNAVAILABLE || len(got[0].GetComponents()) != 0 || !got[0].GetPriceMayVary() {
		t.Fatalf("resolveCatalogPricing() = %v, want unavailable pricing without components and with price_may_vary", got)
	}
	if store.calls != 0 || provider.calls != 0 || provider.accountCalls != 0 {
		t.Fatalf("pricing lookup calls: account=%d rate_card=%d provider_account=%d, want none without tenant context", store.calls, provider.calls, provider.accountCalls)
	}
}

func TestCatalogPricingResolverRetriesProviderRead(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{
		items:      []billing.RateCardItem{{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour}},
		failures:   2,
		failureErr: errors.New("transient provider failure"),
	}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:   billing.ServiceComputeInstance,
		Supported: true,
	}})
	if got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_CURRENT || provider.calls != 3 {
		t.Fatalf("Resolve() status=%v provider calls=%d, want current after 2 retries", got[0].GetStatus(), provider.calls)
	}
}

func TestCatalogPricingResolverStopsAfterThreeRetries(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{
		failures:   catalogPricingRetryCount + 1,
		failureErr: errors.New("provider unavailable"),
	}
	resolver := NewCatalogPricingResolver("m360", provider, store)

	got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:   billing.ServiceComputeInstance,
		Supported: true,
	}})
	if got[0].GetStatus() != publicv1.CatalogPricingStatus_CATALOG_PRICING_STATUS_UNAVAILABLE || provider.calls != catalogPricingRetryCount+1 {
		t.Fatalf("Resolve() status=%v provider calls=%d, want unavailable after 3 retries", got[0].GetStatus(), provider.calls)
	}
}

func TestCatalogPricingResolverRecordsLowCardinalityMetrics(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}}
	registry := prometheus.NewRegistry()
	resolver := NewCatalogPricingResolver("m360", provider, store, registry)
	resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service:   billing.ServiceComputeInstance,
		Supported: true,
	}})

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, family := range families {
		seen[family.GetName()] = true
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "tenant" || label.GetName() == "account" || label.GetName() == "resource" {
					t.Fatalf("metric %q has high-cardinality label %q", family.GetName(), label.GetName())
				}
			}
		}
	}
	for _, name := range []string{
		"osac_billing_provider_pricing_lookups_total",
		"osac_billing_provider_pricing_lookup_duration_seconds",
		"osac_billing_catalog_pricing_results_total",
	} {
		if !seen[name] {
			t.Errorf("metric %q was not registered", name)
		}
	}
}
