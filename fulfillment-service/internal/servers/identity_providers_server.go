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

type IdentityProvidersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.IdentityProvidersServer = (*IdentityProvidersServer)(nil)

type IdentityProvidersServer struct {
	publicv1.UnimplementedIdentityProvidersServer

	logger    *slog.Logger
	private   privatev1.IdentityProvidersServer
	inMapper  *GenericMapper[*publicv1.IdentityProvider, *privatev1.IdentityProvider]
	outMapper *GenericMapper[*privatev1.IdentityProvider, *publicv1.IdentityProvider]
}

func NewIdentityProvidersServer() *IdentityProvidersServerBuilder {
	return &IdentityProvidersServerBuilder{}
}

func (b *IdentityProvidersServerBuilder) SetLogger(value *slog.Logger) *IdentityProvidersServerBuilder {
	b.logger = value
	return b
}

func (b *IdentityProvidersServerBuilder) SetNotifier(value events.Notifier) *IdentityProvidersServerBuilder {
	b.notifier = value
	return b
}

func (b *IdentityProvidersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *IdentityProvidersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *IdentityProvidersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *IdentityProvidersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *IdentityProvidersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *IdentityProvidersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *IdentityProvidersServerBuilder) Build() (result *IdentityProvidersServer, err error) {
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
	inMapper, err := NewGenericMapper[*publicv1.IdentityProvider, *privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.IdentityProvider, *publicv1.IdentityProvider]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateIdentityProvidersServer().
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
	result = &IdentityProvidersServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *IdentityProvidersServer) Create(ctx context.Context,
	request *publicv1.IdentityProvidersCreateRequest) (response *publicv1.IdentityProvidersCreateResponse, err error) {
	// Map public request to private format:
	privateObject := &privatev1.IdentityProvider{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public identity provider to private",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Create private request:
	privateRequest := &privatev1.IdentityProvidersCreateRequest{}
	privateRequest.SetObject(privateObject)

	// Delegate to private server:
	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.IdentityProvider{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private identity provider to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Build and return the response:
	response = &publicv1.IdentityProvidersCreateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *IdentityProvidersServer) List(ctx context.Context,
	request *publicv1.IdentityProvidersListRequest) (response *publicv1.IdentityProvidersListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.IdentityProvidersListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.IdentityProvider, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.IdentityProvider{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private identity provider to public",
				slog.Any("error", err),
			)
			return nil, err
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.IdentityProvidersListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *IdentityProvidersServer) Get(ctx context.Context,
	request *publicv1.IdentityProvidersGetRequest) (response *publicv1.IdentityProvidersGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.IdentityProvidersGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateObject := privateResponse.GetObject()
	publicObject := &publicv1.IdentityProvider{}
	err = s.outMapper.Copy(ctx, privateObject, publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private identity provider to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Create the public response:
	response = &publicv1.IdentityProvidersGetResponse{}
	response.SetObject(publicObject)
	return
}

func (s *IdentityProvidersServer) Update(ctx context.Context,
	request *publicv1.IdentityProvidersUpdateRequest) (response *publicv1.IdentityProvidersUpdateResponse, err error) {
	// Map public request to private format:
	privateObject := &privatev1.IdentityProvider{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public identity provider to private",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Create private request:
	privateRequest := &privatev1.IdentityProvidersUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())

	// Delegate to private server:
	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.IdentityProvider{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private identity provider to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Build and return the response:
	response = &publicv1.IdentityProvidersUpdateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *IdentityProvidersServer) Delete(ctx context.Context,
	request *publicv1.IdentityProvidersDeleteRequest) (response *publicv1.IdentityProvidersDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.IdentityProvidersDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.IdentityProvidersDeleteResponse{}
	return
}
