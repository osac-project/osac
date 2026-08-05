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
	"github.com/osac-project/fulfillment-service/internal/events"
)

type ExternalIPsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ExternalIPsServer = (*ExternalIPsServer)(nil)

type ExternalIPsServer struct {
	publicv1.UnimplementedExternalIPsServer

	logger    *slog.Logger
	delegate  privatev1.ExternalIPsServer
	inMapper  *GenericMapper[*publicv1.ExternalIP, *privatev1.ExternalIP]
	outMapper *GenericMapper[*privatev1.ExternalIP, *publicv1.ExternalIP]
}

func NewExternalIPsServer() *ExternalIPsServerBuilder {
	return &ExternalIPsServerBuilder{}
}

func (b *ExternalIPsServerBuilder) SetLogger(value *slog.Logger) *ExternalIPsServerBuilder {
	b.logger = value
	return b
}

func (b *ExternalIPsServerBuilder) SetNotifier(value events.Notifier) *ExternalIPsServerBuilder {
	b.notifier = value
	return b
}

func (b *ExternalIPsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ExternalIPsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *ExternalIPsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ExternalIPsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *ExternalIPsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ExternalIPsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ExternalIPsServerBuilder) Build() (result *ExternalIPsServer, err error) {
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

	inMapper, err := NewGenericMapper[*publicv1.ExternalIP, *privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ExternalIP, *publicv1.ExternalIP]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateExternalIPsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ExternalIPsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ExternalIPsServer) List(ctx context.Context,
	request *publicv1.ExternalIPsListRequest) (response *publicv1.ExternalIPsListResponse, err error) {
	privateRequest := &privatev1.ExternalIPsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ExternalIP, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ExternalIP{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private external IP to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IPs")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ExternalIPsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ExternalIPsServer) Get(ctx context.Context,
	request *publicv1.ExternalIPsGetRequest) (response *publicv1.ExternalIPsGetResponse, err error) {
	privateRequest := &privatev1.ExternalIPsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateExternalIP := privateResponse.GetObject()
	publicExternalIP := &publicv1.ExternalIP{}
	err = s.outMapper.Copy(ctx, privateExternalIP, publicExternalIP)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP")
	}

	response = &publicv1.ExternalIPsGetResponse{}
	response.SetObject(publicExternalIP)
	return
}

func (s *ExternalIPsServer) Create(ctx context.Context,
	request *publicv1.ExternalIPsCreateRequest) (response *publicv1.ExternalIPsCreateResponse, err error) {
	publicExternalIP := request.GetObject()
	if publicExternalIP == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateExternalIP := &privatev1.ExternalIP{}
	err = s.inMapper.Copy(ctx, publicExternalIP, privateExternalIP)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public external IP to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP")
		return
	}

	privateRequest := &privatev1.ExternalIPsCreateRequest{}
	privateRequest.SetObject(privateExternalIP)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateExternalIP := privateResponse.GetObject()
	createdPublicExternalIP := &publicv1.ExternalIP{}
	err = s.outMapper.Copy(ctx, createdPrivateExternalIP, createdPublicExternalIP)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP")
		return
	}

	response = &publicv1.ExternalIPsCreateResponse{}
	response.SetObject(createdPublicExternalIP)
	return
}

func (s *ExternalIPsServer) Update(ctx context.Context,
	request *publicv1.ExternalIPsUpdateRequest) (response *publicv1.ExternalIPsUpdateResponse, err error) {
	publicExternalIP := request.GetObject()
	if publicExternalIP == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateExternalIP := &privatev1.ExternalIP{}
	err = s.inMapper.Copy(ctx, publicExternalIP, privateExternalIP)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public external IP to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP")
		return
	}

	privateRequest := &privatev1.ExternalIPsUpdateRequest{}
	privateRequest.SetObject(privateExternalIP)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateExternalIP := privateResponse.GetObject()
	updatedPublicExternalIP := &publicv1.ExternalIP{}
	err = s.outMapper.Copy(ctx, updatedPrivateExternalIP, updatedPublicExternalIP)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP")
		return
	}

	response = &publicv1.ExternalIPsUpdateResponse{}
	response.SetObject(updatedPublicExternalIP)
	return
}

func (s *ExternalIPsServer) Delete(ctx context.Context,
	request *publicv1.ExternalIPsDeleteRequest) (response *publicv1.ExternalIPsDeleteResponse, err error) {
	privateRequest := &privatev1.ExternalIPsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ExternalIPsDeleteResponse{}
	return
}
