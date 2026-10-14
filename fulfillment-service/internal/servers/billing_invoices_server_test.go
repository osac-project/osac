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
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type invoiceProviderStub struct {
	page       billing.InvoicePage
	invoice    billing.Invoice
	query      billing.InvoiceQuery
	accountRef string
	invoiceID  string
	err        error
}

func (p *invoiceProviderStub) ListInvoices(_ context.Context, query billing.InvoiceQuery) (billing.InvoicePage, error) {
	p.query = query
	return p.page, p.err
}

func (p *invoiceProviderStub) GetInvoice(_ context.Context, accountReference, invoiceID string) (billing.Invoice, error) {
	p.accountRef = accountReference
	p.invoiceID = invoiceID
	return p.invoice, p.err
}

type invoiceAccountStoreStub struct {
	account billing.AssignedAccount
	err     error
	tenant  string
}

func (s *invoiceAccountStoreStub) GetAssignedAccount(_ context.Context, tenant string) (billing.AssignedAccount, error) {
	s.tenant = tenant
	return s.account, s.err
}

func newInvoiceTenancyLogic(t *testing.T, tenants ...string) *auth.MockTenancyLogic {
	t.Helper()
	controller := gomock.NewController(t)
	logic := auth.NewMockTenancyLogic(controller)
	visibilityBuilder := auth.NewVisibility().AddVisibleTenants(tenants...)
	visibility, err := visibilityBuilder.Build()
	require.NoError(t, err)
	logic.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
	logic.EXPECT().DetermineAssignableTenants(gomock.Any()).Return(collections.NewSet(tenants...), nil).AnyTimes()
	return logic
}

func newBillingInvoicesServer(t *testing.T, provider *invoiceProviderStub, store *invoiceAccountStoreStub, tenants ...string) *BillingInvoicesServer {
	t.Helper()
	server, err := NewBillingInvoicesServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetAccountStore(store).
		SetTenancyLogic(newInvoiceTenancyLogic(t, tenants...)).
		Build()
	require.NoError(t, err)
	return server
}

func TestBillingInvoicesListUsesAssignedTenantAccountAndPagination(t *testing.T) {
	provider := &invoiceProviderStub{page: billing.InvoicePage{
		Total: 1,
		Items: []billing.Invoice{{ID: "INV-1001", Number: "INV-1001", ApprovalStatus: "Approved"}},
	}}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", ConnectionState: "CONNECTED",
	}}
	server := newBillingInvoicesServer(t, provider, store, "tenant-a")
	offset, limit := int32(20), int32(10)
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	response, err := server.List(ctx, publicv1.BillingInvoicesListRequest_builder{
		FromDate: new("2026-08-01"), ToDate: new("2026-08-31"), Offset: &offset, Limit: &limit,
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "tenant-a", store.tenant)
	require.Equal(t, "native-account-1", provider.query.AccountReference)
	require.Equal(t, int32(20), provider.query.Offset)
	require.Equal(t, int32(10), provider.query.Limit)
	require.Equal(t, int32(1), response.GetTotal())
	require.Equal(t, "INV-1001", response.GetItems()[0].GetId())
	require.Equal(t, "Approved", response.GetItems()[0].GetApprovalStatus())
}

func TestBillingInvoicesGetReturnsItemizedInvoice(t *testing.T) {
	provider := &invoiceProviderStub{invoice: billing.Invoice{
		ID: "INV-1001", Number: "INV-1001", Amount: "40.25", CurrencyCode: "USD",
		Items: []billing.InvoiceLineItem{{Description: "Usage", Amount: "40.25", CurrencyCode: "USD"}},
	}}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", ConnectionState: "CONNECTED",
	}}
	server := newBillingInvoicesServer(t, provider, store, "tenant-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	response, err := server.Get(ctx, publicv1.BillingInvoicesGetRequest_builder{Id: "INV-1001"}.Build())
	require.NoError(t, err)
	require.Equal(t, "native-account-1", provider.accountRef)
	require.Equal(t, "INV-1001", provider.invoiceID)
	require.Equal(t, "40.25", response.GetObject().GetAmount())
	require.Len(t, response.GetObject().GetItems(), 1)
	require.Equal(t, "Usage", response.GetObject().GetItems()[0].GetDescription())
}

func TestBillingInvoicesRejectsTenantOutsideCallerVisibility(t *testing.T) {
	provider := &invoiceProviderStub{}
	store := &invoiceAccountStoreStub{}
	server := newBillingInvoicesServer(t, provider, store, "tenant-a")

	_, err := server.List(context.Background(), publicv1.BillingInvoicesListRequest_builder{TenantId: new("tenant-b")}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.PermissionDenied, status.Code())
	require.Empty(t, store.tenant)
	require.Empty(t, provider.query.AccountReference)
}

func TestBillingInvoicesSanitizesProviderErrors(t *testing.T) {
	provider := &invoiceProviderStub{err: errors.New("private provider response with native account id")}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", ConnectionState: "CONNECTED",
	}}
	server := newBillingInvoicesServer(t, provider, store, "tenant-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.List(ctx, &publicv1.BillingInvoicesListRequest{})
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.Unavailable, status.Code())
	require.NotContains(t, status.Message(), "native account id")
}

func TestBillingInvoicesGetReturnsNotFoundForUnknownInvoice(t *testing.T) {
	provider := &invoiceProviderStub{err: billing.ErrInvoiceNotFound}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", ConnectionState: "CONNECTED",
	}}
	server := newBillingInvoicesServer(t, provider, store, "tenant-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.Get(ctx, publicv1.BillingInvoicesGetRequest_builder{Id: "INV-MISSING"}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.NotFound, status.Code())
	require.Equal(t, "invoice was not found", status.Message())
}
