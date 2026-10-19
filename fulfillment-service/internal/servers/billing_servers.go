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
	"log/slog"
	"strings"
	"sync"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// BillingAccountStore persists provider-discovered accounts and their stable
// OSAC identifiers.
type BillingAccountStore interface {
	SyncAccounts(context.Context, string, []billing.Account, time.Time) ([]*privatev1.BillingAccount, error)
	GetAccount(context.Context, string) (*privatev1.BillingAccount, error)
	ListAccounts(context.Context, string, int32, int32) ([]*privatev1.BillingAccount, int32, error)
}

type PrivateBillingProviderStatusesServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.Provider
}

type PrivateBillingProviderStatusesServer struct {
	privatev1.UnimplementedBillingProviderStatusesServer
	logger              *slog.Logger
	providerKey         string
	provider            billing.Provider
	lastSuccessfulMu    sync.RWMutex
	lastSuccessfulCheck time.Time
}

var _ privatev1.BillingProviderStatusesServer = (*PrivateBillingProviderStatusesServer)(nil)

func NewPrivateBillingProviderStatusesServer() *PrivateBillingProviderStatusesServerBuilder {
	return &PrivateBillingProviderStatusesServerBuilder{}
}

func (b *PrivateBillingProviderStatusesServerBuilder) SetLogger(value *slog.Logger) *PrivateBillingProviderStatusesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBillingProviderStatusesServerBuilder) SetProvider(providerKey string, value billing.Provider) *PrivateBillingProviderStatusesServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *PrivateBillingProviderStatusesServerBuilder) Build() (*PrivateBillingProviderStatusesServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	return &PrivateBillingProviderStatusesServer{
		logger:      b.logger,
		providerKey: b.providerKey,
		provider:    b.provider,
	}, nil
}

func (s *PrivateBillingProviderStatusesServer) Get(
	ctx context.Context,
	_ *privatev1.BillingProviderStatusesGetRequest,
) (*privatev1.BillingProviderStatusesGetResponse, error) {
	status := billing.ResolveProviderStatus(ctx, s.providerKey, s.provider)
	if status.State == billing.ProviderStateAvailable {
		s.lastSuccessfulMu.Lock()
		s.lastSuccessfulCheck = time.Now().UTC()
		s.lastSuccessfulMu.Unlock()
	}
	s.lastSuccessfulMu.RLock()
	lastSuccessfulCheck := s.lastSuccessfulCheck
	s.lastSuccessfulMu.RUnlock()
	var lastSuccessfulCheckTimestamp *timestamppb.Timestamp
	if !lastSuccessfulCheck.IsZero() {
		lastSuccessfulCheckTimestamp = timestamppb.New(lastSuccessfulCheck)
	}
	object := privatev1.BillingProviderStatus_builder{
		State:                 privateProviderState(status.State),
		ProviderKey:           status.ProviderKey,
		Message:               status.Message,
		SupportedCapabilities: privateProviderCapabilities(status.Capabilities),
		LastSuccessfulCheck:   lastSuccessfulCheckTimestamp,
		UsageDeliveryState:    privateProviderUsageDeliveryState(status.UsageDeliveryState),
	}.Build()
	return privatev1.BillingProviderStatusesGetResponse_builder{Object: object}.Build(), nil
}

type BillingProviderStatusesServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.Provider
}

type BillingProviderStatusesServer struct {
	publicv1.UnimplementedBillingProviderStatusesServer
	private privatev1.BillingProviderStatusesServer
	mapper  *GenericMapper[*privatev1.BillingProviderStatus, *publicv1.BillingProviderStatus]
	logger  *slog.Logger
}

var _ publicv1.BillingProviderStatusesServer = (*BillingProviderStatusesServer)(nil)

func NewBillingProviderStatusesServer() *BillingProviderStatusesServerBuilder {
	return &BillingProviderStatusesServerBuilder{}
}

func (b *BillingProviderStatusesServerBuilder) SetLogger(value *slog.Logger) *BillingProviderStatusesServerBuilder {
	b.logger = value
	return b
}

func (b *BillingProviderStatusesServerBuilder) SetProvider(providerKey string, value billing.Provider) *BillingProviderStatusesServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *BillingProviderStatusesServerBuilder) Build() (*BillingProviderStatusesServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	delegate, err := NewPrivateBillingProviderStatusesServer().
		SetLogger(b.logger).
		SetProvider(b.providerKey, b.provider).
		Build()
	if err != nil {
		return nil, err
	}
	mapper, err := NewGenericMapper[*privatev1.BillingProviderStatus, *publicv1.BillingProviderStatus]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return nil, err
	}
	return &BillingProviderStatusesServer{private: delegate, mapper: mapper, logger: b.logger}, nil
}

func (s *BillingProviderStatusesServer) Get(
	ctx context.Context,
	_ *publicv1.BillingProviderStatusesGetRequest,
) (*publicv1.BillingProviderStatusesGetResponse, error) {
	privateResponse, err := s.private.Get(ctx, &privatev1.BillingProviderStatusesGetRequest{})
	if err != nil {
		return nil, err
	}
	object := &publicv1.BillingProviderStatus{}
	if err = s.mapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		s.logger.ErrorContext(ctx, "failed to map billing provider status", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to map billing provider status")
	}
	return publicv1.BillingProviderStatusesGetResponse_builder{Object: object}.Build(), nil
}

type PrivateBillingAccountsServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.Provider
	store       BillingAccountStore
}

type PrivateBillingAccountsServer struct {
	privatev1.UnimplementedBillingAccountsServer
	logger      *slog.Logger
	providerKey string
	provider    billing.Provider
	store       BillingAccountStore
}

var _ privatev1.BillingAccountsServer = (*PrivateBillingAccountsServer)(nil)

func NewPrivateBillingAccountsServer() *PrivateBillingAccountsServerBuilder {
	return &PrivateBillingAccountsServerBuilder{}
}

func (b *PrivateBillingAccountsServerBuilder) SetLogger(value *slog.Logger) *PrivateBillingAccountsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBillingAccountsServerBuilder) SetProvider(providerKey string, value billing.Provider) *PrivateBillingAccountsServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *PrivateBillingAccountsServerBuilder) SetStore(value BillingAccountStore) *PrivateBillingAccountsServerBuilder {
	b.store = value
	return b
}

func (b *PrivateBillingAccountsServerBuilder) Build() (*PrivateBillingAccountsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.store == nil {
		b.store = billing.NewAccountStore()
	}
	return &PrivateBillingAccountsServer{
		logger:      b.logger,
		providerKey: b.providerKey,
		provider:    b.provider,
		store:       b.store,
	}, nil
}

func (s *PrivateBillingAccountsServer) List(
	ctx context.Context,
	request *privatev1.BillingAccountsListRequest,
) (*privatev1.BillingAccountsListResponse, error) {
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if request.GetOffset() < 0 || request.GetLimit() < 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "offset and limit must not be negative")
	}
	if strings.TrimSpace(request.GetFilter()) != "" || strings.TrimSpace(request.GetOrder()) != "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "billing account filtering and ordering are not supported")
	}
	accounts, err := billing.RetryProviderRead(ctx, s.provider.ListAccounts)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to discover billing accounts")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "billing account discovery failed")
	}
	if _, err = s.store.SyncAccounts(ctx, s.providerKey, accounts, time.Now().UTC()); err != nil {
		s.logger.ErrorContext(ctx, "failed to persist discovered billing accounts", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to persist discovered billing accounts")
	}
	limit := request.GetLimit()
	if !request.HasLimit() {
		limit = 100
	}
	items, total, err := s.store.ListAccounts(ctx, s.providerKey, request.GetOffset(), limit)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list billing accounts", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to list billing accounts")
	}
	size, ok := protobufListSize(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "billing account page is too large")
	}
	return privatev1.BillingAccountsListResponse_builder{Size: size, Total: total, Items: items}.Build(), nil
}

func protobufListSize(size int) (int32, bool) {
	const maxInt32 = int64(1<<31 - 1)
	if size < 0 || int64(size) > maxInt32 {
		return 0, false
	}
	return int32(size), true
}

func (s *PrivateBillingAccountsServer) Get(
	ctx context.Context,
	request *privatev1.BillingAccountsGetRequest,
) (*privatev1.BillingAccountsGetResponse, error) {
	if request.GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "billing account identifier is required")
	}
	account, err := s.store.GetAccount(ctx, request.GetId())
	if errors.Is(err, billing.ErrAccountNotFound) {
		return nil, grpcstatus.Error(grpccodes.NotFound, "billing account was not found")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to retrieve billing account", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to retrieve billing account")
	}
	return privatev1.BillingAccountsGetResponse_builder{Object: account}.Build(), nil
}

type BillingAccountsServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.Provider
	store       BillingAccountStore
}

type BillingAccountsServer struct {
	publicv1.UnimplementedBillingAccountsServer
	private *PrivateBillingAccountsServer
	mapper  *GenericMapper[*privatev1.BillingAccount, *publicv1.BillingAccount]
	logger  *slog.Logger
}

var _ publicv1.BillingAccountsServer = (*BillingAccountsServer)(nil)

func NewBillingAccountsServer() *BillingAccountsServerBuilder {
	return &BillingAccountsServerBuilder{}
}

func (b *BillingAccountsServerBuilder) SetLogger(value *slog.Logger) *BillingAccountsServerBuilder {
	b.logger = value
	return b
}

func (b *BillingAccountsServerBuilder) SetProvider(providerKey string, value billing.Provider) *BillingAccountsServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *BillingAccountsServerBuilder) SetStore(value BillingAccountStore) *BillingAccountsServerBuilder {
	b.store = value
	return b
}

func (b *BillingAccountsServerBuilder) Build() (*BillingAccountsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	delegate, err := NewPrivateBillingAccountsServer().
		SetLogger(b.logger).
		SetProvider(b.providerKey, b.provider).
		SetStore(b.store).
		Build()
	if err != nil {
		return nil, err
	}
	mapper, err := NewGenericMapper[*privatev1.BillingAccount, *publicv1.BillingAccount]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	return &BillingAccountsServer{private: delegate, mapper: mapper, logger: b.logger}, nil
}

func (s *BillingAccountsServer) List(
	ctx context.Context,
	request *publicv1.BillingAccountsListRequest,
) (*publicv1.BillingAccountsListResponse, error) {
	privateRequest := &privatev1.BillingAccountsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items := make([]*publicv1.BillingAccount, 0, len(privateResponse.GetItems()))
	for _, privateItem := range privateResponse.GetItems() {
		item := &publicv1.BillingAccount{}
		if err = s.mapper.Copy(ctx, privateItem, item); err != nil {
			s.logger.ErrorContext(ctx, "failed to map billing account", slog.Any("error", err))
			return nil, grpcstatus.Error(grpccodes.Internal, "failed to map billing account")
		}
		items = append(items, item)
	}
	return publicv1.BillingAccountsListResponse_builder{
		Size:  privateResponse.GetSize(),
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}

func privateProviderState(state billing.ProviderState) privatev1.BillingProviderState {
	switch state {
	case billing.ProviderStateNotConfigured:
		return privatev1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED
	case billing.ProviderStateAvailable:
		return privatev1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE
	case billing.ProviderStateUnavailable:
		return privatev1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE
	default:
		return privatev1.BillingProviderState_BILLING_PROVIDER_STATE_UNSPECIFIED
	}
}

func privateProviderUsageDeliveryState(state billing.UsageDeliveryState) privatev1.BillingUsageDeliveryState {
	switch state {
	case billing.UsageDeliveryStateAvailable:
		return privatev1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_AVAILABLE
	case billing.UsageDeliveryStateUnavailable:
		return privatev1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNAVAILABLE
	default:
		return privatev1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN
	}
}

func privateProviderCapabilities(capabilities []billing.ProviderCapability) []privatev1.BillingProviderCapability {
	result := make([]privatev1.BillingProviderCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		switch capability {
		case billing.ProviderCapabilityPricing:
			result = append(result, privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_PRICING)
		case billing.ProviderCapabilityCosts:
			result = append(result, privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_COSTS)
		case billing.ProviderCapabilityInvoices:
			result = append(result, privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_INVOICES)
		case billing.ProviderCapabilityDraftCharge:
			result = append(result, privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_DRAFT_CHARGES)
		case billing.ProviderCapabilityExports:
			result = append(result, privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_EXPORTS)
		}
	}
	return result
}
