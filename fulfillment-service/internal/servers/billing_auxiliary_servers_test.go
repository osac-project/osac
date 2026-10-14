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
	"testing"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"github.com/stretchr/testify/require"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type draftChargeProviderStub struct {
	page  billing.DraftChargePage
	query billing.DraftChargeQuery
}

func (p *draftChargeProviderStub) ListDraftCharges(_ context.Context, query billing.DraftChargeQuery) (billing.DraftChargePage, error) {
	p.query = query
	return p.page, nil
}

type exportProviderStub struct {
	createdRequest billing.BillingExportRequest
	created        billing.BillingExport
	queriedAccount string
	queriedID      string
	queried        billing.BillingExport
}

func (p *exportProviderStub) CreateBillingExport(_ context.Context, request billing.BillingExportRequest) (billing.BillingExport, error) {
	p.createdRequest = request
	return p.created, nil
}

func (p *exportProviderStub) GetBillingExport(_ context.Context, accountReference, id string) (billing.BillingExport, error) {
	p.queriedAccount = accountReference
	p.queriedID = id
	return p.queried, nil
}

func TestDraftChargesListUsesTenantAccountAndProviderPage(t *testing.T) {
	provider := &draftChargeProviderStub{page: billing.DraftChargePage{
		Total: 1,
		Items: []billing.DraftCharge{{
			ID: "charge-1", Description: "VM usage", Service: "compute_instance",
			ProjectID: new("project-a"), ResourceID: new("vm-1"), Amount: "4.50", CurrencyCode: "USD",
			PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
		}},
	}}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server, err := NewDraftChargesServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetAccountStore(store).
		SetTenancyLogic(newInvoiceTenancyLogic(t, "tenant-a")).
		Build()
	require.NoError(t, err)
	offset, limit := int32(10), int32(25)
	response, err := server.List(context.Background(), publicv1.DraftChargesListRequest_builder{
		TenantId: "tenant-a", FromDate: "2026-08-01", ToDate: "2026-08-31", Offset: &offset, Limit: &limit,
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "tenant-a", store.tenant)
	require.Equal(t, "provider-account", provider.query.AccountReference)
	require.Equal(t, int32(10), provider.query.Offset)
	require.Equal(t, int32(25), provider.query.Limit)
	require.Equal(t, "charge-1", response.GetItems()[0].GetId())
	require.Equal(t, "vm-1", response.GetItems()[0].GetResourceId())
	require.Equal(t, int32(1), response.GetTotal())
}

func TestDraftChargesListReturnsUnsupportedWhenProviderLacksCapability(t *testing.T) {
	server, err := NewDraftChargesServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", nil).
		SetTenancyLogic(newInvoiceTenancyLogic(t, "tenant-a")).
		Build()
	require.NoError(t, err)
	_, err = server.List(context.Background(), publicv1.DraftChargesListRequest_builder{
		TenantId: "tenant-a", FromDate: "2026-08-01", ToDate: "2026-08-31",
	}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.Unimplemented, status.Code())
	require.Equal(t, "PROVIDER_CAPABILITY_UNSUPPORTED", status.Message())
}

func TestBillingExportsCreateAndGetUseTenantAccount(t *testing.T) {
	createdAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	provider := &exportProviderStub{
		created: billing.BillingExport{
			ID: "export-1", FromDate: "2026-08-01", ToDate: "2026-08-31",
			State: billing.BillingExportStateRunning, CreatedAt: createdAt,
		},
		queried: billing.BillingExport{
			ID: "export-1", FromDate: "2026-08-01", ToDate: "2026-08-31",
			State: billing.BillingExportStateSucceeded, CreatedAt: createdAt, CompletedAt: &createdAt,
		},
	}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server, err := NewBillingExportsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetAccountStore(store).
		SetTenancyLogic(newInvoiceTenancyLogic(t, "tenant-a")).
		Build()
	require.NoError(t, err)
	createResponse, err := server.Create(context.Background(), publicv1.BillingExportsCreateRequest_builder{
		TenantId: "tenant-a", FromDate: "2026-08-01", ToDate: "2026-08-31", IdempotencyKey: "  retry-stable-key  ",
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "retry-stable-key", provider.createdRequest.IdempotencyKey)
	require.Equal(t, "provider-account", provider.createdRequest.AccountReference)
	require.Equal(t, publicv1.BillingExportState_BILLING_EXPORT_STATE_RUNNING, createResponse.GetObject().GetState())
	require.Equal(t, "tenant-a", createResponse.GetObject().GetTenantId())

	getResponse, err := server.Get(context.Background(), publicv1.BillingExportsGetRequest_builder{
		TenantId: "tenant-a", Id: "export-1",
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "provider-account", provider.queriedAccount)
	require.Equal(t, "export-1", provider.queriedID)
	require.Equal(t, publicv1.BillingExportState_BILLING_EXPORT_STATE_SUCCEEDED, getResponse.GetObject().GetState())
	require.NotNil(t, getResponse.GetObject().GetCompletedAt())
}

func TestBillingExportsReturnsUnsupportedWhenProviderLacksCapability(t *testing.T) {
	server, err := NewBillingExportsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", nil).
		SetTenancyLogic(newInvoiceTenancyLogic(t, "tenant-a")).
		Build()
	require.NoError(t, err)
	_, err = server.Create(context.Background(), publicv1.BillingExportsCreateRequest_builder{
		TenantId: "tenant-a", FromDate: "2026-08-01", ToDate: "2026-08-31", IdempotencyKey: "key",
	}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.Unimplemented, status.Code())
	require.Equal(t, "PROVIDER_CAPABILITY_UNSUPPORTED", status.Message())
}

func TestBillingExportsRejectsProviderResponseWithReversedPeriod(t *testing.T) {
	createdAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	provider := &exportProviderStub{created: billing.BillingExport{
		ID: "export-1", FromDate: "2026-08-31", ToDate: "2026-08-01",
		State: billing.BillingExportStateRunning, CreatedAt: createdAt,
	}}
	store := &invoiceAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "provider-account", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server, err := NewBillingExportsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetAccountStore(store).
		SetTenancyLogic(newInvoiceTenancyLogic(t, "tenant-a")).
		Build()
	require.NoError(t, err)
	_, err = server.Create(context.Background(), publicv1.BillingExportsCreateRequest_builder{
		TenantId: "tenant-a", FromDate: "2026-08-01", ToDate: "2026-08-31", IdempotencyKey: "key",
	}.Build())
	require.Equal(t, grpccodes.Unavailable, grpcstatus.Code(err))
}
