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
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type SshKeysServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.SshKeysServer = (*SshKeysServer)(nil)

type SshKeysServer struct {
	publicv1.UnimplementedSshKeysServer

	logger    *slog.Logger
	private   privatev1.SshKeysServer
	inMapper  *GenericMapper[*publicv1.SshKey, *privatev1.SshKey]
	outMapper *GenericMapper[*privatev1.SshKey, *publicv1.SshKey]
}

func NewSshKeysServer() *SshKeysServerBuilder {
	return &SshKeysServerBuilder{}
}

func (b *SshKeysServerBuilder) SetLogger(value *slog.Logger) *SshKeysServerBuilder {
	b.logger = value
	return b
}

func (b *SshKeysServerBuilder) SetNotifier(value events.Notifier) *SshKeysServerBuilder {
	b.notifier = value
	return b
}

func (b *SshKeysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *SshKeysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *SshKeysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *SshKeysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *SshKeysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *SshKeysServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *SshKeysServerBuilder) Build() (result *SshKeysServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	inMapper, err := NewGenericMapper[*publicv1.SshKey, *privatev1.SshKey]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.SshKey, *publicv1.SshKey]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}

	delegate, err := NewPrivateSshKeysServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.SshKey)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return nil, err
	}

	return &SshKeysServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}, nil
}

func (s *SshKeysServer) List(ctx context.Context,
	request *publicv1.SshKeysListRequest) (response *publicv1.SshKeysListResponse, err error) {
	privateRequest := &privatev1.SshKeysListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicItems := make([]*publicv1.SshKey, len(privateResponse.GetItems()))
	for i, privateItem := range privateResponse.GetItems() {
		publicItem := &publicv1.SshKey{}
		if err = s.outMapper.Copy(ctx, privateItem, publicItem); err != nil {
			s.logger.ErrorContext(ctx, "failed to map private SSH key to public", slog.Any("error", err))
			return nil, err
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.SshKeysListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *SshKeysServer) Get(ctx context.Context,
	request *publicv1.SshKeysGetRequest) (response *publicv1.SshKeysGetResponse, err error) {
	privateRequest := &privatev1.SshKeysGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.SshKey{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		s.logger.ErrorContext(ctx, "failed to map private SSH key to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.SshKeysGetResponse{}
	response.SetObject(publicObject)
	return
}

func (s *SshKeysServer) Create(ctx context.Context,
	request *publicv1.SshKeysCreateRequest) (response *publicv1.SshKeysCreateResponse, err error) {
	if request.GetObject() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateObject := &privatev1.SshKey{}
	if err = s.inMapper.Copy(ctx, request.GetObject(), privateObject); err != nil {
		s.logger.ErrorContext(ctx, "failed to map public SSH key to private", slog.Any("error", err))
		return nil, err
	}

	privateRequest := &privatev1.SshKeysCreateRequest{}
	privateRequest.SetObject(privateObject)
	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.SshKey{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		s.logger.ErrorContext(ctx, "failed to map private SSH key to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.SshKeysCreateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *SshKeysServer) Delete(ctx context.Context,
	request *publicv1.SshKeysDeleteRequest) (*publicv1.SshKeysDeleteResponse, error) {
	privateRequest := &privatev1.SshKeysDeleteRequest{}
	privateRequest.SetId(request.GetId())
	if _, err := s.private.Delete(ctx, privateRequest); err != nil {
		return nil, err
	}
	return &publicv1.SshKeysDeleteResponse{}, nil
}
