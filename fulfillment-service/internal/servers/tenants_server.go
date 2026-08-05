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
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type TenantsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.TenantsServer = (*TenantsServer)(nil)

type TenantsServer struct {
	publicv1.UnimplementedTenantsServer

	logger    *slog.Logger
	private   privatev1.TenantsServer
	inMapper  *GenericMapper[*publicv1.Tenant, *privatev1.Tenant]
	outMapper *GenericMapper[*privatev1.Tenant, *publicv1.Tenant]
}

func NewTenantsServer() *TenantsServerBuilder {
	return &TenantsServerBuilder{}
}

func (b *TenantsServerBuilder) SetLogger(value *slog.Logger) *TenantsServerBuilder {
	b.logger = value
	return b
}

func (b *TenantsServerBuilder) SetNotifier(value events.Notifier) *TenantsServerBuilder {
	b.notifier = value
	return b
}

func (b *TenantsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *TenantsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *TenantsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *TenantsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *TenantsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *TenantsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *TenantsServerBuilder) Build() (result *TenantsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.Tenant, *privatev1.Tenant]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Tenant, *publicv1.Tenant]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateTenantsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &TenantsServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *TenantsServer) List(ctx context.Context,
	request *publicv1.TenantsListRequest) (response *publicv1.TenantsListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.TenantsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	// Delegate to private server:
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Tenant, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Tenant{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private tenant to public",
				slog.Any("error", err),
			)
			return nil, err
		}
		publicItems[i] = publicItem
	}

	// Build and return the response:
	response = &publicv1.TenantsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *TenantsServer) Get(ctx context.Context,
	request *publicv1.TenantsGetRequest) (response *publicv1.TenantsGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.TenantsGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.Tenant{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private tenant to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Build and return the response:
	response = &publicv1.TenantsGetResponse{}
	response.SetObject(publicObject)
	return
}
