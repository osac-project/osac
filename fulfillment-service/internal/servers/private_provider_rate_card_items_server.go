/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

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

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// unsupportedRateCardItemsMetricName is the counter PrivateProviderRateCardItemsServer increments for every
// rate-card row it omits from a List response (AC3). It carries provider and reason labels only, never a tenant,
// service name, or any other detail that could make the metric high-cardinality or leak request content.
const unsupportedRateCardItemsMetricName = "osac_fulfillment_billing_rate_card_items_unsupported_total"

// providerRateCardItemFilterEnv is the CEL environment used to evaluate the optional 'filter' request field against
// each provider-neutral row, following the 'this' convention documented in docs/FILTER.md. Built once at package
// scope through the shared newTypedFilterEnv, mirroring catalogPricingInputFilterEnv: the message type is fixed, so
// there's nothing per-request to configure.
var providerRateCardItemFilterEnv = newTypedFilterEnv[*privatev1.ProviderRateCardItem]()

// PrivateProviderRateCardItemsServerBuilder builds a PrivateProviderRateCardItemsServer.
type PrivateProviderRateCardItemsServerBuilder struct {
	logger            *slog.Logger
	provider          *billing.Provider
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ProviderRateCardItemsServer = (*PrivateProviderRateCardItemsServer)(nil)

// PrivateProviderRateCardItemsServer returns provider rate-card rows as currency-specific, provider-neutral rows
// (OSAC-6891). It never returns a provider rule id, rate-card id, service id, native account id, or raw provider
// payload (AC7); it only uses those internally to call the adapter.
type PrivateProviderRateCardItemsServer struct {
	privatev1.UnimplementedProviderRateCardItemsServer
	logger                 *slog.Logger
	provider               *billing.Provider
	computeCatalogItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
	clusterCatalogItemsDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	unsupportedRowsCounter *prometheus.CounterVec
	// rateCardCache keeps one successful provider read of a (service, currency) rate card for a short TTL, so an
	// admin List does not repeat the provider's paginated service, priceable-attribute and rate-card sweeps on
	// every request. See ttlCache for the semantics.
	rateCardCache *ttlCache[rateCardKey, rateCardEntry]
	// inventoryCache keeps one sweep of the aggregated catalog inventory the cross-reference matches rows against,
	// for a short TTL, so a List served from the rate-card cache does not still pay a full catalog-table sweep. See
	// ttlCache for the semantics.
	inventoryCache *ttlCache[catalogInventoryKey, []*privatev1.CatalogPricingInput]
}

// NewPrivateProviderRateCardItemsServer starts building the private server.
func NewPrivateProviderRateCardItemsServer() *PrivateProviderRateCardItemsServerBuilder {
	return &PrivateProviderRateCardItemsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateProviderRateCardItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateProviderRateCardItemsServerBuilder {
	b.logger = value
	return b
}

// SetProvider sets the billing provider. This is mandatory.
func (b *PrivateProviderRateCardItemsServerBuilder) SetProvider(value *billing.Provider) *PrivateProviderRateCardItemsServerBuilder {
	b.provider = value
	return b
}

// SetTenancyLogic sets the tenancy logic used by the catalog item DAOs this server reads for catalog_item_matches.
// This is mandatory.
func (b *PrivateProviderRateCardItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateProviderRateCardItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer. This is mandatory.
func (b *PrivateProviderRateCardItemsServerBuilder) SetMetricsRegisterer(
	value prometheus.Registerer) *PrivateProviderRateCardItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build fails when a mandatory dependency is missing.
func (b *PrivateProviderRateCardItemsServerBuilder) Build() (result *PrivateProviderRateCardItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.provider == nil {
		err = errors.New("provider is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	computeCatalogItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	clusterCatalogItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: unsupportedRateCardItemsMetricName,
		Help: "Provider rate-card rows omitted from ProviderRateCardItems.List because their shape is unsupported.",
	}, []string{"provider", "reason"})
	if b.metricsRegisterer != nil {
		if err = b.metricsRegisterer.Register(counter); err != nil {
			var are prometheus.AlreadyRegisteredError
			if errors.As(err, &are) {
				counter = are.ExistingCollector.(*prometheus.CounterVec)
				err = nil
			} else {
				return
			}
		}
	}
	result = &PrivateProviderRateCardItemsServer{
		logger:                 b.logger,
		provider:               b.provider,
		computeCatalogItemsDao: computeCatalogItemsDao,
		clusterCatalogItemsDao: clusterCatalogItemsDao,
		unsupportedRowsCounter: counter,
		rateCardCache:          newRateCardCache(),
		inventoryCache:         newCatalogInventoryCache(),
	}
	return
}

// List implements privatev1.ProviderRateCardItemsServer.
func (s *PrivateProviderRateCardItemsServer) List(ctx context.Context,
	request *privatev1.ProviderRateCardItemsListRequest) (*privatev1.ProviderRateCardItemsListResponse, error) {
	if _, err := requireAdapter(s.provider); err != nil {
		return nil, err
	}

	currency := request.GetCurrencyCode()
	if currency == "" {
		// Reject the malformed request before the tenant validation, so it cannot pay a provider ListAccounts
		// round trip only to be rejected locally.
		return nil, currencyUnresolvedError()
	}
	if tenantID := request.GetTenantId(); tenantID != "" {
		if err := s.validateTenantAccount(ctx, tenantID); err != nil {
			return nil, err
		}
		// OSAC applies no separate override tier (AC4): the rows for a validated tenant are the same globally
		// configured rows as the no-tenant path, so no account-specific lookup follows here.
	}

	adapter, err := s.provider.Adapter()
	if err != nil {
		return nil, classifyBillingError(err)
	}
	resolver := pricing.NewResolver(adapter)

	services := []string{request.GetService()}
	if request.GetService() == "" {
		// Scoped limitation (OSAC-6891): without OSAC-6889's CatalogPricingInputs.List, OSAC has no registry of
		// every mapped service name in this codebase yet, so an omitted `service` filter lists compute_instance
		// only. Passing `service` explicitly queries any provider service by exact name.
		services = []string{computeInstanceServiceName}
	}

	var allItems []pricing.RateCardItem
	for _, service := range services {
		// One provider read of a (service, currency) rate card is reused for the cache's TTL, and concurrent Lists
		// of the same key collapse into a single read; the skipped-row counts are re-applied per response so the
		// metric keeps its per-response meaning on a cache hit. A failed read is never cached and never served from
		// a stale entry, preserving the uncached failure semantics.
		entry, err := s.rateCardCache.get(ctx, rateCardKey{service: service, currency: currency},
			func(ctx context.Context) (rateCardEntry, error) {
				items, skipped, err := resolver.ListServiceRateCardItems(ctx, service, currency)
				if err != nil {
					return rateCardEntry{}, err
				}
				return rateCardEntry{items: items, skipped: skipped}, nil
			})
		if err != nil {
			return nil, classifyBillingError(err)
		}
		for reason, count := range entry.skipped {
			s.unsupportedRowsCounter.WithLabelValues(s.provider.Key(), reason).Add(float64(count))
		}
		allItems = append(allItems, entry.items...)
	}

	// Cross-reference every row against the full (service, dimension, value) catalog inventory OSAC-6889's
	// aggregateCatalogPricingInputs computes (AC5/AC6), across all five documented dimensions, not just
	// compute_instance/instance_type. The inventory is swept once per this server's cache TTL and the rows are
	// shared read-only among this server's Lists; the dimension index is rebuilt per request from the cached rows,
	// which is in-memory work over an already-read slice.
	inventory, err := s.inventoryCache.get(ctx, catalogInventoryKey{}, func(ctx context.Context) ([]*privatev1.CatalogPricingInput, error) {
		return sweepCatalogInventory(ctx, s.computeCatalogItemsDao, s.clusterCatalogItemsDao)
	})
	if err != nil {
		return nil, err
	}
	catalogIndex := buildCatalogDimensionIndex(inventory)

	objects := make([]*privatev1.ProviderRateCardItem, 0, len(allItems))
	for _, item := range allItems {
		objects = append(objects, toProtoRateCardItem(item, matchCatalogItems(item, catalogIndex)))
	}

	objects, err = filterItems(providerRateCardItemFilterEnv, objects, request.GetFilter())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid filter: %s", err)
	}

	total := int32(len(objects)) // #nosec G115 -- bounded by one provider rate card, never a user-controlled count
	objects = paginate(objects, request.GetOffset(), request.GetLimit())
	return privatev1.ProviderRateCardItemsListResponse_builder{
		Size:  int32(len(objects)), // #nosec G115 -- bounded by total above
		Total: total,
		Items: objects,
	}.Build(), nil
}

// validateTenantAccount checks that tenantID identifies exactly one active provider account, matching it the same
// way the design's day-one tenant/account pairing does: against the account's correlation value (ExternalID). This
// story's worktree has no persisted tenant-to-account assignment store yet (that is a separate, not-yet-landed
// story in this epic), so tenantID is matched directly as that correlation value rather than looked up through one.
// The check is deliberately a live provider read on every tenant-scoped List, never cached: an account can be
// unlinked or deactivated between requests, and serving a remembered verdict would keep a just-disconnected
// account's rates listable for a cache TTL, so each List pays one ListAccounts discovery call instead.
func (s *PrivateProviderRateCardItemsServer) validateTenantAccount(ctx context.Context, tenantID string) error {
	adapter, err := s.provider.Adapter()
	if err != nil {
		return classifyBillingError(err)
	}
	page, err := adapter.ListAccounts(ctx, billing.AccountFilter{ExternalID: tenantID}, billing.Page{Size: 2})
	if err != nil {
		return classifyBillingError(err)
	}
	switch len(page.Accounts) {
	case 0:
		return tenantValidationError(tenantID, "no billing account is linked to this tenant")
	case 1:
		if page.Accounts[0].State != billing.AccountActive {
			return tenantValidationError(tenantID, "the tenant's linked billing account is not active")
		}
		return nil
	default:
		return tenantValidationError(tenantID, "more than one billing account is linked to this tenant")
	}
}

func tenantValidationError(tenantID, reason string) error {
	result := status.Newf(codes.InvalidArgument, "tenant_id %q is invalid: %s", tenantID, reason)
	if detailed, err := result.WithDetails(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "tenant_id", Description: reason}},
	}); err == nil {
		result = detailed
	}
	return result.Err()
}

func currencyUnresolvedError() error {
	result := status.New(codes.InvalidArgument,
		"currency_code is required: no billing account currency is available to resolve it")
	if detailed, err := result.WithDetails(&errdetails.ErrorInfo{Reason: pricing.ReasonCurrencyUnresolved}); err == nil {
		result = detailed
	}
	return result.Err()
}

// classifyBillingError maps an adapter failure to UNAVAILABLE (AC8): a provider failure never serves a cached or
// guessed card as current. It reuses probeReason's *billing.Error extraction (private_billing_provider_statuses_
// server.go), the same classification BillingProviderStatuses already applies to a provider failure.
func classifyBillingError(err error) error {
	result := status.New(codes.Unavailable, "billing provider is unavailable")
	if detailed, werr := result.WithDetails(&errdetails.ErrorInfo{Reason: string(probeReason(err))}); werr == nil {
		result = detailed
	}
	return result.Err()
}

// toProtoRateCardItem converts one normalized pricing.RateCardItem into its wire shape.
func toProtoRateCardItem(item pricing.RateCardItem, catalogMatches []string) *privatev1.ProviderRateCardItem {
	conditions := make([]*privatev1.ProviderRateCardItemResourceCondition, 0, len(item.Conditions))
	for _, c := range item.Conditions {
		operator := privatev1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY
		if c.Operator == pricing.OperatorEquals {
			operator = privatev1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS
		}
		conditions = append(conditions, privatev1.ProviderRateCardItemResourceCondition_builder{
			Dimension: c.Dimension,
			Operator:  operator,
			Value:     c.Value,
		}.Build())
	}
	return privatev1.ProviderRateCardItem_builder{
		Service:            item.Service,
		CurrencyCode:       item.Currency,
		ResourceConditions: conditions,
		Rate:               privatev1.ProviderRateCardItemRate_builder{Amount: item.Amount.String(), Unit: item.Unit}.Build(),
		CatalogItemMatches: catalogMatches,
	}.Build()
}

// paginate applies a list request's offset/limit over an already-computed, in-memory slice: a negative offset is
// clamped to zero, a limit of zero or less means everything, and an offset past the end yields an empty page. It
// compares the limit against the remaining length instead of adding offset and limit, so large int32 values cannot
// overflow the arithmetic and panic the handler with a slice bound error. `order` is accepted for API consistency
// with every other List method but, like everywhere else in this API, is not yet implemented (see docs/API.md).
func paginate[T any](items []T, offset, limit int32) []T {
	if offset < 0 {
		offset = 0
	}
	if int(offset) >= len(items) {
		return nil
	}
	items = items[offset:]
	if limit > 0 && int(limit) < len(items) {
		items = items[:limit]
	}
	return items
}
