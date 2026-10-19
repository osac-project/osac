/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	catalogPricingInputSourcePageSize = 500
	catalogPricingInputDefaultLimit   = 100
	catalogPricingInputMaxLimit       = 1000
)

type computeCatalogItemsLister interface {
	List(context.Context, *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error)
}

type clusterCatalogItemsLister interface {
	List(context.Context, *publicv1.ClusterCatalogItemsListRequest) (*publicv1.ClusterCatalogItemsListResponse, error)
}

type CatalogPricingInputsServerBuilder struct {
	logger       *slog.Logger
	computeItems computeCatalogItemsLister
	clusterItems clusterCatalogItemsLister
}

type CatalogPricingInputsServer struct {
	publicv1.UnimplementedCatalogPricingInputsServer
	logger       *slog.Logger
	computeItems computeCatalogItemsLister
	clusterItems clusterCatalogItemsLister
}

var _ publicv1.CatalogPricingInputsServer = (*CatalogPricingInputsServer)(nil)

func NewCatalogPricingInputsServer() *CatalogPricingInputsServerBuilder {
	return &CatalogPricingInputsServerBuilder{}
}

func (b *CatalogPricingInputsServerBuilder) SetLogger(value *slog.Logger) *CatalogPricingInputsServerBuilder {
	b.logger = value
	return b
}

func (b *CatalogPricingInputsServerBuilder) SetComputeInstanceCatalogItems(value computeCatalogItemsLister) *CatalogPricingInputsServerBuilder {
	b.computeItems = value
	return b
}

func (b *CatalogPricingInputsServerBuilder) SetClusterCatalogItems(value clusterCatalogItemsLister) *CatalogPricingInputsServerBuilder {
	b.clusterItems = value
	return b
}

func (b *CatalogPricingInputsServerBuilder) Build() (*CatalogPricingInputsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	return &CatalogPricingInputsServer{
		logger:       b.logger,
		computeItems: b.computeItems,
		clusterItems: b.clusterItems,
	}, nil
}

func (s *CatalogPricingInputsServer) List(ctx context.Context,
	request *publicv1.CatalogPricingInputsListRequest,
) (*publicv1.CatalogPricingInputsListResponse, error) {
	if request == nil {
		request = &publicv1.CatalogPricingInputsListRequest{}
	}
	offset, limit, err := catalogPricingInputPage(request)
	if err != nil {
		return nil, err
	}
	if request.GetFilter() != "" || request.GetOrder() != "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "filter and order are not supported")
	}

	// This API inventories OSAC catalog values only. Its internal catalog reads
	// must not trigger provider pricing lookups.
	ctx = withoutCatalogPricing(ctx)
	computeItems, err := listComputeCatalogItems(ctx, s.computeItems)
	if err != nil {
		return nil, s.listError(ctx, "compute instance", err)
	}
	clusterItems, err := listClusterCatalogItems(ctx, s.clusterItems)
	if err != nil {
		return nil, s.listError(ctx, "cluster", err)
	}
	computeDefaults, computeDefaultErrors := loadCatalogPricingTemplateDefaults(
		ctx, templateDefaultsReaderFrom(s.computeItems), catalogPricingTemplateReferencesFromComputeItems(computeItems),
	)
	clusterDefaults, clusterDefaultErrors := loadCatalogPricingTemplateDefaults(
		ctx, templateDefaultsReaderFrom(s.clusterItems), catalogPricingTemplateReferencesFromClusterItems(clusterItems),
	)
	if len(computeDefaultErrors) != 0 || len(clusterDefaultErrors) != 0 {
		s.logger.ErrorContext(ctx, "failed to load catalog template defaults for pricing inventory")
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to load catalog pricing inputs")
	}
	items := billing.CatalogPricingInputsFromCatalogWithTemplateDefaults(
		computeItems,
		clusterItems,
		nil,
		billing.CatalogPricingInputTemplateDefaults{
			ComputeInstances: computeDefaults,
			Clusters:         clusterDefaults,
		},
	)
	total, ok := catalogPricingInputTotal(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many catalog pricing inputs")
	}
	if offset >= total || limit == 0 {
		return publicv1.CatalogPricingInputsListResponse_builder{
			Size:  0,
			Total: total,
		}.Build(), nil
	}
	end := offset + min(limit, total-offset)
	page := items[offset:end]
	return publicv1.CatalogPricingInputsListResponse_builder{
		Size:  end - offset,
		Total: total,
		Items: page,
	}.Build(), nil
}

func catalogPricingInputTotal(length int) (int32, bool) {
	var total int32
	for i := 0; i < length; i++ {
		if total == 1<<31-1 {
			return 0, false
		}
		total++
	}
	return total, true
}

func catalogPricingInputPage(request *publicv1.CatalogPricingInputsListRequest) (int32, int32, error) {
	offset := request.GetOffset()
	if offset < 0 {
		return 0, 0, grpcstatus.Error(grpccodes.InvalidArgument, "offset must not be negative")
	}
	limit := int32(catalogPricingInputDefaultLimit)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	if limit < 0 || limit > catalogPricingInputMaxLimit {
		return 0, 0, grpcstatus.Errorf(grpccodes.InvalidArgument, "limit must be between 0 and %d", catalogPricingInputMaxLimit)
	}
	return offset, limit, nil
}

func (s *CatalogPricingInputsServer) listError(ctx context.Context, service string, err error) error {
	s.logger.ErrorContext(ctx, "failed to list catalog items for pricing inventory", slog.String("service", service), slog.Any("error", err))
	return grpcstatus.Error(grpccodes.Internal, "failed to list catalog pricing inputs")
}

func listComputeCatalogItems(ctx context.Context, lister computeCatalogItemsLister) ([]*publicv1.ComputeInstanceCatalogItem, error) {
	if lister == nil {
		return nil, nil
	}
	items := make([]*publicv1.ComputeInstanceCatalogItem, 0)
	for offset := int32(0); ; {
		request := &publicv1.ComputeInstanceCatalogItemsListRequest{}
		request.SetOffset(offset)
		request.SetLimit(catalogPricingInputSourcePageSize)
		response, err := lister.List(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("compute instance catalog list returned no response")
		}
		items = append(items, response.GetItems()...)
		offset += response.GetSize()
		if response.GetSize() == 0 || offset >= response.GetTotal() {
			return items, nil
		}
	}
}

func listClusterCatalogItems(ctx context.Context, lister clusterCatalogItemsLister) ([]*publicv1.ClusterCatalogItem, error) {
	if lister == nil {
		return nil, nil
	}
	items := make([]*publicv1.ClusterCatalogItem, 0)
	for offset := int32(0); ; {
		request := &publicv1.ClusterCatalogItemsListRequest{}
		request.SetOffset(offset)
		request.SetLimit(catalogPricingInputSourcePageSize)
		response, err := lister.List(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("cluster catalog list returned no response")
		}
		items = append(items, response.GetItems()...)
		offset += response.GetSize()
		if response.GetSize() == 0 || offset >= response.GetTotal() {
			return items, nil
		}
	}
}
