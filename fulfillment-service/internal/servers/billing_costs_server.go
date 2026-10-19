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
	"log/slog"
	"strings"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	billingCostDefaultLimit = 20
	billingCostMaxLimit     = 100
)

var errBillingCostCircuitOpen = errors.New("billing cost circuit is open")

type BillingCostsServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.CostProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

type BillingCostsServer struct {
	publicv1.UnimplementedBillingCostsServer
	logger      *slog.Logger
	providerKey string
	provider    billing.CostProvider
	circuit     costReadCircuitBreaker
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

// costReadCircuitBreaker has independent state from pricing coverage lookups.
// It uses the same breaker policy while keeping cost read failures isolated.
type costReadCircuitBreaker struct {
	catalogPricingCircuitBreaker
}

var _ publicv1.BillingCostsServer = (*BillingCostsServer)(nil)

func NewBillingCostsServer() *BillingCostsServerBuilder {
	return &BillingCostsServerBuilder{}
}

func (b *BillingCostsServerBuilder) SetLogger(value *slog.Logger) *BillingCostsServerBuilder {
	b.logger = value
	return b
}

func (b *BillingCostsServerBuilder) SetProvider(providerKey string, value billing.CostProvider) *BillingCostsServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *BillingCostsServerBuilder) SetAccountStore(value assignedBillingAccountStore) *BillingCostsServerBuilder {
	b.store = value
	return b
}

func (b *BillingCostsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BillingCostsServerBuilder {
	b.tenancy = value
	return b
}

func (b *BillingCostsServerBuilder) Build() (*BillingCostsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancy == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	return &BillingCostsServer{
		logger:      b.logger,
		providerKey: b.providerKey,
		provider:    b.provider,
		store:       b.store,
		tenancy:     b.tenancy,
	}, nil
}

func (s *BillingCostsServer) List(ctx context.Context,
	request *publicv1.BillingCostsListRequest,
) (*publicv1.BillingCostsListResponse, error) {
	if request == nil {
		request = &publicv1.BillingCostsListRequest{}
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	offset := request.GetOffset()
	if offset < 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "offset must not be negative")
	}
	limit := int32(billingCostDefaultLimit)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	if limit < 1 || limit > billingCostMaxLimit {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "limit must be between 1 and %d", billingCostMaxLimit)
	}
	if err := validateBillingCostDateRange(request.GetFromDate(), request.GetToDate()); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "cost date range is invalid")
	}

	tenantID := strings.TrimSpace(request.GetTenantId())
	if tenantID == "" {
		tenantID = config.TenantFromContext(ctx)
	}
	if tenantID == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id is required")
	}
	account, err := s.assignedAccount(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	costs, err := s.listProviderCosts(ctx, billing.CostQuery{
		AccountReference: account.ProviderAccountReference,
		TenantID:         tenantID,
		FromDate:         request.GetFromDate(),
		ToDate:           request.GetToDate(),
	})
	if err != nil {
		if errors.Is(err, billing.ErrCostHeaderLimitExceeded) {
			return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_COST_RANGE_TOO_LARGE")
		}
		s.logger.ErrorContext(ctx, "failed to list provider costs")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider costs are unavailable")
	}
	costsAsOf := timestamppb.New(time.Now())
	visibility, err := s.tenancy.DetermineVisibility(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to determine billing project visibility", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to determine project visibility")
	}
	if visibility == nil || visibility.IsZero() {
		return nil, grpcstatus.Error(grpccodes.PermissionDenied, "tenant project visibility is unavailable")
	}

	visibleCosts, err := s.visibleBillingCosts(ctx, costs, tenantID, account.CurrencyCode, visibility)
	if err != nil {
		return nil, err
	}

	total, ok := catalogPricingInputTotal(len(visibleCosts))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many provider costs")
	}
	start := min(int(offset), len(visibleCosts))
	end := min(start+int(limit), len(visibleCosts))
	items := make([]*publicv1.BillingCost, 0, end-start)
	for _, cost := range visibleCosts[start:end] {
		items = append(items, publicv1.BillingCost_builder{
			ProjectId:    cost.ProjectID,
			ResourceId:   cost.ResourceID,
			Service:      cost.Service,
			Amount:       cost.Amount,
			CurrencyCode: cost.CurrencyCode,
			PeriodStart:  cost.PeriodStart,
			PeriodEnd:    cost.PeriodEnd,
			AsOf:         costsAsOf,
		}.Build())
	}
	size, ok := catalogPricingInputTotal(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many provider costs")
	}
	return publicv1.BillingCostsListResponse_builder{
		Size:  size,
		Total: total,
		Items: items,
	}.Build(), nil
}

func (s *BillingCostsServer) visibleBillingCosts(
	ctx context.Context,
	costs []billing.Cost,
	tenantID string,
	currencyCode string,
	visibility *auth.Visibility,
) ([]billing.Cost, error) {
	visibleCosts := make([]billing.Cost, 0, len(costs))
	for _, cost := range costs {
		if cost.TenantID == nil {
			// The linked account belongs to this tenant under the current 1:1 model.
			cost.TenantID = &tenantID
		} else {
			attributedTenantID := strings.TrimSpace(*cost.TenantID)
			if attributedTenantID != tenantID {
				continue
			}
			cost.TenantID = &attributedTenantID
		}
		if strings.TrimSpace(cost.Service) == "" || strings.TrimSpace(cost.Amount) == "" || len(strings.TrimSpace(cost.CurrencyCode)) != 3 {
			s.logger.ErrorContext(ctx, "provider returned an incomplete cost")
			return nil, grpcstatus.Error(grpccodes.Unavailable, "provider costs are unavailable")
		}
		cost.CurrencyCode = strings.ToUpper(strings.TrimSpace(cost.CurrencyCode))
		if currencyCode != "" && !strings.EqualFold(cost.CurrencyCode, currencyCode) {
			s.logger.ErrorContext(ctx, "provider returned a cost in an unexpected currency")
			return nil, grpcstatus.Error(grpccodes.Unavailable, "provider costs are unavailable")
		}
		if cost.ProjectID != nil {
			projectID := strings.TrimSpace(*cost.ProjectID)
			cost.ProjectID = &projectID
		}
		if cost.ResourceID != nil {
			resourceID := strings.TrimSpace(*cost.ResourceID)
			cost.ResourceID = &resourceID
		}
		if !visibility.IsTotal() {
			if cost.ProjectID == nil {
				// A tenant-scoped caller cannot safely see a cost without project attribution.
				// Skip that row while preserving the rest of the caller's visible costs.
				continue
			}
			// IsProjectVisible also grants access to descendants of each visible project.
			// Keep provider rows at their original project/resource level; synthesizing
			// parent rollups here could double-count rows when clients sum the response.
			if !visibility.IsProjectVisible(tenantID, *cost.ProjectID) {
				continue
			}
		}
		visibleCosts = append(visibleCosts, cost)
	}
	return visibleCosts, nil
}

func (s *BillingCostsServer) listProviderCosts(ctx context.Context, query billing.CostQuery) ([]billing.Cost, error) {
	allowed, probe := s.circuit.allow(time.Now())
	if !allowed {
		return nil, errBillingCostCircuitOpen
	}
	costs, err := billing.RetryProviderRead(ctx, func(ctx context.Context) ([]billing.Cost, error) {
		return s.provider.ListCosts(ctx, query)
	})
	if err == nil {
		s.circuit.recordSuccess(probe)
	} else if errors.Is(err, context.Canceled) {
		s.circuit.recordNeutral(probe)
	} else if errors.Is(err, billing.ErrProviderUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		s.circuit.recordFailure(time.Now(), probe)
	} else {
		// A provider response such as a result-size limit is not an outage.
		s.circuit.recordSuccess(probe)
	}
	return costs, err
}

func (s *BillingCostsServer) assignedAccount(ctx context.Context, tenantID string) (billing.AssignedAccount, error) {
	if err := auth.ValidateTenantAssignment(ctx, s.tenancy, tenantID); err != nil {
		return billing.AssignedAccount{}, convertTenantErrorToGRPC(ctx, err, s.logger, tenantID)
	}
	if s.store == nil {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "tenant billing account lookup is unavailable")
	}
	account, err := s.store.GetAssignedAccount(ctx, tenantID)
	if errors.Is(err, billing.ErrTenantBillingAssignmentNotFound) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant has no active billing account")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to resolve tenant billing account", slog.Any("error", err))
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "failed to resolve tenant billing account")
	}
	if account.ProviderKey != s.providerKey {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account uses a different provider")
	}
	if !assignmentIsConnected(account.ConnectionState) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account is not connected")
	}
	if account.ProviderAccountReference == "" {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account is unavailable")
	}
	return account, nil
}

func validateBillingCostDateRange(fromDate, toDate string) error {
	const dateFormat = "2006-01-02"
	from, err := time.Parse(dateFormat, fromDate)
	if err != nil {
		return err
	}
	to, err := time.Parse(dateFormat, toDate)
	if err != nil {
		return err
	}
	if from.After(to) {
		return errors.New("from_date must be on or before to_date")
	}
	if to.Sub(from) >= 366*24*time.Hour {
		return errors.New("cost date range exceeds 366 days")
	}
	return nil
}
