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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
)

type ExternalIPPoolsServerBuilder struct {
	logger            *slog.Logger
	notifier          *database.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ExternalIPPoolsServer = (*ExternalIPPoolsServer)(nil)

type ExternalIPPoolsServer struct {
	publicv1.UnimplementedExternalIPPoolsServer

	logger    *slog.Logger
	delegate  privatev1.ExternalIPPoolsServer
	outMapper *GenericMapper[*privatev1.ExternalIPPool, *publicv1.ExternalIPPool]
}

func NewExternalIPPoolsServer() *ExternalIPPoolsServerBuilder {
	return &ExternalIPPoolsServerBuilder{}
}

func (b *ExternalIPPoolsServerBuilder) SetLogger(value *slog.Logger) *ExternalIPPoolsServerBuilder {
	b.logger = value
	return b
}

func (b *ExternalIPPoolsServerBuilder) SetNotifier(value *database.Notifier) *ExternalIPPoolsServerBuilder {
	b.notifier = value
	return b
}

func (b *ExternalIPPoolsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ExternalIPPoolsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *ExternalIPPoolsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ExternalIPPoolsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *ExternalIPPoolsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ExternalIPPoolsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ExternalIPPoolsServerBuilder) Build() (result *ExternalIPPoolsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	outMapper, err := NewGenericMapper[*privatev1.ExternalIPPool, *publicv1.ExternalIPPool]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateExternalIPPoolsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ExternalIPPoolsServer{
		logger:    b.logger,
		delegate:  delegate,
		outMapper: outMapper,
	}
	return
}

func isExternalIPPoolPubliclyVisible(p *privatev1.ExternalIPPool) bool {
	return p.GetStatus().GetState() == privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY &&
		p.GetStatus().GetAvailable() > 0
}

func (s *ExternalIPPoolsServer) List(ctx context.Context,
	request *publicv1.ExternalIPPoolsListRequest) (response *publicv1.ExternalIPPoolsListResponse, err error) {
	privateRequest := &privatev1.ExternalIPPoolsListRequest{}
	privateRequest.SetFilter(request.GetFilter())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	var eligible []*privatev1.ExternalIPPool
	for _, p := range privateResponse.GetItems() {
		if isExternalIPPoolPubliclyVisible(p) {
			eligible = append(eligible, p)
		}
	}

	total := int32(len(eligible)) // #nosec G115 -- bounded by pool size
	offset := request.GetOffset()
	limit := request.GetLimit()
	if offset < 0 || limit < 0 {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "offset and limit must be non-negative")
	}
	if offset > total {
		offset = total
	}
	paged := eligible[offset:]
	if limit > 0 && int32(len(paged)) > limit { // #nosec G115 -- bounded by pool size
		paged = paged[:limit]
	}

	publicItems := make([]*publicv1.ExternalIPPool, len(paged))
	for i, privateItem := range paged {
		publicItem := &publicv1.ExternalIPPool{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private external IP pool to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP pools")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ExternalIPPoolsListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by pool size
	response.SetTotal(total)
	response.SetItems(publicItems)
	return
}

func (s *ExternalIPPoolsServer) Get(ctx context.Context,
	request *publicv1.ExternalIPPoolsGetRequest) (response *publicv1.ExternalIPPoolsGetResponse, err error) {
	privateRequest := &privatev1.ExternalIPPoolsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	pool := privateResponse.GetObject()
	if !isExternalIPPoolPubliclyVisible(pool) {
		s.logger.DebugContext(ctx, "Pool not eligible for public API",
			slog.String("id", request.GetId()),
		)
		return nil, grpcstatus.Errorf(grpccodes.NotFound, "external IP pool not found")
	}

	publicPool := &publicv1.ExternalIPPool{}
	err = s.outMapper.Copy(ctx, pool, publicPool)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private external IP pool to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP pool")
	}

	response = &publicv1.ExternalIPPoolsGetResponse{}
	response.SetObject(publicPool)
	return
}
