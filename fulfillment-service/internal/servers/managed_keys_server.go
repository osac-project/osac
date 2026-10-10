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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ManagedKeysServerBuilder is a builder for creating instances of ManagedKeysServer.
type ManagedKeysServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ManagedKeysServer = (*ManagedKeysServer)(nil)

// ManagedKeysServer implements the public managed keys gRPC service by delegating to the private server.
type ManagedKeysServer struct {
	publicv1.UnimplementedManagedKeysServer

	logger    *slog.Logger
	delegate  privatev1.ManagedKeysServer
	outMapper *GenericMapper[*privatev1.ManagedKey, *publicv1.ManagedKey]
}

// NewManagedKeysServer creates a new builder for the public managed keys server.
func NewManagedKeysServer() *ManagedKeysServerBuilder {
	return &ManagedKeysServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ManagedKeysServerBuilder) SetLogger(value *slog.Logger) *ManagedKeysServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *ManagedKeysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ManagedKeysServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ManagedKeysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ManagedKeysServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ManagedKeysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ManagedKeysServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public managed keys server from the builder configuration.
func (b *ManagedKeysServerBuilder) Build() (result *ManagedKeysServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	outMapper, err := NewGenericMapper[*privatev1.ManagedKey, *publicv1.ManagedKey]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateManagedKeysServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.ManagedKey)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &ManagedKeysServer{
		logger:    b.logger,
		delegate:  delegate,
		outMapper: outMapper,
	}
	return
}

func (s *ManagedKeysServer) List(ctx context.Context,
	request *publicv1.ManagedKeysListRequest) (response *publicv1.ManagedKeysListResponse, err error) {
	privateRequest := &privatev1.ManagedKeysListRequest{}
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
	publicItems := make([]*publicv1.ManagedKey, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ManagedKey{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private managed key to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process managed keys")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ManagedKeysListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ManagedKeysServer) Get(ctx context.Context,
	request *publicv1.ManagedKeysGetRequest) (response *publicv1.ManagedKeysGetResponse, err error) {
	privateRequest := &privatev1.ManagedKeysGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateManagedKey := privateResponse.GetObject()
	publicManagedKey := &publicv1.ManagedKey{}
	err = s.outMapper.Copy(ctx, privateManagedKey, publicManagedKey)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private managed key to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process managed key")
	}

	response = &publicv1.ManagedKeysGetResponse{}
	response.SetObject(publicManagedKey)
	return
}

// Create is reserved for future managed key lifecycle support.
func (s *ManagedKeysServer) Create(ctx context.Context,
	request *publicv1.ManagedKeysCreateRequest) (*publicv1.ManagedKeysCreateResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Create not implemented")
}

// Update is reserved for future managed key lifecycle support.
func (s *ManagedKeysServer) Update(ctx context.Context,
	request *publicv1.ManagedKeysUpdateRequest) (*publicv1.ManagedKeysUpdateResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Update not implemented")
}

// Delete is reserved for future managed key lifecycle support.
func (s *ManagedKeysServer) Delete(ctx context.Context,
	request *publicv1.ManagedKeysDeleteRequest) (*publicv1.ManagedKeysDeleteResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Delete not implemented")
}
