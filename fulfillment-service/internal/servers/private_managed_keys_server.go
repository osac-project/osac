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
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateManagedKeysServerBuilder is a builder for creating instances of PrivateManagedKeysServer.
type PrivateManagedKeysServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ManagedKeysServer = (*PrivateManagedKeysServer)(nil)

// PrivateManagedKeysServer implements the private managed keys gRPC service.
type PrivateManagedKeysServer struct {
	privatev1.UnimplementedManagedKeysServer

	generic *GenericServer[*privatev1.ManagedKey]
}

// NewPrivateManagedKeysServer creates a new builder for the private managed keys server.
func NewPrivateManagedKeysServer() *PrivateManagedKeysServerBuilder {
	return &PrivateManagedKeysServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateManagedKeysServerBuilder) SetLogger(value *slog.Logger) *PrivateManagedKeysServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is mandatory.
func (b *PrivateManagedKeysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateManagedKeysServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateManagedKeysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateManagedKeysServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateManagedKeysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateManagedKeysServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateManagedKeysServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateManagedKeysServerBuilder {
	b.filterDesc = value
	return b
}

// Build creates the private managed keys server from the builder configuration.
func (b *PrivateManagedKeysServerBuilder) Build() (result *PrivateManagedKeysServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.ManagedKey]().
		SetLogger(b.logger).
		SetService(privatev1.ManagedKeys_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	result = &PrivateManagedKeysServer{
		generic: generic,
	}
	return
}

func (s *PrivateManagedKeysServer) List(ctx context.Context,
	request *privatev1.ManagedKeysListRequest) (response *privatev1.ManagedKeysListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateManagedKeysServer) Get(ctx context.Context,
	request *privatev1.ManagedKeysGetRequest) (response *privatev1.ManagedKeysGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// Create is reserved for future managed key lifecycle support.
func (s *PrivateManagedKeysServer) Create(ctx context.Context,
	request *privatev1.ManagedKeysCreateRequest) (*privatev1.ManagedKeysCreateResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Create not implemented")
}

// Update is reserved for future managed key lifecycle support.
func (s *PrivateManagedKeysServer) Update(ctx context.Context,
	request *privatev1.ManagedKeysUpdateRequest) (*privatev1.ManagedKeysUpdateResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Update not implemented")
}

// Delete is reserved for future managed key lifecycle support.
func (s *PrivateManagedKeysServer) Delete(ctx context.Context,
	request *privatev1.ManagedKeysDeleteRequest) (*privatev1.ManagedKeysDeleteResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Delete not implemented")
}

// Signal is reserved for future managed key lifecycle support.
func (s *PrivateManagedKeysServer) Signal(ctx context.Context,
	request *privatev1.ManagedKeysSignalRequest) (*privatev1.ManagedKeysSignalResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "method Signal not implemented")
}
