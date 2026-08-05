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

// PrivateProjectMembershipsServerBuilder is a builder for creating instances of PrivateProjectMembershipsServer.
type PrivateProjectMembershipsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ProjectMembershipsServer = (*PrivateProjectMembershipsServer)(nil)

// PrivateProjectMembershipsServer implements the private project memberships gRPC service.
type PrivateProjectMembershipsServer struct {
	privatev1.UnimplementedProjectMembershipsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.ProjectMembership]
}

// NewPrivateProjectMembershipsServer creates a new builder for the private project memberships server.
func NewPrivateProjectMembershipsServer() *PrivateProjectMembershipsServerBuilder {
	return &PrivateProjectMembershipsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateProjectMembershipsServerBuilder) SetLogger(value *slog.Logger) *PrivateProjectMembershipsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *PrivateProjectMembershipsServerBuilder) SetNotifier(value events.Notifier) *PrivateProjectMembershipsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *PrivateProjectMembershipsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateProjectMembershipsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *PrivateProjectMembershipsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateProjectMembershipsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateProjectMembershipsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateProjectMembershipsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the private project memberships server from the builder configuration.
func (b *PrivateProjectMembershipsServerBuilder) Build() (result *PrivateProjectMembershipsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.ProjectMembership]().
		SetLogger(b.logger).
		SetService(privatev1.ProjectMemberships_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateProjectMembershipsServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateProjectMembershipsServer) List(ctx context.Context,
	request *privatev1.ProjectMembershipsListRequest) (response *privatev1.ProjectMembershipsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateProjectMembershipsServer) Get(ctx context.Context,
	request *privatev1.ProjectMembershipsGetRequest) (response *privatev1.ProjectMembershipsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateProjectMembershipsServer) Create(ctx context.Context,
	request *privatev1.ProjectMembershipsCreateRequest) (response *privatev1.ProjectMembershipsCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateProjectMembershipsServer) Update(ctx context.Context,
	request *privatev1.ProjectMembershipsUpdateRequest) (response *privatev1.ProjectMembershipsUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateProjectMembershipsServer) Delete(ctx context.Context,
	request *privatev1.ProjectMembershipsDeleteRequest) (response *privatev1.ProjectMembershipsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateProjectMembershipsServer) Signal(ctx context.Context,
	request *privatev1.ProjectMembershipsSignalRequest) (response *privatev1.ProjectMembershipsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
