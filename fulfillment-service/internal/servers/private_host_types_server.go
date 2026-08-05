/*
Copyright (c) 2025 Red Hat Inc.

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

type PrivateHostTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.HostTypesServer = (*PrivateHostTypesServer)(nil)

type PrivateHostTypesServer struct {
	privatev1.UnimplementedHostTypesServer
	logger  *slog.Logger
	generic *GenericServer[*privatev1.HostType]
}

func NewPrivateHostTypesServer() *PrivateHostTypesServerBuilder {
	return &PrivateHostTypesServerBuilder{}
}

func (b *PrivateHostTypesServerBuilder) SetLogger(value *slog.Logger) *PrivateHostTypesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateHostTypesServerBuilder) SetNotifier(value events.Notifier) *PrivateHostTypesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateHostTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateHostTypesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateHostTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateHostTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateHostTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateHostTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateHostTypesServerBuilder) Build() (result *PrivateHostTypesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.HostType]().
		SetLogger(b.logger).
		SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateHostTypesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateHostTypesServer) List(ctx context.Context,
	request *privatev1.HostTypesListRequest) (response *privatev1.HostTypesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Get(ctx context.Context,
	request *privatev1.HostTypesGetRequest) (response *privatev1.HostTypesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Create(ctx context.Context,
	request *privatev1.HostTypesCreateRequest) (response *privatev1.HostTypesCreateResponse, err error) {
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Update(ctx context.Context,
	request *privatev1.HostTypesUpdateRequest) (response *privatev1.HostTypesUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Delete(ctx context.Context,
	request *privatev1.HostTypesDeleteRequest) (response *privatev1.HostTypesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Signal(ctx context.Context,
	request *privatev1.HostTypesSignalRequest) (response *privatev1.HostTypesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
