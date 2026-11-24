/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type QuotaDefaultLimitsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.QuotaDefaultLimitsServer = (*QuotaDefaultLimitsServer)(nil)

// QuotaDefaultLimitsServer is the public face of the default limits, which belong to no tenant.
type QuotaDefaultLimitsServer struct {
	publicv1.UnimplementedQuotaDefaultLimitsServer

	logger    *slog.Logger
	delegate  privatev1.QuotaDefaultLimitsServer
	inMapper  *GenericMapper[*publicv1.QuotaDefaultLimit, *privatev1.QuotaDefaultLimit]
	outMapper *GenericMapper[*privatev1.QuotaDefaultLimit, *publicv1.QuotaDefaultLimit]
}

func NewQuotaDefaultLimitsServer() *QuotaDefaultLimitsServerBuilder {
	return &QuotaDefaultLimitsServerBuilder{}
}

func (b *QuotaDefaultLimitsServerBuilder) SetLogger(value *slog.Logger) *QuotaDefaultLimitsServerBuilder {
	b.logger = value
	return b
}

func (b *QuotaDefaultLimitsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *QuotaDefaultLimitsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *QuotaDefaultLimitsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *QuotaDefaultLimitsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *QuotaDefaultLimitsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *QuotaDefaultLimitsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *QuotaDefaultLimitsServerBuilder) Build() (result *QuotaDefaultLimitsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	inMapper, err := NewGenericMapper[*publicv1.QuotaDefaultLimit, *privatev1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.QuotaDefaultLimit, *publicv1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	delegate, err := NewPrivateQuotaDefaultLimitsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.QuotaDefaultLimit)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}
	result = &QuotaDefaultLimitsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *QuotaDefaultLimitsServer) List(ctx context.Context,
	request *publicv1.QuotaDefaultLimitsListRequest) (response *publicv1.QuotaDefaultLimitsListResponse, err error) {
	privateRequest := &privatev1.QuotaDefaultLimitsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaDefaultLimitsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	items := make([]*publicv1.QuotaDefaultLimit, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		if items[i], err = s.mapOut(ctx, item); err != nil {
			return nil, err
		}
	}
	response.SetItems(items)
	return
}

func (s *QuotaDefaultLimitsServer) Get(ctx context.Context,
	request *publicv1.QuotaDefaultLimitsGetRequest) (response *publicv1.QuotaDefaultLimitsGetResponse, err error) {
	privateRequest := &privatev1.QuotaDefaultLimitsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaDefaultLimitsGetResponse{}
	response.SetObject(object)
	return
}

func (s *QuotaDefaultLimitsServer) Create(ctx context.Context,
	request *publicv1.QuotaDefaultLimitsCreateRequest) (response *publicv1.QuotaDefaultLimitsCreateResponse, err error) {
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaDefaultLimitsCreateRequest{}
	privateRequest.SetObject(object)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	created, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaDefaultLimitsCreateResponse{}
	response.SetObject(created)
	return
}

func (s *QuotaDefaultLimitsServer) Update(ctx context.Context,
	request *publicv1.QuotaDefaultLimitsUpdateRequest) (response *publicv1.QuotaDefaultLimitsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err = validatePublicObjectMetadata(request.GetObject()); err != nil {
		return
	}
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaDefaultLimitsUpdateRequest{}
	privateRequest.SetObject(object)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	updated, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaDefaultLimitsUpdateResponse{}
	response.SetObject(updated)
	return
}

func (s *QuotaDefaultLimitsServer) Delete(ctx context.Context,
	request *publicv1.QuotaDefaultLimitsDeleteRequest) (response *publicv1.QuotaDefaultLimitsDeleteResponse, err error) {
	privateRequest := &privatev1.QuotaDefaultLimitsDeleteRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaDefaultLimitsDeleteResponse{}
	return
}

func (s *QuotaDefaultLimitsServer) mapIn(
	ctx context.Context, object *publicv1.QuotaDefaultLimit,
) (*privatev1.QuotaDefaultLimit, error) {
	result := &privatev1.QuotaDefaultLimit{}
	if err := s.inMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public quota default limit to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process quota default limit")
	}
	return result, nil
}

func (s *QuotaDefaultLimitsServer) mapOut(
	ctx context.Context, object *privatev1.QuotaDefaultLimit,
) (*publicv1.QuotaDefaultLimit, error) {
	result := &publicv1.QuotaDefaultLimit{}
	if err := s.outMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota default limit to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota default limit")
	}
	return result, nil
}
