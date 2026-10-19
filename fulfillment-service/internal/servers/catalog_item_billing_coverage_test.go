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
	"strings"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCatalogItemBillingCoverageChecksPublishedTenantCatalogs(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4.50", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "small"}},
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	requests := []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance,
		Dimensions: map[string]string{
			"instance_type": "small",
		},
		Supported: true,
	}}

	if err := checkCatalogItemBillingCoverage(context.Background(), resolver, "tenant-a", true, requests); err != nil {
		t.Fatalf("checkCatalogItemBillingCoverage() error = %v, want matched current coverage", err)
	}
	if store.tenant != "tenant-a" || provider.calls != 1 {
		t.Fatalf("coverage lookup used tenant %q and %d provider calls, want tenant-a and 1", store.tenant, provider.calls)
	}

	provider.items = []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4.50", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"}},
	}}
	err := checkCatalogItemBillingCoverage(context.Background(), resolver, "tenant-a", true, requests)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "BILLING_COVERAGE_GAP") {
		t.Fatalf("checkCatalogItemBillingCoverage() error = %v, want a current coverage gap", err)
	}
}

func TestCatalogItemBillingCoverageSkipsUnpublishedAndSharedCatalogs(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	request := CatalogPricingRequest{Service: billing.ServiceComputeInstance, Supported: true}

	for _, test := range []struct {
		name      string
		tenant    string
		published bool
	}{
		{name: "draft tenant catalog", tenant: "tenant-a", published: false},
		{name: "shared catalog", tenant: auth.SharedTenant, published: true},
		{name: "system catalog", tenant: auth.SystemTenant, published: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := checkCatalogItemBillingCoverage(context.Background(), resolver, test.tenant, test.published, []CatalogPricingRequest{request}); err != nil {
				t.Fatalf("checkCatalogItemBillingCoverage() error = %v, want no gate", err)
			}
		})
	}
	if store.calls != 0 || provider.calls != 0 {
		t.Fatalf("account/provider calls = (%d, %d), want no lookup for drafts or shared catalogs", store.calls, provider.calls)
	}
}

func TestCatalogItemBillingCoverageBlocksPublishDuringProviderOutage(t *testing.T) {
	for _, test := range []struct {
		name        string
		configure   func(*catalogPricingRateCardProviderStub)
		openCircuit bool
		wantCode    codes.Code
	}{
		{
			name: "rate-card provider outage",
			configure: func(provider *catalogPricingRateCardProviderStub) {
				provider.err = errors.Join(billing.ErrProviderUnavailable, errors.New("provider unavailable"))
			},
			wantCode: codes.Unavailable,
		},
		{
			name: "account status timeout",
			configure: func(provider *catalogPricingRateCardProviderStub) {
				provider.accountErr = context.DeadlineExceeded
			},
			wantCode: codes.DeadlineExceeded,
		},
		{
			name:        "open pricing circuit",
			openCircuit: true,
			wantCode:    codes.Unavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &catalogPricingRateCardProviderStub{}
			if test.configure != nil {
				test.configure(provider)
			}
			resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
				ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
			}})
			if test.openCircuit {
				for i := 0; i < catalogPricingCircuitFailureThreshold; i++ {
					resolver.circuit.recordFailure(resolver.now(), false)
				}
			}

			coverageErr := checkCatalogItemBillingCoverage(context.Background(), resolver, "tenant-a", true, []CatalogPricingRequest{{
				Service: billing.ServiceComputeInstance, Supported: true,
			}})
			if !errors.Is(coverageErr, ErrBillingCoverageUnavailable) || status.Code(coverageErr) != test.wantCode {
				t.Fatalf("checkCatalogItemBillingCoverage() error = %v, want provider unavailability to block publish", coverageErr)
			}
			if createErr := coverageErrorForCreate(context.Background(), nil, coverageErr); createErr != nil {
				t.Fatalf("coverageErrorForCreate() error = %v, want the same provider failure to allow launch", createErr)
			}
		})
	}
}
