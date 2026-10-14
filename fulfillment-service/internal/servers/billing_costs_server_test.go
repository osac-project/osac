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
	"fmt"
	"testing"
	"time"

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

type costProviderStub struct {
	costs []billing.Cost
	query billing.CostQuery
	err   error
	calls int
}

func (p *costProviderStub) ListCosts(_ context.Context, query billing.CostQuery) ([]billing.Cost, error) {
	p.calls++
	p.query = query
	return p.costs, p.err
}

func TestBillingCostsRejectsRangesOver366DaysBeforeProviderRead(t *testing.T) {
	provider := &costProviderStub{}
	server := newBillingCostsServer(t, provider, &costAccountStoreStub{}, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{FromDate: "2026-01-01", ToDate: "2027-01-02"}.Build())
	require.Equal(t, grpccodes.InvalidArgument, grpcstatus.Code(err))
	require.Zero(t, provider.calls)
}

func TestBillingCostsMapsProviderHeaderCapToFailedPrecondition(t *testing.T) {
	provider := &costProviderStub{err: billing.ErrCostHeaderLimitExceeded}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{FromDate: "2026-08-01", ToDate: "2026-08-31"}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.FailedPrecondition, status.Code())
	require.Equal(t, "BILLING_COST_RANGE_TOO_LARGE", status.Message())
}

func TestBillingCostsHonorsSharedProviderCircuit(t *testing.T) {
	provider := &costProviderStub{}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	resolver := NewCatalogPricingResolver("m360", nil, nil)
	resolver.circuit.openUntil = time.Now().Add(time.Minute)
	server, err := NewBillingCostsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetPricingResolver(resolver).
		SetAccountStore(store).
		SetTenancyLogic(newCostTenancyLogic(t, "project-a")).
		Build()
	require.NoError(t, err)
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err = server.List(ctx, publicv1.BillingCostsListRequest_builder{FromDate: "2026-08-01", ToDate: "2026-08-31"}.Build())
	require.Equal(t, grpccodes.Unavailable, grpcstatus.Code(err))
	require.Zero(t, provider.calls)
}

type costAccountStoreStub struct {
	account billing.AssignedAccount
	err     error
	tenant  string
}

func (s *costAccountStoreStub) GetAssignedAccount(_ context.Context, tenant string) (billing.AssignedAccount, error) {
	s.tenant = tenant
	return s.account, s.err
}

func newCostTenancyLogic(t *testing.T, projectVisibility ...string) *auth.MockTenancyLogic {
	t.Helper()
	visibilityBuilder := auth.NewVisibility()
	for _, project := range projectVisibility {
		visibilityBuilder.AddVisibleProject("tenant-a", project)
	}
	visibility, err := visibilityBuilder.Build()
	require.NoError(t, err)
	return newCostTenancyLogicWithVisibility(t, visibility)
}

func newCostTenancyLogicWithVisibility(t *testing.T, visibility *auth.Visibility) *auth.MockTenancyLogic {
	t.Helper()
	controller := gomock.NewController(t)
	logic := auth.NewMockTenancyLogic(controller)
	logic.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
	logic.EXPECT().DetermineAssignableTenants(gomock.Any()).Return(collections.NewSet("tenant-a"), nil).AnyTimes()
	return logic
}

func newBillingCostsServer(t *testing.T, provider *costProviderStub, store *costAccountStoreStub, projects ...string) *BillingCostsServer {
	t.Helper()
	server, err := NewBillingCostsServer().
		SetLogger(newBillingTestLogger()).
		SetProvider("m360", provider).
		SetAccountStore(store).
		SetTenancyLogic(newCostTenancyLogic(t, projects...)).
		Build()
	require.NoError(t, err)
	return server
}

func TestBillingCostsListFiltersToVisibleProjects(t *testing.T) {
	provider := &costProviderStub{costs: []billing.Cost{
		{ProjectID: new("project-a"), ResourceID: new("vm-1"), Service: billing.ServiceComputeInstance, Amount: "4.50", CurrencyCode: "USD"},
		{ProjectID: new("project-b"), ResourceID: new("vm-2"), Service: billing.ServiceComputeInstance, Amount: "7.00", CurrencyCode: "USD"},
	}}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	offset, limit := int32(0), int32(10)
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	response, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{
		FromDate: "2026-08-01", ToDate: "2026-08-31", Offset: &offset, Limit: &limit,
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "tenant-a", store.tenant)
	require.Equal(t, "native-account-1", provider.query.AccountReference)
	require.Equal(t, "tenant-a", provider.query.TenantID)
	require.Equal(t, "2026-08-01", provider.query.FromDate)
	require.Equal(t, "2026-08-31", provider.query.ToDate)
	require.Equal(t, int32(1), response.GetTotal())
	require.Equal(t, int32(1), response.GetSize())
	require.Equal(t, "project-a", response.GetItems()[0].GetProjectId())
	require.Equal(t, "vm-1", response.GetItems()[0].GetResourceId())
	require.True(t, response.GetItems()[0].HasAsOf(), "successful cost reads should include as_of")
}

func TestBillingCostsDropsRowsAttributedToAnotherTenant(t *testing.T) {
	provider := &costProviderStub{costs: []billing.Cost{
		{
			TenantID: new("tenant-a"), ProjectID: new("project-a"), ResourceID: new("vm-1"),
			Service: billing.ServiceComputeInstance, Amount: "4.50", CurrencyCode: "USD",
		},
		{
			TenantID: new("tenant-b"), ProjectID: new("project-a"), ResourceID: new("vm-2"),
			Service: billing.ServiceComputeInstance, Amount: "7.00", CurrencyCode: "USD",
		},
		{
			TenantID: new(" \t "), ProjectID: new("project-a"), ResourceID: new("vm-3"),
			Service: billing.ServiceComputeInstance, Amount: "8.00", CurrencyCode: "USD",
		},
	}}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	response, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{
		FromDate: "2026-08-01", ToDate: "2026-08-31",
	}.Build())
	require.NoError(t, err)
	require.Equal(t, "tenant-a", provider.query.TenantID)
	require.Equal(t, int32(1), response.GetTotal())
	require.Len(t, response.GetItems(), 1)
	require.Equal(t, "vm-1", response.GetItems()[0].GetResourceId())
}

func TestBillingCostsPaginatesVisibleAggregatesAtLimit20(t *testing.T) {
	costs := []billing.Cost{{ProjectID: new("project-hidden"), ResourceID: new("hidden-vm"), Service: billing.ServiceComputeInstance, Amount: "100.00", CurrencyCode: "USD"}}
	for index := range 21 {
		costs = append(costs, billing.Cost{
			ProjectID: new("project-a"), ResourceID: new(fmt.Sprintf("vm-%d", index)),
			Service: billing.ServiceComputeInstance, Amount: "1.00", CurrencyCode: "USD",
		})
	}
	provider := &costProviderStub{costs: costs}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")
	offset, limit := int32(1), int32(20)

	response, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{
		FromDate: "2026-08-01", ToDate: "2026-08-31", Offset: &offset, Limit: &limit,
	}.Build())
	require.NoError(t, err)
	require.Equal(t, int32(21), response.GetTotal())
	require.Equal(t, int32(20), response.GetSize())
	require.Len(t, response.GetItems(), 20)
	for _, item := range response.GetItems() {
		require.Equal(t, "project-a", item.GetProjectId())
		require.NotEqual(t, "hidden-vm", item.GetResourceId())
	}
}

func TestBillingCostsFiltersRowsByProjectVisibility(t *testing.T) {
	partialVisibility, err := auth.NewVisibility().
		AddVisibleProject("tenant-a", "project-a").
		Build()
	require.NoError(t, err)
	totalVisibility := auth.TotalVisibility()

	testCases := []struct {
		name       string
		visibility *auth.Visibility
		costs      []billing.Cost
		wantRows   []string
	}{
		{
			name:       "tenant caller skips unattributed and hidden rows but includes visible descendants",
			visibility: partialVisibility,
			costs: []billing.Cost{
				{ResourceID: new("vm-unattributed"), Service: billing.ServiceComputeInstance, Amount: "2.00", CurrencyCode: "USD"},
				{ProjectID: new("project-a.child"), ResourceID: new("vm-child"), Service: billing.ServiceComputeInstance, Amount: "4.50", CurrencyCode: "USD"},
				{ProjectID: new("project-b"), ResourceID: new("vm-hidden"), Service: billing.ServiceComputeInstance, Amount: "7.00", CurrencyCode: "USD"},
			},
			wantRows: []string{"project-a.child|vm-child"},
		},
		{
			name:       "tenant caller returns an empty list when every row is unattributed",
			visibility: partialVisibility,
			costs: []billing.Cost{
				{ResourceID: new("vm-unattributed"), Service: billing.ServiceComputeInstance, Amount: "2.00", CurrencyCode: "USD"},
			},
			wantRows: []string{},
		},
		{
			name:       "total visibility retains unattributed and otherwise hidden rows",
			visibility: totalVisibility,
			costs: []billing.Cost{
				{ResourceID: new("vm-unattributed"), Service: billing.ServiceComputeInstance, Amount: "2.00", CurrencyCode: "USD"},
				{ProjectID: new("project-b.child"), ResourceID: new("vm-other-project"), Service: billing.ServiceComputeInstance, Amount: "7.00", CurrencyCode: "USD"},
			},
			wantRows: []string{"<unattributed>|vm-unattributed", "project-b.child|vm-other-project"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &costProviderStub{costs: testCase.costs}
			store := &costAccountStoreStub{account: billing.AssignedAccount{
				ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
			}}
			server, err := NewBillingCostsServer().
				SetLogger(newBillingTestLogger()).
				SetProvider("m360", provider).
				SetAccountStore(store).
				SetTenancyLogic(newCostTenancyLogicWithVisibility(t, testCase.visibility)).
				Build()
			require.NoError(t, err)
			ctx := config.TenantIntoContext(context.Background(), "tenant-a")

			response, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{
				FromDate: "2026-08-01", ToDate: "2026-08-31",
			}.Build())
			require.NoError(t, err)
			gotRows := make([]string, 0, len(response.GetItems()))
			for _, item := range response.GetItems() {
				projectID := "<unattributed>"
				if item.HasProjectId() {
					projectID = item.GetProjectId()
				}
				gotRows = append(gotRows, projectID+"|"+item.GetResourceId())
			}
			require.Equal(t, testCase.wantRows, gotRows, "visible provider rows should be returned once, without synthesized parent rollups")
			require.Equal(t, int32(len(testCase.wantRows)), response.GetTotal())
			require.Equal(t, int32(len(testCase.wantRows)), response.GetSize())
		})
	}
}

func TestBillingCostsRejectsProviderCurrencyMismatch(t *testing.T) {
	provider := &costProviderStub{costs: []billing.Cost{{
		ProjectID: new("project-a"), Service: billing.ServiceComputeInstance, Amount: "4.50", CurrencyCode: "EUR",
	}}}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{
		FromDate: "2026-08-01", ToDate: "2026-08-31",
	}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.Unavailable, status.Code())
}

func TestBillingCostsSanitizesProviderErrors(t *testing.T) {
	provider := &costProviderStub{err: errors.New("provider response leaked native account reference")}
	store := &costAccountStoreStub{account: billing.AssignedAccount{
		ProviderKey: "m360", ProviderAccountReference: "native-account-1", CurrencyCode: "USD", ConnectionState: "CONNECTED",
	}}
	server := newBillingCostsServer(t, provider, store, "project-a")
	ctx := config.TenantIntoContext(context.Background(), "tenant-a")

	_, err := server.List(ctx, publicv1.BillingCostsListRequest_builder{FromDate: "2026-08-01", ToDate: "2026-08-31"}.Build())
	status, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	require.Equal(t, grpccodes.Unavailable, status.Code())
	require.NotContains(t, status.Message(), "native account reference")
}
