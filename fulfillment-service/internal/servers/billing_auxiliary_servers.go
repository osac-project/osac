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
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	draftChargesDefaultLimit = 20
	draftChargesMaxLimit     = 100
)

type DraftChargesServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.DraftChargeProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

type DraftChargesServer struct {
	publicv1.UnimplementedDraftChargesServer
	logger      *slog.Logger
	providerKey string
	provider    billing.DraftChargeProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

var _ publicv1.DraftChargesServer = (*DraftChargesServer)(nil)

func NewDraftChargesServer() *DraftChargesServerBuilder { return &DraftChargesServerBuilder{} }

func (b *DraftChargesServerBuilder) SetLogger(value *slog.Logger) *DraftChargesServerBuilder {
	b.logger = value
	return b
}

func (b *DraftChargesServerBuilder) SetProvider(providerKey string, value billing.DraftChargeProvider) *DraftChargesServerBuilder {
	b.providerKey, b.provider = providerKey, value
	return b
}

func (b *DraftChargesServerBuilder) SetAccountStore(value assignedBillingAccountStore) *DraftChargesServerBuilder {
	b.store = value
	return b
}

func (b *DraftChargesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *DraftChargesServerBuilder {
	b.tenancy = value
	return b
}

func (b *DraftChargesServerBuilder) Build() (*DraftChargesServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancy == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	return &DraftChargesServer{
		logger: b.logger, providerKey: b.providerKey, provider: b.provider, store: b.store, tenancy: b.tenancy,
	}, nil
}

func (s *DraftChargesServer) List(ctx context.Context, request *publicv1.DraftChargesListRequest) (*publicv1.DraftChargesListResponse, error) {
	if request == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "request is required")
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	if err := validateBillingPeriod(request.GetFromDate(), request.GetToDate()); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "draft charge date range is invalid")
	}
	offset := request.GetOffset()
	if offset < 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "offset must not be negative")
	}
	limit := int32(draftChargesDefaultLimit)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	if limit < 1 || limit > draftChargesMaxLimit {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "limit must be between 1 and %d", draftChargesMaxLimit)
	}
	tenantID := strings.TrimSpace(request.GetTenantId())
	if tenantID == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id is required")
	}
	account, err := resolveBillingAdminAccount(ctx, s.logger, s.providerKey, s.store, s.tenancy, tenantID)
	if err != nil {
		return nil, err
	}
	page, err := s.provider.ListDraftCharges(ctx, billing.DraftChargeQuery{
		AccountReference: account.ProviderAccountReference,
		FromDate:         request.GetFromDate(), ToDate: request.GetToDate(), Offset: offset, Limit: limit,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list provider draft charges")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider draft charges are unavailable")
	}
	items := make([]*publicv1.BillingDraftCharge, 0, len(page.Items))
	for _, charge := range page.Items {
		if strings.TrimSpace(charge.ID) == "" || strings.TrimSpace(charge.Amount) == "" || len(strings.TrimSpace(charge.CurrencyCode)) != 3 {
			s.logger.ErrorContext(ctx, "provider returned an incomplete draft charge")
			return nil, grpcstatus.Error(grpccodes.Unavailable, "provider draft charges are unavailable")
		}
		currency := strings.ToUpper(strings.TrimSpace(charge.CurrencyCode))
		if account.CurrencyCode != "" && !strings.EqualFold(currency, account.CurrencyCode) {
			s.logger.ErrorContext(ctx, "provider returned a draft charge in an unexpected currency")
			return nil, grpcstatus.Error(grpccodes.Unavailable, "provider draft charges are unavailable")
		}
		items = append(items, publicv1.BillingDraftCharge_builder{
			Id: charge.ID, Description: charge.Description, Service: charge.Service,
			ProjectId: charge.ProjectID, ResourceId: charge.ResourceID, Amount: charge.Amount,
			CurrencyCode: currency, PeriodStart: charge.PeriodStart, PeriodEnd: charge.PeriodEnd,
		}.Build())
	}
	size, ok := catalogPricingInputTotal(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many provider draft charges")
	}
	return publicv1.DraftChargesListResponse_builder{Size: size, Total: page.Total, Items: items}.Build(), nil
}

type BillingExportsServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.BillingExportProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

type BillingExportsServer struct {
	publicv1.UnimplementedBillingExportsServer
	logger      *slog.Logger
	providerKey string
	provider    billing.BillingExportProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

var _ publicv1.BillingExportsServer = (*BillingExportsServer)(nil)

func NewBillingExportsServer() *BillingExportsServerBuilder { return &BillingExportsServerBuilder{} }

func (b *BillingExportsServerBuilder) SetLogger(value *slog.Logger) *BillingExportsServerBuilder {
	b.logger = value
	return b
}

func (b *BillingExportsServerBuilder) SetProvider(providerKey string, value billing.BillingExportProvider) *BillingExportsServerBuilder {
	b.providerKey, b.provider = providerKey, value
	return b
}

func (b *BillingExportsServerBuilder) SetAccountStore(value assignedBillingAccountStore) *BillingExportsServerBuilder {
	b.store = value
	return b
}

func (b *BillingExportsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BillingExportsServerBuilder {
	b.tenancy = value
	return b
}

func (b *BillingExportsServerBuilder) Build() (*BillingExportsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancy == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	return &BillingExportsServer{
		logger: b.logger, providerKey: b.providerKey, provider: b.provider, store: b.store, tenancy: b.tenancy,
	}, nil
}

func (s *BillingExportsServer) Create(ctx context.Context, request *publicv1.BillingExportsCreateRequest) (*publicv1.BillingExportsCreateResponse, error) {
	if request == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "request is required")
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	if err := validateBillingPeriod(request.GetFromDate(), request.GetToDate()); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "export date range is invalid")
	}
	tenantID := strings.TrimSpace(request.GetTenantId())
	idempotencyKey := strings.TrimSpace(request.GetIdempotencyKey())
	if tenantID == "" || idempotencyKey == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id and idempotency_key are required")
	}
	account, err := resolveBillingAdminAccount(ctx, s.logger, s.providerKey, s.store, s.tenancy, tenantID)
	if err != nil {
		return nil, err
	}
	export, err := s.provider.CreateBillingExport(ctx, billing.BillingExportRequest{
		AccountReference: account.ProviderAccountReference, TenantID: tenantID,
		FromDate: request.GetFromDate(), ToDate: request.GetToDate(), IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to create provider billing export")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider billing export is unavailable")
	}
	object, err := billingExportToProto(export, tenantID)
	if err != nil {
		s.logger.ErrorContext(ctx, "provider returned an invalid billing export")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider billing export is unavailable")
	}
	return publicv1.BillingExportsCreateResponse_builder{Object: object}.Build(), nil
}

func (s *BillingExportsServer) Get(ctx context.Context, request *publicv1.BillingExportsGetRequest) (*publicv1.BillingExportsGetResponse, error) {
	if request == nil || strings.TrimSpace(request.GetId()) == "" || strings.TrimSpace(request.GetTenantId()) == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id and export id are required")
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	tenantID := strings.TrimSpace(request.GetTenantId())
	account, err := resolveBillingAdminAccount(ctx, s.logger, s.providerKey, s.store, s.tenancy, tenantID)
	if err != nil {
		return nil, err
	}
	exportID := strings.TrimSpace(request.GetId())
	export, err := s.provider.GetBillingExport(ctx, account.ProviderAccountReference, exportID)
	if errors.Is(err, billing.ErrBillingExportNotFound) {
		return nil, grpcstatus.Error(grpccodes.NotFound, "billing export was not found")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to retrieve provider billing export")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider billing export is unavailable")
	}
	if export.ID != exportID {
		s.logger.ErrorContext(ctx, "provider returned a mismatched billing export")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider billing export is unavailable")
	}
	object, err := billingExportToProto(export, tenantID)
	if err != nil {
		s.logger.ErrorContext(ctx, "provider returned an invalid billing export")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider billing export is unavailable")
	}
	return publicv1.BillingExportsGetResponse_builder{Object: object}.Build(), nil
}

func resolveBillingAdminAccount(ctx context.Context, logger *slog.Logger, providerKey string, store assignedBillingAccountStore, tenancy auth.TenancyLogic, tenantID string) (billing.AssignedAccount, error) {
	if err := auth.ValidateTenantAssignment(ctx, tenancy, tenantID); err != nil {
		return billing.AssignedAccount{}, convertTenantErrorToGRPC(ctx, err, logger, tenantID)
	}
	if store == nil {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "tenant billing account lookup is unavailable")
	}
	account, err := store.GetAssignedAccount(ctx, tenantID)
	if errors.Is(err, billing.ErrTenantBillingAssignmentNotFound) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant has no active billing account")
	}
	if err != nil {
		logger.ErrorContext(ctx, "failed to resolve tenant billing account", slog.Any("error", err))
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "failed to resolve tenant billing account")
	}
	if account.ProviderKey != providerKey {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account uses a different provider")
	}
	if !assignmentIsConnected(account.ConnectionState) || account.ProviderAccountReference == "" {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account is not connected")
	}
	return account, nil
}

func billingExportToProto(export billing.BillingExport, tenantID string) (*publicv1.BillingExport, error) {
	if strings.TrimSpace(export.ID) == "" || export.CreatedAt.IsZero() {
		return nil, errors.New("billing export is incomplete")
	}
	var state publicv1.BillingExportState
	switch export.State {
	case billing.BillingExportStatePending:
		state = publicv1.BillingExportState_BILLING_EXPORT_STATE_PENDING
	case billing.BillingExportStateRunning:
		state = publicv1.BillingExportState_BILLING_EXPORT_STATE_RUNNING
	case billing.BillingExportStateSucceeded:
		state = publicv1.BillingExportState_BILLING_EXPORT_STATE_SUCCEEDED
	case billing.BillingExportStateFailed:
		state = publicv1.BillingExportState_BILLING_EXPORT_STATE_FAILED
	default:
		return nil, errors.New("billing export has an unknown state")
	}
	if err := validateBillingPeriod(export.FromDate, export.ToDate); err != nil {
		return nil, errors.New("billing export has an invalid date range")
	}
	createdAt := timestamppb.New(export.CreatedAt)
	if err := createdAt.CheckValid(); err != nil {
		return nil, errors.New("billing export has an invalid creation time")
	}
	builder := publicv1.BillingExport_builder{
		Id: export.ID, TenantId: tenantID, FromDate: export.FromDate, ToDate: export.ToDate,
		State: state, CreatedAt: createdAt,
	}
	if export.CompletedAt != nil {
		completedAt := timestamppb.New(*export.CompletedAt)
		if err := completedAt.CheckValid(); err != nil {
			return nil, errors.New("billing export has an invalid completion time")
		}
		builder.CompletedAt = completedAt
	}
	return builder.Build(), nil
}

func validateBillingPeriod(fromDate, toDate string) error {
	from, err := time.Parse("2006-01-02", fromDate)
	if err != nil {
		return err
	}
	to, err := time.Parse("2006-01-02", toDate)
	if err != nil {
		return err
	}
	if from.After(to) {
		return errors.New("from_date must be on or before to_date")
	}
	return nil
}
