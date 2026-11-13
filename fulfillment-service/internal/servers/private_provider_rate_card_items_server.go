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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// unsupportedRateCardItemsMetricName is the counter PrivateProviderRateCardItemsServer increments for every
// rate-card row it omits from a List response. It carries provider and reason labels only, never a tenant,
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
	// cacheTTL overrides rateCardCacheDefaultTTL. It is nil when the deployment configures no rate-card cache TTL.
	cacheTTL *time.Duration
	// inventoryCacheTTL overrides catalogInventoryCacheDefaultTTL for the cross-reference inventory. It is nil when
	// the deployment configures no inventory cache TTL.
	inventoryCacheTTL *time.Duration
}

var _ privatev1.ProviderRateCardItemsServer = (*PrivateProviderRateCardItemsServer)(nil)

// PrivateProviderRateCardItemsServer returns provider rate-card rows as currency-specific, provider-neutral rows
// (OSAC-6891). It never returns a provider rule id, rate-card id, service id, native account id, or raw provider
// payload; it only uses those internally to call the adapter.
type PrivateProviderRateCardItemsServer struct {
	privatev1.UnimplementedProviderRateCardItemsServer
	logger                 *slog.Logger
	provider               *billing.Provider
	computeCatalogItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
	clusterCatalogItemsDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	unsupportedRowsCounter *prometheus.CounterVec
	// rateCardCache keeps one successful provider read of a (service, currency) rate card for a short TTL, so an
	// admin List does not repeat the provider's service, priceable-attribute and rate-card reads on every request.
	// See ttlCache for the semantics.
	rateCardCache *ttlCache[rateCardKey, rateCardEntry]
	// inventoryCache keeps one sweep of the catalog inventory and the complete per-service catalog item set used by
	// conditionless provider rows, so a List served from the rate-card cache does not still pay a full catalog-table
	// sweep. See ttlCache for the semantics.
	inventoryCache *ttlCache[catalogInventoryKey, catalogPricingInventory]
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

// SetRateCardCacheTTL sets how long the rate-card cache reuses one provider read. It is optional: an unset value
// keeps rateCardCacheDefaultTTL, and a non-positive value makes Build fail.
func (b *PrivateProviderRateCardItemsServerBuilder) SetRateCardCacheTTL(
	value time.Duration) *PrivateProviderRateCardItemsServerBuilder {
	b.cacheTTL = &value
	return b
}

// SetInventoryCacheTTL sets how long the cross-reference inventory cache reuses one sweep of both catalog tables. It
// is optional: an unset value keeps catalogInventoryCacheDefaultTTL, and a non-positive value makes Build fail.
func (b *PrivateProviderRateCardItemsServerBuilder) SetInventoryCacheTTL(
	value time.Duration) *PrivateProviderRateCardItemsServerBuilder {
	b.inventoryCacheTTL = &value
	return b
}

// Build fails when a mandatory dependency is missing, or when a configured cache TTL is not positive.
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
	cacheTTL := rateCardCacheDefaultTTL
	if b.cacheTTL != nil {
		if *b.cacheTTL <= 0 {
			err = errors.New("cache TTL must be positive")
			return
		}
		cacheTTL = *b.cacheTTL
	}
	inventoryCacheTTL := catalogInventoryCacheDefaultTTL
	if b.inventoryCacheTTL != nil {
		if *b.inventoryCacheTTL <= 0 {
			err = errors.New("inventory cache TTL must be positive")
			return
		}
		inventoryCacheTTL = *b.inventoryCacheTTL
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
		rateCardCache:          newRateCardCache(cacheTTL),
		inventoryCache:         newCatalogPricingInventoryCache(inventoryCacheTTL),
	}
	return
}

// List implements privatev1.ProviderRateCardItemsServer.
func (s *PrivateProviderRateCardItemsServer) List(ctx context.Context,
	request *privatev1.ProviderRateCardItemsListRequest) (*privatev1.ProviderRateCardItemsListResponse, error) {
	if request.GetTenantId() != "" {
		return nil, status.Error(codes.Unimplemented, "tenant_id is not supported until tenant-to-account assignments are available")
	}
	adapter, err := requireAdapter(s.provider)
	if err != nil {
		return nil, err
	}

	currency := request.GetCurrencyCode()

	resolver := pricing.NewResolver(adapter)

	services := []string{request.GetService()}
	if request.GetService() == "" {
		// These are the service mappings used by aggregateCatalogPricingInputs, not the services present in
		// today's inventory: an empty catalog must not hide configured provider rows for either mapped service.
		// Passing service explicitly still queries any provider service by exact name.
		services = []string{computeInstanceServiceName, serviceCluster}
	}

	var allItems []pricing.RateCardItem
	skipped := map[string]int{}
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
			if ctx.Err() != nil {
				return nil, status.FromContextError(ctx.Err()).Err()
			}
			return nil, billingFailure(codes.Unavailable, "billing provider is unavailable", string(probeReason(err)))
		}
		for reason, count := range entry.skipped {
			skipped[reason] += count
		}
		allItems = append(allItems, entry.items...)
	}

	objects := make([]*privatev1.ProviderRateCardItem, 0, len(allItems))
	// Cross-reference every row against all dimensions emitted from catalog field policies, including image and
	// boot-disk condition dimensions. The inventory is swept once per this server's cache TTL and the snapshot is
	// shared read-only among this server's Lists; the dimension index is rebuilt per request from that cached snapshot.
	// The sweep runs in its own transaction: the fill is detached from every caller's request scope, so it must not
	// borrow the leading caller's request transaction, whose lifetime no longer bounds the sweep. The inventory is
	// published only after WithNewTx returns — committing — because the cache stores the value after the load returns.
	//
	// An unknown provider service has no rows to cross-reference, so catalog availability must not prevent its
	// documented empty response. Filtering and pagination still run below, so an empty result is validated alike.
	if len(allItems) > 0 {
		inventory, err := s.inventoryCache.get(ctx, catalogInventoryKey{}, func(ctx context.Context) (catalogPricingInventory, error) {
			return database.WithNewTx(ctx, func(ctx context.Context) (catalogPricingInventory, error) {
				return sweepProviderRateCardCatalogInventory(ctx, s.computeCatalogItemsDao, s.clusterCatalogItemsDao)
			})
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, status.FromContextError(ctx.Err()).Err()
			}
			return nil, err
		}
		catalogIndex := buildCatalogDimensionIndex(inventory)
		for _, item := range allItems {
			objects = append(objects, toProtoRateCardItem(item, matchCatalogItems(item, catalogIndex)))
		}
	}

	objects, err = filterItems(ctx, providerRateCardItemFilterEnv, objects, request.GetFilter())
	if err != nil {
		return nil, filterError(err)
	}

	total := int32(len(objects)) // #nosec G115 -- bounded by the fetched provider rate cards, never a user-controlled count
	objects, err = paginate(objects, request.GetOffset(), request.GetLimit(), request.HasLimit())
	if err != nil {
		return nil, err
	}
	// Count omissions only for successful responses, including those served from cached provider reads.
	for reason, count := range skipped {
		s.unsupportedRowsCounter.WithLabelValues(s.provider.Key(), reason).Add(float64(count))
	}
	return privatev1.ProviderRateCardItemsListResponse_builder{
		Size:  int32(len(objects)), // #nosec G115 -- bounded by total above
		Total: total,
		Items: objects,
	}.Build(), nil
}

// sweepProviderRateCardCatalogInventory keeps all catalog item identities as well as the emitted pricing inputs:
// conditionless provider rows match the full catalog service, including items with no pricing dimensions.
func sweepProviderRateCardCatalogInventory(ctx context.Context,
	computeDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem],
	clusterDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]) (catalogPricingInventory, error) {
	computeItems, err := fetchAllPages(ctx, computeDao)
	if err != nil {
		return catalogPricingInventory{}, err
	}
	clusterItems, err := fetchAllPages(ctx, clusterDao)
	if err != nil {
		return catalogPricingInventory{}, err
	}
	return aggregateCatalogPricingInventory(computeItems, clusterItems), nil
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
		Dimension:          item.Dimension,
		ResourceConditions: conditions,
		Rate:               privatev1.ProviderRateCardItemRate_builder{Amount: item.Amount.String(), Unit: item.Unit}.Build(),
		CatalogItemMatches: catalogMatches,
	}.Build()
}

// paginate follows the shared List contract for an in-memory slice: omitted limit defaults to 100, explicit zero
// is count-only, negative limits are invalid, and limits are capped at 1000. Negative offsets clamp to zero.
// Comparing limit with the remaining length avoids overflowing offset+limit for large int32 inputs.
// `order` remains accepted but unimplemented, as documented for other Lists in docs/API.md.
func paginate[T any](items []T, offset, limit int32, hasLimit bool) ([]T, error) {
	if !hasLimit {
		limit = 100
	}
	if limit < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit must not be negative")
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	if limit == 0 || int(offset) >= len(items) {
		return nil, nil
	}
	items = items[offset:]
	if int(limit) < len(items) {
		items = items[:limit]
	}
	return items, nil
}
