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
	"strings"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type catalogPricingRateCardOnlyProvider struct{}

func (catalogPricingRateCardOnlyProvider) ListRateCardItems(context.Context, billing.RateCardQuery) ([]billing.RateCardItem, error) {
	return []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
	}}, nil
}

func TestCatalogPricingResolverCheckCoverageUsesFreshProviderRates(t *testing.T) {
	store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"}},
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, store)
	request := CatalogPricingRequest{Service: billing.ServiceComputeInstance, Dimensions: map[string]string{"image_ref": "fedora-16"}, Supported: true}

	if got := resolver.Resolve(context.Background(), "tenant-a", []CatalogPricingRequest{request}); got[0].GetComponents()[0].GetAmount() != "3.00" {
		t.Fatalf("Resolve() = %v, want cached current rate", got)
	}
	if err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{request}); err != nil {
		t.Fatalf("CheckCoverage() error = %v, want current rate coverage", err)
	}
	provider.items = []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4.00", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-14"}},
	}}

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{request})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "BILLING_COVERAGE_GAP") {
		t.Fatalf("CheckCoverage() error = %v, want failed precondition for missing current coverage", err)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want fresh provider lookups despite catalog cache", provider.calls)
	}
}

func TestCatalogPricingCoverageRequiresEachAdditionalDiskRate(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{items: []billing.RateCardItem{{
		Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "4", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{{Dimension: "instance_type", Operator: billing.RateMatchEquals, Value: "large"}},
	}}}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})
	request := CatalogPricingRequest{Service: billing.ServiceComputeInstance, Dimensions: map[string]string{"instance_type": "large"}, Supported: true,
		Components: []billing.CatalogPricingComponentRequest{
			{Dimensions: map[string]string{"additional_disk_size_gib": "50"}},
			{Dimensions: map[string]string{"additional_disk_size_gib": "100"}},
		}}
	components := catalogPricingComponents(request)
	if len(components) != 3 || components[0].Dimensions["instance_type"] != "large" {
		t.Fatalf("components = %+v, want one base and two disks", components)
	}
	if _, err := billing.ResolveRateCardItem(provider.items, request.Service, "USD", components[0].Dimensions); err != nil {
		t.Fatalf("base VM coverage error = %v", err)
	}
	for _, disk := range components[1:] {
		if _, err := billing.ResolveRateCardItem(provider.items, request.Service, "USD", disk.Dimensions); !errors.Is(err, billing.ErrRateCoverageGap) {
			t.Fatalf("disk coverage error = %v, want gap", err)
		}
	}
	if err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{request}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CheckCoverage() error = %v, want coverage gap", err)
	}
}

func TestCatalogPricingResolverCheckCoverageIdentifiesProviderFailure(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{err: errors.Join(billing.ErrProviderUnavailable, errors.New("private provider error"))}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance, Supported: true,
	}})
	if status.Code(err) != codes.Unavailable || strings.Contains(status.Convert(err).Message(), "private provider error") {
		t.Fatalf("CheckCoverage() error = %v, want sanitized unavailable error", err)
	}
	if !errors.Is(err, ErrBillingCoverageUnavailable) {
		t.Fatalf("CheckCoverage() error = %v, want provider-unavailable classification", err)
	}
	if provider.calls != catalogPricingRetryCount+1 {
		t.Fatalf("provider calls = %d, want %d retry attempts", provider.calls, catalogPricingRetryCount+1)
	}
}

func TestCoverageErrorForCreate(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "rate provider outage", err: coverageProviderError(codes.Unavailable, "BILLING_COVERAGE_UNAVAILABLE")},
		{name: "provider timeout", err: coverageProviderError(codes.DeadlineExceeded, "BILLING_COVERAGE_TIMEOUT")},
		{name: "coverage gap", err: status.Error(codes.FailedPrecondition, "BILLING_COVERAGE_GAP"), want: status.Error(codes.FailedPrecondition, "BILLING_COVERAGE_GAP")},
		{name: "no assignment", err: status.Error(codes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE"), want: status.Error(codes.FailedPrecondition, "BILLING_ACCOUNT_UNAVAILABLE")},
		{name: "account lookup failure", err: status.Error(codes.Internal, "BILLING_ACCOUNT_LOOKUP_FAILED"), want: status.Error(codes.Internal, "BILLING_ACCOUNT_LOOKUP_FAILED")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := coverageErrorForCreate(context.Background(), nil, test.err)
			if status.Code(got) != status.Code(test.want) || status.Convert(got).Message() != status.Convert(test.want).Message() {
				t.Fatalf("coverageErrorForCreate(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestCatalogPricingResolverCheckCoverageRequiresConnectedAssignment(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{err: billing.ErrTenantBillingAssignmentNotFound})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{Service: billing.ServiceComputeInstance, Supported: true}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CheckCoverage() error = %v, want failed precondition without a linked account", err)
	}
	if errors.Is(err, ErrBillingCoverageUnavailable) {
		t.Fatalf("CheckCoverage() error = %v, missing assignment must block launch", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want no provider calls without assignment", provider.calls)
	}
}

func TestCatalogPricingResolverCheckCoverageRejectsInvalidProviderData(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{err: errors.New("invalid provider rate-card response")}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{Service: billing.ServiceComputeInstance, Supported: true}})
	if status.Code(err) != codes.Internal || errors.Is(err, ErrBillingCoverageUnavailable) {
		t.Fatalf("CheckCoverage() error = %v, invalid provider data must block launch", err)
	}
}

func TestCatalogPricingResolverMalformedRatesDoNotOpenCircuit(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{err: errors.New("invalid provider rate-card response")}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})
	request := []CatalogPricingRequest{{Service: billing.ServiceComputeInstance, Supported: true}}

	for i := 0; i <= catalogPricingCircuitFailureThreshold; i++ {
		err := resolver.CheckCoverage(context.Background(), "tenant-a", request)
		if status.Code(err) != codes.Internal || errors.Is(err, ErrBillingCoverageUnavailable) {
			t.Fatalf("CheckCoverage() after %d malformed responses = %v, want blocking provider-data error", i+1, err)
		}
	}
	if allowed, _ := resolver.circuit.allow(resolver.now()); !allowed {
		t.Fatal("malformed responses opened the provider outage circuit")
	}
}

func TestCatalogPricingResolverCheckCoverageRequiresCurrentActiveAccount(t *testing.T) {
	for _, test := range []struct {
		name  string
		state billing.AccountState
	}{
		{name: "inactive", state: billing.AccountStateInactive},
		{name: "unknown", state: billing.AccountStateUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
				ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
			}}
			provider := &catalogPricingRateCardProviderStub{
				items:   []billing.RateCardItem{{Service: billing.ServiceComputeInstance, CurrencyCode: "USD", Amount: "3.00", Unit: billing.RateUnitHour}},
				account: billing.Account{Reference: "native-account", State: test.state}, accountSet: true,
			}
			resolver := NewCatalogPricingResolver("m360", provider, store)

			err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{
				Service: billing.ServiceComputeInstance, Supported: true,
			}})
			if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_ACCOUNT_UNAVAILABLE" {
				t.Fatalf("CheckCoverage() error = %v, want BILLING_ACCOUNT_UNAVAILABLE", err)
			}
			if provider.accountCalls != 1 || provider.calls != 0 {
				t.Fatalf("account/rate-card calls = (%d, %d), want current account check and no rate-card lookup", provider.accountCalls, provider.calls)
			}
		})
	}
}

func TestCatalogPricingResolverCheckCoverageFailsClosedWhenAccountStatusCannotBeRead(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{
		accountErr: errors.Join(billing.ErrProviderUnavailable, errors.New("provider account details unavailable")),
	}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance, Supported: true,
	}})
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "BILLING_ACCOUNT_STATUS_UNAVAILABLE" {
		t.Fatalf("CheckCoverage() error = %v, want sanitized BILLING_ACCOUNT_STATUS_UNAVAILABLE", err)
	}
	if strings.Contains(status.Convert(err).Message(), "provider account details unavailable") || provider.calls != 0 {
		t.Fatalf("CheckCoverage() leaked account status error or queried rates: %v, calls=%d", err, provider.calls)
	}
}

func TestCatalogPricingResolverCheckCoverageRequiresAccountStatusCapability(t *testing.T) {
	resolver := NewCatalogPricingResolver("m360", catalogPricingRateCardOnlyProvider{}, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service: billing.ServiceComputeInstance, Supported: true,
	}})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "BILLING_ACCOUNT_STATUS_UNAVAILABLE" {
		t.Fatalf("CheckCoverage() error = %v, want fail-closed status when provider lacks account status capability", err)
	}
}

func TestCatalogPricingResolverCheckCoverageRejectsUnsupportedConfiguration(t *testing.T) {
	provider := &catalogPricingRateCardProviderStub{}
	resolver := NewCatalogPricingResolver("m360", provider, &catalogPricingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}})

	err := resolver.CheckCoverage(context.Background(), "tenant-a", []CatalogPricingRequest{{
		Service: billing.ServiceCluster, Supported: false,
	}})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "BILLING_CONFIGURATION_UNSUPPORTED") {
		t.Fatalf("CheckCoverage() error = %v, want failed precondition for unsupported configuration", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want no provider call for unsupported configuration", provider.calls)
	}
}
