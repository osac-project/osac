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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

// PrivateRolesServerBuilder is a builder for creating instances of PrivateRolesServer.
type PrivateRolesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.RolesServer = (*PrivateRolesServer)(nil)

// PrivateRolesServer implements the private roles gRPC service.
type PrivateRolesServer struct {
	privatev1.UnimplementedRolesServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.Role]
}

// NewPrivateRolesServer creates a new builder for the private roles server.
func NewPrivateRolesServer() *PrivateRolesServerBuilder {
	return &PrivateRolesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateRolesServerBuilder) SetLogger(value *slog.Logger) *PrivateRolesServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *PrivateRolesServerBuilder) SetNotifier(value events.Notifier) *PrivateRolesServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *PrivateRolesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateRolesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateRolesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateRolesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateRolesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateRolesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the private roles server from the builder configuration.
func (b *PrivateRolesServerBuilder) Build() (result *PrivateRolesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.Role]().
		SetLogger(b.logger).
		SetService(privatev1.Roles_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateRolesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateRolesServer) List(ctx context.Context,
	request *privatev1.RolesListRequest) (response *privatev1.RolesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateRolesServer) Get(ctx context.Context,
	request *privatev1.RolesGetRequest) (response *privatev1.RolesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateRolesServer) Create(ctx context.Context,
	request *privatev1.RolesCreateRequest) (response *privatev1.RolesCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateRolesServer) Update(ctx context.Context,
	request *privatev1.RolesUpdateRequest) (response *privatev1.RolesUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateRolesServer) Delete(ctx context.Context,
	request *privatev1.RolesDeleteRequest) (response *privatev1.RolesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateRolesServer) Signal(ctx context.Context,
	request *privatev1.RolesSignalRequest) (response *privatev1.RolesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
