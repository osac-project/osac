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
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type billingProviderStub struct {
	status    billing.ProviderStatus
	statusErr error
	accounts  []billing.Account
	listErr   error
	account   billing.Account
}

func (p *billingProviderStub) GetStatus(context.Context) (billing.ProviderStatus, error) {
	return p.status, p.statusErr
}

func (p *billingProviderStub) ListAccounts(context.Context) ([]billing.Account, error) {
	return p.accounts, p.listErr
}

func (p *billingProviderStub) GetAccount(context.Context, string) (billing.Account, error) {
	return p.account, nil
}

func (*billingProviderStub) UpdateAccountExternalID(context.Context, string, string, string) error {
	return nil
}

type billingAccountStoreStub struct {
	accounts []*privatev1.BillingAccount
	total    int32
}

func (*billingAccountStoreStub) SyncAccounts(context.Context, string, []billing.Account, time.Time) ([]*privatev1.BillingAccount, error) {
	return nil, nil
}

func (s *billingAccountStoreStub) ListAccounts(context.Context, string, int32, int32) ([]*privatev1.BillingAccount, int32, error) {
	return s.accounts, s.total, nil
}

func (*billingAccountStoreStub) GetAccount(context.Context, string) (*privatev1.BillingAccount, error) {
	return nil, billing.ErrAccountNotFound
}

func newBillingTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBillingProviderStatusReportsNotConfigured(t *testing.T) {
	server, err := NewBillingProviderStatusesServer().SetLogger(newBillingTestLogger()).Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Get(context.Background(), &publicv1.BillingProviderStatusesGetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.GetObject().GetState(); got != publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED {
		t.Fatalf("state = %v, want NOT_CONFIGURED", got)
	}
	if len(response.GetObject().GetSupportedCapabilities()) != 0 {
		t.Fatalf("unconfigured provider advertised capabilities: %v", response.GetObject().GetSupportedCapabilities())
	}
	if response.GetObject().GetLastSuccessfulCheck() != nil {
		t.Fatal("unconfigured provider returned a successful health-check time")
	}
	if got := response.GetObject().GetUsageDeliveryState(); got != publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN {
		t.Fatalf("unconfigured provider usage delivery state = %v, want UNKNOWN", got)
	}
}

func TestBillingProviderStatusExposesProviderAdapterCapabilities(t *testing.T) {
	provider := &billingProviderStub{status: billing.ProviderStatus{
		State:              billing.ProviderStateAvailable,
		UsageDeliveryState: billing.UsageDeliveryStateUnknown,
		Capabilities: []billing.ProviderCapability{
			billing.ProviderCapabilityPricing,
			billing.ProviderCapabilityCosts,
			billing.ProviderCapabilityInvoices,
			"FUTURE_CAPABILITY",
		},
	}}
	server, err := NewBillingProviderStatusesServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Get(context.Background(), &publicv1.BillingProviderStatusesGetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := response.GetObject().GetSupportedCapabilities()
	want := []publicv1.BillingProviderCapability{
		publicv1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_PRICING,
		publicv1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_COSTS,
		publicv1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_INVOICES,
	}
	if len(capabilities) != len(want) {
		t.Fatalf("capabilities = %v, want %v", capabilities, want)
	}
	if got := response.GetObject().GetUsageDeliveryState(); got != publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN {
		t.Fatalf("successful provider API check implied usage delivery state %v, want UNKNOWN", got)
	}
	for i := range want {
		if capabilities[i] != want[i] {
			t.Fatalf("capabilities = %v, want %v", capabilities, want)
		}
	}
}

func TestBillingProviderStatusRetainsLastSuccessfulCheckAfterFailure(t *testing.T) {
	provider := &billingProviderStub{status: billing.ProviderStatus{State: billing.ProviderStateAvailable}}
	server, err := NewBillingProviderStatusesServer().SetLogger(newBillingTestLogger()).SetProvider("m360", provider).Build()
	if err != nil {
		t.Fatal(err)
	}
	first, err := server.Get(context.Background(), &publicv1.BillingProviderStatusesGetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	firstCheck := first.GetObject().GetLastSuccessfulCheck()
	if firstCheck == nil {
		t.Fatal("successful provider check did not return last_successful_check")
	}
	provider.statusErr = errors.New("provider response details must not escape")
	second, err := server.Get(context.Background(), &publicv1.BillingProviderStatusesGetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := second.GetObject().GetState(); got != publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE {
		t.Fatalf("state after failed check = %v, want UNAVAILABLE", got)
	}
	secondCheck := second.GetObject().GetLastSuccessfulCheck()
	if !proto.Equal(firstCheck, secondCheck) {
		t.Fatalf("last_successful_check changed after failed check: first=%v second=%v", firstCheck, secondCheck)
	}
}

func TestBillingAssignmentsBuildWithoutDeleteRPC(t *testing.T) {
	logger := newBillingTestLogger()
	tenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := auth.NewSystemAttributionLogic().Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).
		SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
	if err != nil {
		t.Fatal(err)
	}
	if err = server.generic.Delete(context.Background(), nil, nil); grpcstatus.Code(err) != codes.Unimplemented {
		t.Fatalf("Delete error = %v, want Unimplemented", err)
	}
}

func TestBillingAccountsRequireConfiguredProvider(t *testing.T) {
	server, err := NewPrivateBillingAccountsServer().
		SetLogger(newBillingTestLogger()).
		SetStore(&billingAccountStoreStub{}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.List(context.Background(), &privatev1.BillingAccountsListRequest{})
	if got := grpcstatus.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("status = %v, want FAILED_PRECONDITION; err=%v", got, err)
	}
	if got := grpcstatus.Convert(err).Message(); got != "BILLING_NOT_CONFIGURED" {
		t.Fatalf("message = %q, want BILLING_NOT_CONFIGURED", got)
	}
}

func TestBillingAssignmentCreateRequiresConfiguredProvider(t *testing.T) {
	logger := newBillingTestLogger()
	tenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
	if err != nil {
		t.Fatal(err)
	}
	attribution, err := auth.NewSystemAttributionLogic().Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).
		SetAttributionLogic(attribution).SetTenancyLogic(tenancy).
		SetAccountStore(&billingAccountStoreStub{}).Build()
	if err != nil {
		t.Fatal(err)
	}
	request := privatev1.TenantBillingAssignmentsCreateRequest_builder{
		Object: privatev1.TenantBillingAssignment_builder{
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				TenantId:         "tenant-1",
				BillingAccountId: "account-1",
			}.Build(),
		}.Build(),
	}.Build()
	_, err = server.Create(context.Background(), request)
	if got := grpcstatus.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("status = %v, want FAILED_PRECONDITION; err=%v", got, err)
	}
	if got := grpcstatus.Convert(err).Message(); got != "BILLING_NOT_CONFIGURED" {
		t.Fatalf("message = %q, want BILLING_NOT_CONFIGURED", got)
	}
}

func TestBillingAccountDiscoveryHidesProviderReferenceFromPublicResponse(t *testing.T) {
	provider := &billingProviderStub{
		accounts: []billing.Account{{
			Reference:   "provider-secret-id",
			DisplayName: "Acme",
			State:       billing.AccountStateActive,
		}},
	}
	store := &billingAccountStoreStub{
		accounts: []*privatev1.BillingAccount{
			privatev1.BillingAccount_builder{
				Id:                       "osac-account-id",
				ProviderKey:              "m360",
				ProviderAccountReference: "provider-secret-id",
				DisplayName:              "Acme",
				State:                    privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
			}.Build(),
		},
		total: 1,
	}
	server, err := NewBillingAccountsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetStore(store).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.List(context.Background(), &publicv1.BillingAccountsListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.GetItems()[0].GetId(); got != "osac-account-id" {
		t.Fatalf("account ID = %q, want opaque OSAC ID", got)
	}
	if got := response.GetItems()[0].String(); got == "" || strings.Contains(got, "provider-secret-id") {
		t.Fatalf("public account response unexpectedly exposes provider reference: %s", got)
	}
}

func TestBillingAccountListRejectsPublicFilterAndOrder(t *testing.T) {
	server, err := NewBillingAccountsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", &billingProviderStub{}).
		SetStore(&billingAccountStoreStub{}).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"filter", "order"} {
		request := &publicv1.BillingAccountsListRequest{}
		if field == "filter" {
			request.SetFilter("this.spec.display_name == 'Acme'")
		} else {
			request.SetOrder("display_name")
		}
		_, err := server.List(context.Background(), request)
		if got := grpcstatus.Code(err); got != codes.InvalidArgument {
			t.Errorf("%s request status = %v, want INVALID_ARGUMENT; err=%v", field, got, err)
		}
	}
}

func TestBillingAccountDiscoveryRedactsProviderErrors(t *testing.T) {
	provider := &billingProviderStub{listErr: errors.New("provider-secret-id: HTTP response body")}
	server, err := NewPrivateBillingAccountsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetStore(&billingAccountStoreStub{}).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.List(context.Background(), &privatev1.BillingAccountsListRequest{})
	if got := grpcstatus.Code(err); got != codes.Unavailable {
		t.Fatalf("status = %v, want UNAVAILABLE; err=%v", got, err)
	}
	if err != nil && (strings.Contains(err.Error(), "provider-secret-id") || strings.Contains(err.Error(), "HTTP response body")) {
		t.Fatalf("provider error was not redacted: %v", err)
	}
}

func TestNewTenantBillingAssignmentUsesEffectiveTimeSuppliedByCaller(t *testing.T) {
	effectiveAt := timestamppb.New(time.Date(2026, 10, 13, 12, 30, 0, 0, time.UTC))
	requested := privatev1.TenantBillingAssignment_builder{
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
		Spec: privatev1.TenantBillingAssignmentSpec_builder{
			TenantId:         "tenant-a",
			BillingAccountId: "account-b",
		}.Build(),
	}.Build()

	got, err := newTenantBillingAssignment(requested, "tenant-a", "account-b", effectiveAt)
	if err != nil {
		t.Fatalf("newTenantBillingAssignment() error = %v", err)
	}
	if !proto.Equal(got.GetStatus().GetEffectiveFrom(), effectiveAt) {
		t.Fatalf("effective_from = %v, want shared transition time %v", got.GetStatus().GetEffectiveFrom(), effectiveAt)
	}
}
