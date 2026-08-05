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

// RoleBindingsServerBuilder is a builder for creating instances of RoleBindingsServer.
type RoleBindingsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.RoleBindingsServer = (*RoleBindingsServer)(nil)

// RoleBindingsServer implements the public role bindings gRPC service by delegating to the private server.
type RoleBindingsServer struct {
	publicv1.UnimplementedRoleBindingsServer

	logger    *slog.Logger
	delegate  privatev1.RoleBindingsServer
	inMapper  *GenericMapper[*publicv1.RoleBinding, *privatev1.RoleBinding]
	outMapper *GenericMapper[*privatev1.RoleBinding, *publicv1.RoleBinding]
}

// NewRoleBindingsServer creates a new builder for the public role bindings server.
func NewRoleBindingsServer() *RoleBindingsServerBuilder {
	return &RoleBindingsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *RoleBindingsServerBuilder) SetLogger(value *slog.Logger) *RoleBindingsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *RoleBindingsServerBuilder) SetNotifier(value events.Notifier) *RoleBindingsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *RoleBindingsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *RoleBindingsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *RoleBindingsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *RoleBindingsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *RoleBindingsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *RoleBindingsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public role bindings server from the builder configuration.
func (b *RoleBindingsServerBuilder) Build() (result *RoleBindingsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.RoleBinding, *privatev1.RoleBinding]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.RoleBinding, *publicv1.RoleBinding]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateRoleBindingsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &RoleBindingsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *RoleBindingsServer) List(ctx context.Context,
	request *publicv1.RoleBindingsListRequest) (response *publicv1.RoleBindingsListResponse, err error) {
	privateRequest := &privatev1.RoleBindingsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.RoleBinding, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.RoleBinding{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private role binding to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process role bindings")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.RoleBindingsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *RoleBindingsServer) Get(ctx context.Context,
	request *publicv1.RoleBindingsGetRequest) (response *publicv1.RoleBindingsGetResponse, err error) {
	privateRequest := &privatev1.RoleBindingsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateRoleBinding := privateResponse.GetObject()
	publicRoleBinding := &publicv1.RoleBinding{}
	err = s.outMapper.Copy(ctx, privateRoleBinding, publicRoleBinding)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role binding to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process role binding")
	}

	response = &publicv1.RoleBindingsGetResponse{}
	response.SetObject(publicRoleBinding)
	return
}

func (s *RoleBindingsServer) Create(ctx context.Context,
	request *publicv1.RoleBindingsCreateRequest) (response *publicv1.RoleBindingsCreateResponse, err error) {
	publicRoleBinding := request.GetObject()
	if publicRoleBinding == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateRoleBinding := &privatev1.RoleBinding{}
	err = s.inMapper.Copy(ctx, publicRoleBinding, privateRoleBinding)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public role binding to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role binding")
		return
	}

	privateRequest := &privatev1.RoleBindingsCreateRequest{}
	privateRequest.SetObject(privateRoleBinding)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateRoleBinding := privateResponse.GetObject()
	createdPublicRoleBinding := &publicv1.RoleBinding{}
	err = s.outMapper.Copy(ctx, createdPrivateRoleBinding, createdPublicRoleBinding)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role binding to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role binding")
		return
	}

	response = &publicv1.RoleBindingsCreateResponse{}
	response.SetObject(createdPublicRoleBinding)
	return
}

func (s *RoleBindingsServer) Update(ctx context.Context,
	request *publicv1.RoleBindingsUpdateRequest) (response *publicv1.RoleBindingsUpdateResponse, err error) {
	publicRoleBinding := request.GetObject()
	if publicRoleBinding == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	if publicRoleBinding.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	privateRoleBinding := &privatev1.RoleBinding{}
	err = s.inMapper.Copy(ctx, publicRoleBinding, privateRoleBinding)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public role binding to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role binding")
		return
	}

	privateRequest := &privatev1.RoleBindingsUpdateRequest{}
	privateRequest.SetObject(privateRoleBinding)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateRoleBinding := privateResponse.GetObject()
	updatedPublicRoleBinding := &publicv1.RoleBinding{}
	err = s.outMapper.Copy(ctx, updatedPrivateRoleBinding, updatedPublicRoleBinding)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private role binding to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process role binding")
		return
	}

	response = &publicv1.RoleBindingsUpdateResponse{}
	response.SetObject(updatedPublicRoleBinding)
	return
}

func (s *RoleBindingsServer) Delete(ctx context.Context,
	request *publicv1.RoleBindingsDeleteRequest) (response *publicv1.RoleBindingsDeleteResponse, err error) {
	privateRequest := &privatev1.RoleBindingsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.RoleBindingsDeleteResponse{}
	return
}
