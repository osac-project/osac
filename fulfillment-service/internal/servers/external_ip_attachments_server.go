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

type ExternalIPAttachmentsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ExternalIPAttachmentsServer = (*ExternalIPAttachmentsServer)(nil)

type ExternalIPAttachmentsServer struct {
	publicv1.UnimplementedExternalIPAttachmentsServer

	logger    *slog.Logger
	delegate  privatev1.ExternalIPAttachmentsServer
	inMapper  *GenericMapper[*publicv1.ExternalIPAttachment, *privatev1.ExternalIPAttachment]
	outMapper *GenericMapper[*privatev1.ExternalIPAttachment, *publicv1.ExternalIPAttachment]
}

func NewExternalIPAttachmentsServer() *ExternalIPAttachmentsServerBuilder {
	return &ExternalIPAttachmentsServerBuilder{}
}

func (b *ExternalIPAttachmentsServerBuilder) SetLogger(value *slog.Logger) *ExternalIPAttachmentsServerBuilder {
	b.logger = value
	return b
}

func (b *ExternalIPAttachmentsServerBuilder) SetNotifier(value events.Notifier) *ExternalIPAttachmentsServerBuilder {
	b.notifier = value
	return b
}

func (b *ExternalIPAttachmentsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ExternalIPAttachmentsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *ExternalIPAttachmentsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ExternalIPAttachmentsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *ExternalIPAttachmentsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ExternalIPAttachmentsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ExternalIPAttachmentsServerBuilder) Build() (result *ExternalIPAttachmentsServer, err error) {
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

	inMapper, err := NewGenericMapper[*publicv1.ExternalIPAttachment, *privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ExternalIPAttachment, *publicv1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateExternalIPAttachmentsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ExternalIPAttachmentsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ExternalIPAttachmentsServer) List(ctx context.Context,
	request *publicv1.ExternalIPAttachmentsListRequest) (*publicv1.ExternalIPAttachmentsListResponse, error) {
	privateRequest := &privatev1.ExternalIPAttachmentsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ExternalIPAttachment, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ExternalIPAttachment{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private external IP attachment to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachments")
		}
		publicItems[i] = publicItem
	}

	response := &publicv1.ExternalIPAttachmentsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return response, nil
}

func (s *ExternalIPAttachmentsServer) Get(ctx context.Context,
	request *publicv1.ExternalIPAttachmentsGetRequest) (*publicv1.ExternalIPAttachmentsGetResponse, error) {
	privateRequest := &privatev1.ExternalIPAttachmentsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateAttachment := privateResponse.GetObject()
	publicAttachment := &publicv1.ExternalIPAttachment{}
	err = s.outMapper.Copy(ctx, privateAttachment, publicAttachment)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP attachment to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachment")
	}

	response := &publicv1.ExternalIPAttachmentsGetResponse{}
	response.SetObject(publicAttachment)
	return response, nil
}

func (s *ExternalIPAttachmentsServer) Create(ctx context.Context,
	request *publicv1.ExternalIPAttachmentsCreateRequest) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
	publicAttachment := request.GetObject()
	if publicAttachment == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateAttachment := &privatev1.ExternalIPAttachment{}
	err := s.inMapper.Copy(ctx, publicAttachment, privateAttachment)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public external IP attachment to private",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachment")
	}

	privateRequest := &privatev1.ExternalIPAttachmentsCreateRequest{}
	privateRequest.SetObject(privateAttachment)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateAttachment := privateResponse.GetObject()
	createdPublicAttachment := &publicv1.ExternalIPAttachment{}
	err = s.outMapper.Copy(ctx, createdPrivateAttachment, createdPublicAttachment)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP attachment to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachment")
	}

	response := &publicv1.ExternalIPAttachmentsCreateResponse{}
	response.SetObject(createdPublicAttachment)
	return response, nil
}

func (s *ExternalIPAttachmentsServer) Update(ctx context.Context,
	request *publicv1.ExternalIPAttachmentsUpdateRequest) (*publicv1.ExternalIPAttachmentsUpdateResponse, error) {
	publicAttachment := request.GetObject()
	if publicAttachment == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateAttachment := &privatev1.ExternalIPAttachment{}
	err := s.inMapper.Copy(ctx, publicAttachment, privateAttachment)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public external IP attachment to private",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachment")
	}

	privateRequest := &privatev1.ExternalIPAttachmentsUpdateRequest{}
	privateRequest.SetObject(privateAttachment)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateAttachment := privateResponse.GetObject()
	updatedPublicAttachment := &publicv1.ExternalIPAttachment{}
	err = s.outMapper.Copy(ctx, updatedPrivateAttachment, updatedPublicAttachment)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private external IP attachment to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process external IP attachment")
	}

	response := &publicv1.ExternalIPAttachmentsUpdateResponse{}
	response.SetObject(updatedPublicAttachment)
	return response, nil
}

func (s *ExternalIPAttachmentsServer) Delete(ctx context.Context,
	request *publicv1.ExternalIPAttachmentsDeleteRequest) (*publicv1.ExternalIPAttachmentsDeleteResponse, error) {
	privateRequest := &privatev1.ExternalIPAttachmentsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err := s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response := &publicv1.ExternalIPAttachmentsDeleteResponse{}
	return response, nil
}
