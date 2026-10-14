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
	"sort"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	providerRateCardDefaultLimit = 100
	providerRateCardMaxLimit     = 1000
)

type assignedBillingAccountStore interface {
	GetAssignedAccount(context.Context, string) (billing.AssignedAccount, error)
}

type catalogPricingInputsLister interface {
	List(context.Context, *publicv1.CatalogPricingInputsListRequest) (*publicv1.CatalogPricingInputsListResponse, error)
}

type ProviderRateCardItemsServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.RateCardProvider
	resolver    *CatalogPricingResolver
	store       assignedBillingAccountStore
	catalog     catalogPricingInputsLister
}

type ProviderRateCardItemsServer struct {
	publicv1.UnimplementedProviderRateCardItemsServer
	logger      *slog.Logger
	providerKey string
	provider    billing.RateCardProvider
	resolver    *CatalogPricingResolver
	store       assignedBillingAccountStore
	catalog     catalogPricingInputsLister
}

var _ publicv1.ProviderRateCardItemsServer = (*ProviderRateCardItemsServer)(nil)

func NewProviderRateCardItemsServer() *ProviderRateCardItemsServerBuilder {
	return &ProviderRateCardItemsServerBuilder{}
}

func (b *ProviderRateCardItemsServerBuilder) SetLogger(value *slog.Logger) *ProviderRateCardItemsServerBuilder {
	b.logger = value
	return b
}

func (b *ProviderRateCardItemsServerBuilder) SetProvider(providerKey string, value billing.RateCardProvider) *ProviderRateCardItemsServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *ProviderRateCardItemsServerBuilder) SetPricingResolver(value *CatalogPricingResolver) *ProviderRateCardItemsServerBuilder {
	b.resolver = value
	return b
}

func (b *ProviderRateCardItemsServerBuilder) SetAccountStore(value assignedBillingAccountStore) *ProviderRateCardItemsServerBuilder {
	b.store = value
	return b
}

func (b *ProviderRateCardItemsServerBuilder) SetCatalogPricingInputs(value catalogPricingInputsLister) *ProviderRateCardItemsServerBuilder {
	b.catalog = value
	return b
}

func (b *ProviderRateCardItemsServerBuilder) Build() (*ProviderRateCardItemsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	return &ProviderRateCardItemsServer{
		logger:      b.logger,
		providerKey: b.providerKey,
		provider:    b.provider,
		resolver:    b.resolver,
		store:       b.store,
		catalog:     b.catalog,
	}, nil
}

func (s *ProviderRateCardItemsServer) List(ctx context.Context,
	request *publicv1.ProviderRateCardItemsListRequest,
) (*publicv1.ProviderRateCardItemsListResponse, error) {
	if request == nil {
		request = &publicv1.ProviderRateCardItemsListRequest{}
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	if request.GetFilter() != "" || request.GetOrder() != "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "filter and order are not supported")
	}
	offset, limit, err := providerRateCardPage(request)
	if err != nil {
		return nil, err
	}
	query := billing.RateCardQuery{
		Service:      request.GetService(),
		CurrencyCode: strings.ToUpper(strings.TrimSpace(request.GetCurrencyCode())),
	}
	if request.HasTenantId() {
		if strings.TrimSpace(request.GetTenantId()) == "" {
			return nil, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id must not be empty")
		}
		account, accountErr := s.assignedAccount(ctx, request.GetTenantId())
		if accountErr != nil {
			return nil, accountErr
		}
		query.AccountReference = account.ProviderAccountReference
		accountCurrency := strings.ToUpper(strings.TrimSpace(account.CurrencyCode))
		if query.CurrencyCode != "" && accountCurrency != "" && query.CurrencyCode != accountCurrency {
			return nil, grpcstatus.Error(grpccodes.InvalidArgument, "currency_code must match the billing account currency")
		}
		if query.CurrencyCode == "" {
			query.CurrencyCode = accountCurrency
		}
	}
	var items []billing.RateCardItem
	if s.resolver != nil {
		items, err = s.resolver.listFreshRateCardItems(ctx, query)
	} else {
		items, err = billing.RetryProviderRead(ctx, func(ctx context.Context) ([]billing.RateCardItem, error) {
			return s.provider.ListRateCardItems(ctx, query)
		})
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list provider rate-card items", slog.Any("error", err))
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider rate-card items are unavailable")
	}
	if s.catalog != nil {
		inputs, listErr := listAllCatalogPricingInputs(ctx, s.catalog)
		if listErr != nil {
			s.logger.ErrorContext(ctx, "failed to list OSAC catalog values for provider rate-card items", slog.Any("error", listErr))
			return nil, grpcstatus.Error(grpccodes.Internal, "failed to match provider rate-card items to the OSAC catalog")
		}
		items = enrichRateCardItems(items, inputs)
	}
	total, ok := catalogPricingInputTotal(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many provider rate-card items")
	}
	if offset >= total || limit == 0 {
		return publicv1.ProviderRateCardItemsListResponse_builder{Size: 0, Total: total}.Build(), nil
	}
	end := offset + min(limit, total-offset)
	responseItems := make([]*publicv1.ProviderRateCardItem, 0, end-offset)
	for _, item := range items[offset:end] {
		responseItems = append(responseItems, providerRateCardItemToProto(item))
	}
	return publicv1.ProviderRateCardItemsListResponse_builder{
		Size:  end - offset,
		Total: total,
		Items: responseItems,
	}.Build(), nil
}

func listAllCatalogPricingInputs(ctx context.Context, lister catalogPricingInputsLister) ([]*publicv1.CatalogPricingInput, error) {
	items := make([]*publicv1.CatalogPricingInput, 0)
	var offset int32
	limit := int32(catalogPricingInputSourcePageSize)
	for {
		request := publicv1.CatalogPricingInputsListRequest_builder{Offset: &offset, Limit: &limit}.Build()
		response, err := lister.List(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("catalog pricing input list returned no response")
		}
		items = append(items, response.GetItems()...)
		if response.GetSize() <= 0 {
			return items, nil
		}
		offset += response.GetSize()
		if offset >= response.GetTotal() {
			return items, nil
		}
	}
}

type catalogPricingItemMatch struct {
	name       string
	dimensions map[string]map[string]struct{}
	anyValue   map[string]bool
}

type catalogPricingItemIndex struct {
	items      map[string]map[string]*catalogPricingItemMatch
	dimensions map[string]map[string]struct{}
}

func enrichRateCardItems(items []billing.RateCardItem, inputs []*publicv1.CatalogPricingInput) []billing.RateCardItem {
	index := indexCatalogPricingInputs(inputs)
	result := make([]billing.RateCardItem, len(items))
	for i, item := range items {
		item.Conditions = append([]billing.RateCondition(nil), item.Conditions...)
		item.Conditions = addWildcardRateConditions(item.Service, item.Conditions, index.dimensions)
		item.CatalogItemMatches = catalogItemsForRate(item, index.items)
		result[i] = item
	}
	return result
}

func indexCatalogPricingInputs(inputs []*publicv1.CatalogPricingInput) catalogPricingItemIndex {
	index := catalogPricingItemIndex{
		items:      map[string]map[string]*catalogPricingItemMatch{},
		dimensions: map[string]map[string]struct{}{},
	}
	for _, input := range inputs {
		if input == nil || input.GetService() == "" || input.GetDimension() == "" {
			continue
		}
		if index.dimensions[input.GetService()] == nil {
			index.dimensions[input.GetService()] = map[string]struct{}{}
		}
		index.dimensions[input.GetService()][input.GetDimension()] = struct{}{}
		for _, source := range input.GetCatalogItems() {
			if source == nil || source.GetId() == "" {
				continue
			}
			if index.items[input.GetService()] == nil {
				index.items[input.GetService()] = map[string]*catalogPricingItemMatch{}
			}
			item := index.items[input.GetService()][source.GetId()]
			if item == nil {
				name := source.GetName()
				if name == "" {
					name = source.GetId()
				}
				item = &catalogPricingItemMatch{name: name, dimensions: map[string]map[string]struct{}{}, anyValue: map[string]bool{}}
				index.items[input.GetService()][source.GetId()] = item
			}
			if input.GetAcceptsAnyValue() {
				item.anyValue[input.GetDimension()] = true
			}
			if input.GetValue() != "" {
				if item.dimensions[input.GetDimension()] == nil {
					item.dimensions[input.GetDimension()] = map[string]struct{}{}
				}
				item.dimensions[input.GetDimension()][input.GetValue()] = struct{}{}
			}
		}
	}
	return index
}

func addWildcardRateConditions(service string, conditions []billing.RateCondition, dimensions map[string]map[string]struct{}) []billing.RateCondition {
	seen := make(map[string]struct{}, len(conditions))
	for _, condition := range conditions {
		seen[condition.Dimension] = struct{}{}
	}
	for dimension := range dimensions[service] {
		if _, exists := seen[dimension]; !exists {
			conditions = append(conditions, billing.RateCondition{Dimension: dimension, Operator: billing.RateMatchAny})
		}
	}
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Dimension < conditions[j].Dimension })
	return conditions
}

func catalogItemsForRate(item billing.RateCardItem, catalog map[string]map[string]*catalogPricingItemMatch) []string {
	items := make([]string, 0)
	for _, candidate := range catalog[item.Service] {
		if catalogItemMatchesRate(candidate, item.Conditions) {
			items = append(items, candidate.name)
		}
	}
	sort.Strings(items)
	return items
}

func catalogItemMatchesRate(item *catalogPricingItemMatch, conditions []billing.RateCondition) bool {
	for _, condition := range conditions {
		values, hasDimension := item.dimensions[condition.Dimension]
		if !hasDimension && !item.anyValue[condition.Dimension] {
			return false
		}
		switch condition.Operator {
		case billing.RateMatchAny:
		case billing.RateMatchEquals:
			if !item.anyValue[condition.Dimension] {
				if _, found := values[condition.Value]; !found {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func (s *ProviderRateCardItemsServer) assignedAccount(ctx context.Context, tenantID string) (billing.AssignedAccount, error) {
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

func assignmentIsConnected(state string) bool {
	state = strings.ToUpper(strings.TrimSpace(state))
	// ProtoJSON normally stores enum names, while older stored objects and
	// database fixtures may contain the numeric enum value.
	return state == "CONNECTED" || state == "2" || strings.HasSuffix(state, "_CONNECTED")
}

func providerRateCardPage(request *publicv1.ProviderRateCardItemsListRequest) (int32, int32, error) {
	offset := request.GetOffset()
	if offset < 0 {
		return 0, 0, grpcstatus.Error(grpccodes.InvalidArgument, "offset must not be negative")
	}
	limit := int32(providerRateCardDefaultLimit)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	if limit < 0 || limit > providerRateCardMaxLimit {
		return 0, 0, grpcstatus.Errorf(grpccodes.InvalidArgument, "limit must be between 0 and %d", providerRateCardMaxLimit)
	}
	return offset, limit, nil
}

func providerRateCardItemToProto(item billing.RateCardItem) *publicv1.ProviderRateCardItem {
	conditions := make([]*publicv1.ProviderRateCardCondition, 0, len(item.Conditions))
	for _, condition := range item.Conditions {
		operator := publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_UNSPECIFIED
		switch condition.Operator {
		case billing.RateMatchAny:
			operator = publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY
		case billing.RateMatchEquals:
			operator = publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS
		}
		conditions = append(conditions, publicv1.ProviderRateCardCondition_builder{
			Dimension: condition.Dimension,
			Operator:  operator,
			Value:     condition.Value,
		}.Build())
	}
	source := publicv1.BillingRateCardSource_BILLING_RATE_CARD_SOURCE_UNSPECIFIED
	switch item.Source {
	case billing.RateCardSourceDefault:
		source = publicv1.BillingRateCardSource_BILLING_RATE_CARD_SOURCE_DEFAULT
	case billing.RateCardSourceContract:
		source = publicv1.BillingRateCardSource_BILLING_RATE_CARD_SOURCE_CONTRACT
	}
	return publicv1.ProviderRateCardItem_builder{
		Service:            item.Service,
		CurrencyCode:       item.CurrencyCode,
		Source:             source,
		ResourceConditions: conditions,
		Rate:               publicv1.ProviderRateValue_builder{Amount: item.Amount, Unit: string(item.Unit)}.Build(),
		CatalogItemMatches: item.CatalogItemMatches,
	}.Build()
}
