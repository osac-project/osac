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

type NATGatewaysServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.NATGatewaysServer = (*NATGatewaysServer)(nil)

type NATGatewaysServer struct {
	publicv1.UnimplementedNATGatewaysServer

	logger    *slog.Logger
	delegate  privatev1.NATGatewaysServer
	inMapper  *GenericMapper[*publicv1.NATGateway, *privatev1.NATGateway]
	outMapper *GenericMapper[*privatev1.NATGateway, *publicv1.NATGateway]
}

func NewNATGatewaysServer() *NATGatewaysServerBuilder {
	return &NATGatewaysServerBuilder{}
}

func (b *NATGatewaysServerBuilder) SetLogger(value *slog.Logger) *NATGatewaysServerBuilder {
	b.logger = value
	return b
}

func (b *NATGatewaysServerBuilder) SetNotifier(value events.Notifier) *NATGatewaysServerBuilder {
	b.notifier = value
	return b
}

func (b *NATGatewaysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *NATGatewaysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *NATGatewaysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *NATGatewaysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *NATGatewaysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *NATGatewaysServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *NATGatewaysServerBuilder) Build() (result *NATGatewaysServer, err error) {
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

	inMapper, err := NewGenericMapper[*publicv1.NATGateway, *privatev1.NATGateway]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.NATGateway, *publicv1.NATGateway]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateNATGatewaysServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &NATGatewaysServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *NATGatewaysServer) List(ctx context.Context,
	request *publicv1.NATGatewaysListRequest) (response *publicv1.NATGatewaysListResponse, err error) {
	privateRequest := &privatev1.NATGatewaysListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.NATGateway, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.NATGateway{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private NAT gateway to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateways")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.NATGatewaysListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *NATGatewaysServer) Get(ctx context.Context,
	request *publicv1.NATGatewaysGetRequest) (response *publicv1.NATGatewaysGetResponse, err error) {
	privateRequest := &privatev1.NATGatewaysGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateNATGateway := privateResponse.GetObject()
	publicNATGateway := &publicv1.NATGateway{}
	err = s.outMapper.Copy(ctx, privateNATGateway, publicNATGateway)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private NAT gateway to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateway")
	}

	response = &publicv1.NATGatewaysGetResponse{}
	response.SetObject(publicNATGateway)
	return
}

func (s *NATGatewaysServer) Create(ctx context.Context,
	request *publicv1.NATGatewaysCreateRequest) (response *publicv1.NATGatewaysCreateResponse, err error) {
	publicNATGateway := request.GetObject()
	if publicNATGateway == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateNATGateway := &privatev1.NATGateway{}
	err = s.inMapper.Copy(ctx, publicNATGateway, privateNATGateway)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public NAT gateway to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateway")
		return
	}

	privateRequest := &privatev1.NATGatewaysCreateRequest{}
	privateRequest.SetObject(privateNATGateway)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateNATGateway := privateResponse.GetObject()
	createdPublicNATGateway := &publicv1.NATGateway{}
	err = s.outMapper.Copy(ctx, createdPrivateNATGateway, createdPublicNATGateway)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private NAT gateway to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateway")
		return
	}

	response = &publicv1.NATGatewaysCreateResponse{}
	response.SetObject(createdPublicNATGateway)
	return
}

func (s *NATGatewaysServer) Update(ctx context.Context,
	request *publicv1.NATGatewaysUpdateRequest) (response *publicv1.NATGatewaysUpdateResponse, err error) {
	publicNATGateway := request.GetObject()
	if publicNATGateway == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateNATGateway := &privatev1.NATGateway{}
	err = s.inMapper.Copy(ctx, publicNATGateway, privateNATGateway)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public NAT gateway to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateway")
		return
	}

	privateRequest := &privatev1.NATGatewaysUpdateRequest{}
	privateRequest.SetObject(privateNATGateway)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateNATGateway := privateResponse.GetObject()
	updatedPublicNATGateway := &publicv1.NATGateway{}
	err = s.outMapper.Copy(ctx, updatedPrivateNATGateway, updatedPublicNATGateway)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private NAT gateway to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process NAT gateway")
		return
	}

	response = &publicv1.NATGatewaysUpdateResponse{}
	response.SetObject(updatedPublicNATGateway)
	return
}

func (s *NATGatewaysServer) Delete(ctx context.Context,
	request *publicv1.NATGatewaysDeleteRequest) (response *publicv1.NATGatewaysDeleteResponse, err error) {
	privateRequest := &privatev1.NATGatewaysDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.NATGatewaysDeleteResponse{}
	return
}
