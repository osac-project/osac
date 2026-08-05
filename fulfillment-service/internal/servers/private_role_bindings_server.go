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

// PrivateRoleBindingsServerBuilder is a builder for creating instances of PrivateRoleBindingsServer.
type PrivateRoleBindingsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.RoleBindingsServer = (*PrivateRoleBindingsServer)(nil)

// PrivateRoleBindingsServer implements the private role bindings gRPC service.
type PrivateRoleBindingsServer struct {
	privatev1.UnimplementedRoleBindingsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.RoleBinding]
}

// NewPrivateRoleBindingsServer creates a new builder for the private role bindings server.
func NewPrivateRoleBindingsServer() *PrivateRoleBindingsServerBuilder {
	return &PrivateRoleBindingsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateRoleBindingsServerBuilder) SetLogger(value *slog.Logger) *PrivateRoleBindingsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *PrivateRoleBindingsServerBuilder) SetNotifier(value events.Notifier) *PrivateRoleBindingsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *PrivateRoleBindingsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateRoleBindingsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateRoleBindingsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateRoleBindingsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateRoleBindingsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateRoleBindingsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the private role bindings server from the builder configuration.
func (b *PrivateRoleBindingsServerBuilder) Build() (result *PrivateRoleBindingsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.RoleBinding]().
		SetLogger(b.logger).
		SetService(privatev1.RoleBindings_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateRoleBindingsServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateRoleBindingsServer) List(ctx context.Context,
	request *privatev1.RoleBindingsListRequest) (response *privatev1.RoleBindingsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Get(ctx context.Context,
	request *privatev1.RoleBindingsGetRequest) (response *privatev1.RoleBindingsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Create(ctx context.Context,
	request *privatev1.RoleBindingsCreateRequest) (response *privatev1.RoleBindingsCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Update(ctx context.Context,
	request *privatev1.RoleBindingsUpdateRequest) (response *privatev1.RoleBindingsUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Delete(ctx context.Context,
	request *privatev1.RoleBindingsDeleteRequest) (response *privatev1.RoleBindingsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateRoleBindingsServer) Signal(ctx context.Context,
	request *privatev1.RoleBindingsSignalRequest) (response *privatev1.RoleBindingsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
