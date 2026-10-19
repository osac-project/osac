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

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type rateCardProviderStub struct {
	query billing.RateCardQuery
	items []billing.RateCardItem
	err   error
	calls int
}

func (s *rateCardProviderStub) ListRateCardItems(_ context.Context, query billing.RateCardQuery) ([]billing.RateCardItem, error) {
	s.query = query
	s.calls++
	return s.items, s.err
}

type assignedBillingAccountStoreStub struct {
	account billing.AssignedAccount
	err     error
}

type catalogPricingInputListStub struct {
	items []*publicv1.CatalogPricingInput
}

func (s *catalogPricingInputListStub) List(_ context.Context, request *publicv1.CatalogPricingInputsListRequest) (*publicv1.CatalogPricingInputsListResponse, error) {
	offset := request.GetOffset()
	limit := request.GetLimit()
	if limit <= 0 {
		limit = int32(len(s.items))
	}
	if offset >= int32(len(s.items)) {
		return publicv1.CatalogPricingInputsListResponse_builder{Total: int32(len(s.items))}.Build(), nil
	}
	end := min(offset+limit, int32(len(s.items)))
	return publicv1.CatalogPricingInputsListResponse_builder{
		Size: end - offset, Total: int32(len(s.items)), Items: s.items[offset:end],
	}.Build(), nil
}

func (s *assignedBillingAccountStoreStub) GetAssignedAccount(context.Context, string) (billing.AssignedAccount, error) {
	return s.account, s.err
}

func TestProviderRateCardItemsListUsesTenantAccountAndPaginates(t *testing.T) {
	provider := &rateCardProviderStub{items: []billing.RateCardItem{
		{Service: "compute_instance", CurrencyCode: "USD", Source: billing.RateCardSourceDefault, Amount: "4.50", Unit: billing.RateUnitHour},
		{Service: "compute_instance", CurrencyCode: "USD", Source: billing.RateCardSourceContract, Amount: "3.00", Unit: billing.RateUnitHour,
			Conditions:         []billing.RateCondition{{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"}},
			CatalogItemMatches: []string{"vm-fedora"}},
	}}
	store := &assignedBillingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account-7", CurrencyCode: "USD", ConnectionState: "2",
	}}
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).SetAccountStore(store).Build()
	if err != nil {
		t.Fatal(err)
	}
	tenantID := "tenant-a"
	service := "compute_instance"
	offset, limit := int32(1), int32(1)
	request := publicv1.ProviderRateCardItemsListRequest_builder{
		TenantId: &tenantID, Service: &service, Offset: &offset, Limit: &limit,
	}.Build()
	response, err := server.List(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if provider.query.AccountReference != "provider-account-7" || provider.query.CurrencyCode != "USD" || provider.query.Service != "compute_instance" {
		t.Fatalf("provider query = %+v, want tenant account, currency, and service", provider.query)
	}
	if response.GetTotal() != 2 || response.GetSize() != 1 || len(response.GetItems()) != 1 {
		t.Fatalf("pagination response = %v, want one of two rate rows", response)
	}
	item := response.GetItems()[0]
	if item.GetSource() != publicv1.BillingRateCardSource_BILLING_RATE_CARD_SOURCE_CONTRACT || item.GetRate().GetAmount() != "3.00" || item.GetRate().GetUnit() != "hour" {
		t.Fatalf("mapped rate item = %v, want contract rate 3.00 per hour", item)
	}
	if len(item.GetResourceConditions()) != 1 || item.GetResourceConditions()[0].GetValue() != "fedora-16" || len(item.GetCatalogItemMatches()) != 1 {
		t.Fatalf("rate match = %v, want provider condition and catalog item reference", item)
	}
}

func TestProviderRateCardItemsListRequiresConnectedAssignment(t *testing.T) {
	provider := &rateCardProviderStub{}
	store := &assignedBillingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account-7", CurrencyCode: "USD", ConnectionState: "PENDING",
	}}
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).SetAccountStore(store).Build()
	if err != nil {
		t.Fatal(err)
	}
	tenantID := "tenant-a"
	_, err = server.List(context.Background(), publicv1.ProviderRateCardItemsListRequest_builder{TenantId: &tenantID}.Build())
	if got := grpcstatus.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("status = %s, want FailedPrecondition", got)
	}
	if provider.calls != 0 {
		t.Fatalf("provider was called %d times for an unconnected assignment", provider.calls)
	}
}

func TestProviderRateCardItemsListDoesNotExposeProviderErrors(t *testing.T) {
	provider := &rateCardProviderStub{err: errors.New("provider error contains private data")}
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.List(context.Background(), &publicv1.ProviderRateCardItemsListRequest{})
	if got := grpcstatus.Code(err); got != codes.Unavailable {
		t.Fatalf("status = %s, want Unavailable", got)
	}
	if err != nil && strings.Contains(err.Error(), "private data") {
		t.Fatalf("provider error leaked to caller: %v", err)
	}
}

func TestProviderRateCardItemsListRejectsCurrencyDifferentFromAssignedAccount(t *testing.T) {
	provider := &rateCardProviderStub{}
	store := &assignedBillingAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account-7", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).SetAccountStore(store).Build()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, currency := "tenant-a", "EUR"
	_, err = server.List(context.Background(), publicv1.ProviderRateCardItemsListRequest_builder{
		TenantId: &tenantID, CurrencyCode: &currency,
	}.Build())
	if got := grpcstatus.Code(err); got != codes.InvalidArgument {
		t.Fatalf("status = %s, want InvalidArgument", got)
	}
	if provider.calls != 0 {
		t.Fatalf("provider was called %d times with a currency different from the assigned account", provider.calls)
	}
}

func TestProviderRateCardItemsListRequiresConfiguredProvider(t *testing.T) {
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.List(context.Background(), &publicv1.ProviderRateCardItemsListRequest{})
	if got := grpcstatus.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("status = %s, want FailedPrecondition", got)
	}
}

func TestProviderRateCardItemsListFillsWildcardsAndCatalogMatches(t *testing.T) {
	provider := &rateCardProviderStub{items: []billing.RateCardItem{{
		Service: "compute_instance", CurrencyCode: "USD", Source: billing.RateCardSourceDefault, Amount: "4.50", Unit: billing.RateUnitHour,
		Conditions: []billing.RateCondition{
			{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
			{Dimension: "boot_disk_size_gib", Operator: billing.RateMatchAny},
		},
	}}}
	source1 := publicv1.CatalogPricingInputSource_builder{Id: "vm-fedora-id", Name: "vm-fedora"}.Build()
	source2 := publicv1.CatalogPricingInputSource_builder{Id: "vm-ubuntu-id", Name: "vm-ubuntu"}.Build()
	input := func(dimension, value string, source *publicv1.CatalogPricingInputSource) *publicv1.CatalogPricingInput {
		return publicv1.CatalogPricingInput_builder{Service: "compute_instance", Dimension: dimension, Value: value, CatalogItems: []*publicv1.CatalogPricingInputSource{source}}.Build()
	}
	catalog := &catalogPricingInputListStub{items: []*publicv1.CatalogPricingInput{
		input("image_ref", "fedora-16", source1), input("boot_disk_size_gib", "20", source1),
		input("storage_tier", "gold", source1), input("instance_type", "small", source1),
		input("image_ref", "ubuntu-24", source2), input("boot_disk_size_gib", "40", source2),
		input("storage_tier", "silver", source2), input("instance_type", "large", source2),
	}}
	server, err := NewProviderRateCardItemsServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).SetCatalogPricingInputs(catalog).Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.List(context.Background(), &publicv1.ProviderRateCardItemsListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	item := response.GetItems()[0]
	conditions := map[string]publicv1.BillingRateMatchOperator{}
	for _, condition := range item.GetResourceConditions() {
		conditions[condition.GetDimension()] = condition.GetOperator()
	}
	for _, dimension := range []string{"image_ref", "boot_disk_size_gib", "storage_tier", "instance_type"} {
		if _, ok := conditions[dimension]; !ok {
			t.Fatalf("rate-card row omitted catalog dimension %q: %v", dimension, item.GetResourceConditions())
		}
	}
	if conditions["storage_tier"] != publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY || conditions["instance_type"] != publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY {
		t.Fatalf("unconstrained catalog dimensions were not represented as wildcards: %v", item.GetResourceConditions())
	}
	if len(item.GetCatalogItemMatches()) != 1 || item.GetCatalogItemMatches()[0] != "vm-fedora" {
		t.Fatalf("catalog matches = %v, want only vm-fedora", item.GetCatalogItemMatches())
	}
}
