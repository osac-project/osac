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

type QuotaThresholdsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.QuotaThresholdsServer = (*QuotaThresholdsServer)(nil)

// QuotaThresholdsServer is the public face of the warning thresholds.
type QuotaThresholdsServer struct {
	publicv1.UnimplementedQuotaThresholdsServer

	logger    *slog.Logger
	delegate  privatev1.QuotaThresholdsServer
	inMapper  *GenericMapper[*publicv1.QuotaThreshold, *privatev1.QuotaThreshold]
	outMapper *GenericMapper[*privatev1.QuotaThreshold, *publicv1.QuotaThreshold]
}

func NewQuotaThresholdsServer() *QuotaThresholdsServerBuilder {
	return &QuotaThresholdsServerBuilder{}
}

func (b *QuotaThresholdsServerBuilder) SetLogger(value *slog.Logger) *QuotaThresholdsServerBuilder {
	b.logger = value
	return b
}

func (b *QuotaThresholdsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *QuotaThresholdsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *QuotaThresholdsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *QuotaThresholdsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *QuotaThresholdsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *QuotaThresholdsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *QuotaThresholdsServerBuilder) Build() (result *QuotaThresholdsServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.QuotaThreshold, *privatev1.QuotaThreshold]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.QuotaThreshold, *publicv1.QuotaThreshold]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	delegate, err := NewPrivateQuotaThresholdsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.QuotaThreshold)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}
	result = &QuotaThresholdsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *QuotaThresholdsServer) List(ctx context.Context,
	request *publicv1.QuotaThresholdsListRequest) (response *publicv1.QuotaThresholdsListResponse, err error) {
	privateRequest := &privatev1.QuotaThresholdsListRequest{}
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
	response = &publicv1.QuotaThresholdsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	items := make([]*publicv1.QuotaThreshold, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		if items[i], err = s.mapOut(ctx, item); err != nil {
			return nil, err
		}
	}
	response.SetItems(items)
	return
}

func (s *QuotaThresholdsServer) Get(ctx context.Context,
	request *publicv1.QuotaThresholdsGetRequest) (response *publicv1.QuotaThresholdsGetResponse, err error) {
	privateRequest := &privatev1.QuotaThresholdsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaThresholdsGetResponse{}
	response.SetObject(object)
	return
}

func (s *QuotaThresholdsServer) Create(ctx context.Context,
	request *publicv1.QuotaThresholdsCreateRequest) (response *publicv1.QuotaThresholdsCreateResponse, err error) {
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaThresholdsCreateRequest{}
	privateRequest.SetObject(object)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	created, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaThresholdsCreateResponse{}
	response.SetObject(created)
	return
}

func (s *QuotaThresholdsServer) Update(ctx context.Context,
	request *publicv1.QuotaThresholdsUpdateRequest) (response *publicv1.QuotaThresholdsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err = validatePublicObjectMetadata(request.GetObject()); err != nil {
		return
	}
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaThresholdsUpdateRequest{}
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
	response = &publicv1.QuotaThresholdsUpdateResponse{}
	response.SetObject(updated)
	return
}

func (s *QuotaThresholdsServer) Delete(ctx context.Context,
	request *publicv1.QuotaThresholdsDeleteRequest) (response *publicv1.QuotaThresholdsDeleteResponse, err error) {
	privateRequest := &privatev1.QuotaThresholdsDeleteRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaThresholdsDeleteResponse{}
	return
}

func (s *QuotaThresholdsServer) mapIn(
	ctx context.Context, object *publicv1.QuotaThreshold,
) (*privatev1.QuotaThreshold, error) {
	result := &privatev1.QuotaThreshold{}
	if err := s.inMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public quota threshold to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process quota threshold")
	}
	return result, nil
}

func (s *QuotaThresholdsServer) mapOut(
	ctx context.Context, object *privatev1.QuotaThreshold,
) (*publicv1.QuotaThreshold, error) {
	result := &publicv1.QuotaThreshold{}
	if err := s.outMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota threshold to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota threshold")
	}
	return result, nil
}
