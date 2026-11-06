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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fetchAllPagesLimit is the page size used while fetching every catalog item for aggregation, equal to the DAO's
// default max limit.
const fetchAllPagesLimit = 1000

// catalogPricingInputFilterEnv is the CEL environment used to evaluate the optional 'filter' request field against
// each aggregated (service, dimension, value) row, following the 'this' convention documented in docs/FILTER.md.
// Built once at package scope: the message type is fixed, so there's nothing per-request to configure. This mirrors
// the in-memory CEL evaluation EventsServer already uses for its Watch filter, applied here to a derived,
// non-DAO-backed list instead of a DAO List().
var catalogPricingInputFilterEnv = newTypedFilterEnv[*privatev1.CatalogPricingInput]()

// PrivateCatalogPricingInputsServerBuilder contains the data and logic needed to create a new private catalog
// pricing inputs server.
type PrivateCatalogPricingInputsServerBuilder struct {
	logger            *slog.Logger
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.CatalogPricingInputsServer = (*PrivateCatalogPricingInputsServer)(nil)

// PrivateCatalogPricingInputsServer lists the distinct metered (service, dimension, value) inventory derived from
// the VMaaS and CaaS catalog item DAOs. It never calls the billing provider: it has no dependency capable of doing
// so.
type PrivateCatalogPricingInputsServer struct {
	privatev1.UnimplementedCatalogPricingInputsServer
	computeInstanceCatalogItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]
	clusterCatalogItemsDao         *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	// inventoryCache keeps one sweep of the aggregated inventory for a short TTL, so repeated Lists do not re-read
	// and re-aggregate every page of both catalog tables on every request. See ttlCache for the semantics.
	inventoryCache *ttlCache[catalogInventoryKey, []*privatev1.CatalogPricingInput]
}

// NewPrivateCatalogPricingInputsServer starts building the private catalog pricing inputs server.
func NewPrivateCatalogPricingInputsServer() *PrivateCatalogPricingInputsServerBuilder {
	return &PrivateCatalogPricingInputsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateCatalogPricingInputsServerBuilder) SetLogger(value *slog.Logger) *PrivateCatalogPricingInputsServerBuilder {
	b.logger = value
	return b
}

// SetTenancyLogic sets the tenancy logic used to build the underlying catalog item DAOs. This is mandatory.
func (b *PrivateCatalogPricingInputsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateCatalogPricingInputsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register DAO metrics.
func (b *PrivateCatalogPricingInputsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateCatalogPricingInputsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build fails when the logger or tenancy logic is missing.
func (b *PrivateCatalogPricingInputsServerBuilder) Build() (result *PrivateCatalogPricingInputsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	computeInstanceCatalogItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
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
	result = &PrivateCatalogPricingInputsServer{
		computeInstanceCatalogItemsDao: computeInstanceCatalogItemsDao,
		clusterCatalogItemsDao:         clusterCatalogItemsDao,
		inventoryCache:                 newCatalogInventoryCache(),
	}
	return
}

// List fetches every draft and published VMaaS and CaaS catalog item, aggregates the distinct (service, dimension,
// value) inventory, applies the request's optional CEL filter, and applies the offset/limit to the resulting
// deterministic list. 'order' is accepted for parity with other list requests but, like everywhere else in this
// API, is not yet implemented (see docs/API.md).
func (s *PrivateCatalogPricingInputsServer) List(ctx context.Context,
	request *privatev1.CatalogPricingInputsListRequest) (*privatev1.CatalogPricingInputsListResponse, error) {
	// One sweep of both catalog tables is reused for the cache's TTL, and concurrent Lists collapse into a single
	// sweep; the aggregated rows are shared read-only with every caller served from the entry. The sweep runs in its
	// own transaction: the fill is detached from every caller's request scope, so it must not borrow the leading
	// caller's request transaction, whose lifetime no longer bounds the sweep. The sweep's rows are published only
	// after WithNewTx returns — committing — because the cache stores the value after the load returns.
	all, err := s.inventoryCache.get(ctx, catalogInventoryKey{}, func(ctx context.Context) ([]*privatev1.CatalogPricingInput, error) {
		return database.WithNewTx(ctx, func(ctx context.Context) ([]*privatev1.CatalogPricingInput, error) {
			return sweepCatalogInventory(ctx, s.computeInstanceCatalogItemsDao, s.clusterCatalogItemsDao)
		})
	})
	if err != nil {
		return nil, err
	}

	all, err = filterItems(catalogPricingInputFilterEnv, all, request.GetFilter())
	if err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %s", err)
	}

	total := len(all)
	page, err := paginate(all, request.GetOffset(), request.GetLimit(), request.HasLimit())
	if err != nil {
		return nil, err
	}

	return privatev1.CatalogPricingInputsListResponse_builder{
		Size:  int32(len(page)), // #nosec G115 -- bounded by catalog size
		Total: int32(total),     // #nosec G115 -- bounded by catalog size
		Items: page,
	}.Build(), nil
}

// fetchAllPages pages through a catalog item DAO until every item, published and draft, has been fetched.
func fetchAllPages[O dao.Object](ctx context.Context, d *dao.GenericDAO[O]) ([]O, error) {
	var all []O
	var offset int32
	for {
		response, err := d.List().SetOffset(offset).SetLimit(fetchAllPagesLimit).Do(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, response.GetItems()...)
		offset += response.GetSize()
		if response.GetSize() == 0 || offset >= response.GetTotal() {
			break
		}
	}
	return all, nil
}

// sweepCatalogInventory reads every draft and published catalog item from both catalog tables and aggregates the
// distinct (service, dimension, value) inventory. Both pricing servers' inventory caches load through it, so the
// sweep lives in one place.
func sweepCatalogInventory(ctx context.Context,
	computeDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem],
	clusterDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]) ([]*privatev1.CatalogPricingInput, error) {
	computeItems, err := fetchAllPages(ctx, computeDao)
	if err != nil {
		return nil, err
	}
	clusterItems, err := fetchAllPages(ctx, clusterDao)
	if err != nil {
		return nil, err
	}
	return aggregateCatalogPricingInputs(computeItems, clusterItems), nil
}
