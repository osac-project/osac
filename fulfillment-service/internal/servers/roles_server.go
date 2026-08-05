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

// RolesServerBuilder is a builder for creating instances of RolesServer.
type RolesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.RolesServer = (*RolesServer)(nil)

// RolesServer implements the public roles gRPC service by delegating to the private server.
type RolesServer struct {
	publicv1.UnimplementedRolesServer

	logger    *slog.Logger
	delegate  privatev1.RolesServer
	inMapper  *GenericMapper[*publicv1.Role, *privatev1.Role]
	outMapper *GenericMapper[*privatev1.Role, *publicv1.Role]
}

// NewRolesServer creates a new builder for the public roles server.
func NewRolesServer() *RolesServerBuilder {
	return &RolesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *RolesServerBuilder) SetLogger(value *slog.Logger) *RolesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *RolesServerBuilder) SetNotifier(value events.Notifier) *RolesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *RolesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *RolesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *RolesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *RolesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *RolesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *RolesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public roles server from the builder configuration.
func (b *RolesServerBuilder) Build() (result *RolesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.Role, *privatev1.Role]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Role, *publicv1.Role]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateRolesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &RolesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *RolesServer) List(ctx context.Context,
	request *publicv1.RolesListRequest) (response *publicv1.RolesListResponse, err error) {
	privateRequest := &privatev1.RolesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Role, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Role{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private role to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process roles")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.RolesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *RolesServer) Get(ctx context.Context,
	request *publicv1.RolesGetRequest) (response *publicv1.RolesGetResponse, err error) {
	privateRequest := &privatev1.RolesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateRole := privateResponse.GetObject()
	publicRole := &publicv1.Role{}
	err = s.outMapper.Copy(ctx, privateRole, publicRole)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process role")
	}

	response = &publicv1.RolesGetResponse{}
	response.SetObject(publicRole)
	return
}

func (s *RolesServer) Create(ctx context.Context,
	request *publicv1.RolesCreateRequest) (response *publicv1.RolesCreateResponse, err error) {
	publicRole := request.GetObject()
	if publicRole == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateRole := &privatev1.Role{}
	err = s.inMapper.Copy(ctx, publicRole, privateRole)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public role to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role")
		return
	}

	privateRequest := &privatev1.RolesCreateRequest{}
	privateRequest.SetObject(privateRole)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateRole := privateResponse.GetObject()
	createdPublicRole := &publicv1.Role{}
	err = s.outMapper.Copy(ctx, createdPrivateRole, createdPublicRole)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role")
		return
	}

	response = &publicv1.RolesCreateResponse{}
	response.SetObject(createdPublicRole)
	return
}

func (s *RolesServer) Update(ctx context.Context,
	request *publicv1.RolesUpdateRequest) (response *publicv1.RolesUpdateResponse, err error) {
	publicRole := request.GetObject()
	if publicRole == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	if publicRole.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	privateRole := &privatev1.Role{}
	err = s.inMapper.Copy(ctx, publicRole, privateRole)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public role to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role")
		return
	}

	privateRequest := &privatev1.RolesUpdateRequest{}
	privateRequest.SetObject(privateRole)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateRole := privateResponse.GetObject()
	updatedPublicRole := &publicv1.Role{}
	err = s.outMapper.Copy(ctx, updatedPrivateRole, updatedPublicRole)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role")
		return
	}

	response = &publicv1.RolesUpdateResponse{}
	response.SetObject(updatedPublicRole)
	return
}

func (s *RolesServer) Delete(ctx context.Context,
	request *publicv1.RolesDeleteRequest) (response *publicv1.RolesDeleteResponse, err error) {
	privateRequest := &privatev1.RolesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.RolesDeleteResponse{}
	return
}
