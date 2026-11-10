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

type QuotaLimitRequestsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.QuotaLimitRequestsServer = (*QuotaLimitRequestsServer)(nil)

// QuotaLimitRequestsServer is the public face of the limit increase requests.
type QuotaLimitRequestsServer struct {
	publicv1.UnimplementedQuotaLimitRequestsServer

	logger    *slog.Logger
	delegate  privatev1.QuotaLimitRequestsServer
	inMapper  *GenericMapper[*publicv1.QuotaLimitRequest, *privatev1.QuotaLimitRequest]
	outMapper *GenericMapper[*privatev1.QuotaLimitRequest, *publicv1.QuotaLimitRequest]
}

func NewQuotaLimitRequestsServer() *QuotaLimitRequestsServerBuilder {
	return &QuotaLimitRequestsServerBuilder{}
}

func (b *QuotaLimitRequestsServerBuilder) SetLogger(value *slog.Logger) *QuotaLimitRequestsServerBuilder {
	b.logger = value
	return b
}

func (b *QuotaLimitRequestsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *QuotaLimitRequestsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *QuotaLimitRequestsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *QuotaLimitRequestsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *QuotaLimitRequestsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *QuotaLimitRequestsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *QuotaLimitRequestsServerBuilder) Build() (result *QuotaLimitRequestsServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.QuotaLimitRequest, *privatev1.QuotaLimitRequest]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.QuotaLimitRequest, *publicv1.QuotaLimitRequest]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	delegate, err := NewPrivateQuotaLimitRequestsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.QuotaLimitRequest)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}
	result = &QuotaLimitRequestsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *QuotaLimitRequestsServer) List(ctx context.Context,
	request *publicv1.QuotaLimitRequestsListRequest) (response *publicv1.QuotaLimitRequestsListResponse, err error) {
	privateRequest := &privatev1.QuotaLimitRequestsListRequest{}
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
	response = &publicv1.QuotaLimitRequestsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	items := make([]*publicv1.QuotaLimitRequest, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		if items[i], err = s.mapOut(ctx, item); err != nil {
			return nil, err
		}
	}
	response.SetItems(items)
	return
}

func (s *QuotaLimitRequestsServer) Get(ctx context.Context,
	request *publicv1.QuotaLimitRequestsGetRequest) (response *publicv1.QuotaLimitRequestsGetResponse, err error) {
	privateRequest := &privatev1.QuotaLimitRequestsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitRequestsGetResponse{}
	response.SetObject(object)
	return
}

func (s *QuotaLimitRequestsServer) Create(ctx context.Context,
	request *publicv1.QuotaLimitRequestsCreateRequest) (response *publicv1.QuotaLimitRequestsCreateResponse, err error) {
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaLimitRequestsCreateRequest{}
	privateRequest.SetObject(object)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	created, err := s.mapOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitRequestsCreateResponse{}
	response.SetObject(created)
	return
}

func (s *QuotaLimitRequestsServer) Update(ctx context.Context,
	request *publicv1.QuotaLimitRequestsUpdateRequest) (response *publicv1.QuotaLimitRequestsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err = validatePublicObjectMetadata(request.GetObject()); err != nil {
		return
	}
	object, err := s.mapIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaLimitRequestsUpdateRequest{}
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
	response = &publicv1.QuotaLimitRequestsUpdateResponse{}
	response.SetObject(updated)
	return
}

func (s *QuotaLimitRequestsServer) Delete(ctx context.Context,
	request *publicv1.QuotaLimitRequestsDeleteRequest) (response *publicv1.QuotaLimitRequestsDeleteResponse, err error) {
	privateRequest := &privatev1.QuotaLimitRequestsDeleteRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitRequestsDeleteResponse{}
	return
}

func (s *QuotaLimitRequestsServer) Approve(ctx context.Context,
	request *publicv1.QuotaLimitRequestsApproveRequest) (response *publicv1.QuotaLimitRequestsApproveResponse, err error) {
	privateRequest := &privatev1.QuotaLimitRequestsApproveRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Approve(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitRequestsApproveResponse{}
	return
}

func (s *QuotaLimitRequestsServer) Deny(ctx context.Context,
	request *publicv1.QuotaLimitRequestsDenyRequest) (response *publicv1.QuotaLimitRequestsDenyResponse, err error) {
	privateRequest := &privatev1.QuotaLimitRequestsDenyRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Deny(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitRequestsDenyResponse{}
	return
}

func (s *QuotaLimitRequestsServer) mapIn(
	ctx context.Context, object *publicv1.QuotaLimitRequest,
) (*privatev1.QuotaLimitRequest, error) {
	result := &privatev1.QuotaLimitRequest{}
	if err := s.inMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public quota limit request to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process quota limit request")
	}
	return result, nil
}

func (s *QuotaLimitRequestsServer) mapOut(
	ctx context.Context, object *privatev1.QuotaLimitRequest,
) (*publicv1.QuotaLimitRequest, error) {
	result := &publicv1.QuotaLimitRequest{}
	if err := s.outMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota limit request to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota limit request")
	}
	return result, nil
}
