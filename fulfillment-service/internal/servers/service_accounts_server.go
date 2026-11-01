//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

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

// ServiceAccountsServerBuilder is a builder for creating instances of ServiceAccountsServer.
type ServiceAccountsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ServiceAccountsServer = (*ServiceAccountsServer)(nil)

// ServiceAccountsServer implements the public service accounts gRPC service by delegating to the private server.
type ServiceAccountsServer struct {
	publicv1.UnimplementedServiceAccountsServer

	logger    *slog.Logger
	delegate  privatev1.ServiceAccountsServer
	inMapper  *GenericMapper[*publicv1.ServiceAccount, *privatev1.ServiceAccount]
	outMapper *GenericMapper[*privatev1.ServiceAccount, *publicv1.ServiceAccount]
}

// NewServiceAccountsServer creates a new builder for the public service accounts server.
func NewServiceAccountsServer() *ServiceAccountsServerBuilder {
	return &ServiceAccountsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ServiceAccountsServerBuilder) SetLogger(value *slog.Logger) *ServiceAccountsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ServiceAccountsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ServiceAccountsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ServiceAccountsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ServiceAccountsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ServiceAccountsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ServiceAccountsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public service accounts server from the builder configuration.
func (b *ServiceAccountsServerBuilder) Build() (result *ServiceAccountsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.ServiceAccount, *privatev1.ServiceAccount]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields("status").
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ServiceAccount, *publicv1.ServiceAccount]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateServiceAccountsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.ServiceAccount)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &ServiceAccountsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ServiceAccountsServer) List(ctx context.Context,
	request *publicv1.ServiceAccountsListRequest) (response *publicv1.ServiceAccountsListResponse, err error) {
	privateRequest := &privatev1.ServiceAccountsListRequest{}
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

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ServiceAccount, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ServiceAccount{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private service account to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process service accounts")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ServiceAccountsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ServiceAccountsServer) Get(ctx context.Context,
	request *publicv1.ServiceAccountsGetRequest) (response *publicv1.ServiceAccountsGetResponse, err error) {
	privateRequest := &privatev1.ServiceAccountsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateServiceAccount := privateResponse.GetObject()
	publicServiceAccount := &publicv1.ServiceAccount{}
	err = s.outMapper.Copy(ctx, privateServiceAccount, publicServiceAccount)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private service account to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process service account")
	}

	response = &publicv1.ServiceAccountsGetResponse{}
	response.SetObject(publicServiceAccount)
	return
}

func (s *ServiceAccountsServer) Create(ctx context.Context,
	request *publicv1.ServiceAccountsCreateRequest) (response *publicv1.ServiceAccountsCreateResponse, err error) {
	publicServiceAccount := request.GetObject()
	if publicServiceAccount == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateServiceAccount := &privatev1.ServiceAccount{}
	err = s.inMapper.Copy(ctx, publicServiceAccount, privateServiceAccount)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public service account to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process service account")
		return
	}

	privateRequest := &privatev1.ServiceAccountsCreateRequest{}
	privateRequest.SetObject(privateServiceAccount)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateServiceAccount := privateResponse.GetObject()
	createdPublicServiceAccount := &publicv1.ServiceAccount{}
	err = s.outMapper.Copy(ctx, createdPrivateServiceAccount, createdPublicServiceAccount)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private service account to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process service account")
		return
	}

	response = &publicv1.ServiceAccountsCreateResponse{}
	response.SetObject(createdPublicServiceAccount)
	return
}

func (s *ServiceAccountsServer) Update(ctx context.Context,
	request *publicv1.ServiceAccountsUpdateRequest) (response *publicv1.ServiceAccountsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err := validatePublicObjectMetadata(request.GetObject()); err != nil {
		return nil, err
	}

	publicServiceAccount := request.GetObject()
	if publicServiceAccount == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	if publicServiceAccount.GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	privateServiceAccount := &privatev1.ServiceAccount{}
	err = s.inMapper.Copy(ctx, publicServiceAccount, privateServiceAccount)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public service account to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process service account")
		return
	}

	privateRequest := &privatev1.ServiceAccountsUpdateRequest{}
	privateRequest.SetObject(privateServiceAccount)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateServiceAccount := privateResponse.GetObject()
	updatedPublicServiceAccount := &publicv1.ServiceAccount{}
	err = s.outMapper.Copy(ctx, updatedPrivateServiceAccount, updatedPublicServiceAccount)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private service account to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process service account")
		return
	}

	response = &publicv1.ServiceAccountsUpdateResponse{}
	response.SetObject(updatedPublicServiceAccount)
	return
}

func (s *ServiceAccountsServer) Delete(ctx context.Context,
	request *publicv1.ServiceAccountsDeleteRequest) (response *publicv1.ServiceAccountsDeleteResponse, err error) {
	privateRequest := &privatev1.ServiceAccountsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ServiceAccountsDeleteResponse{}
	return
}
