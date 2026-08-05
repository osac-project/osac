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

type UsersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.UsersServer = (*UsersServer)(nil)

type UsersServer struct {
	publicv1.UnimplementedUsersServer

	logger    *slog.Logger
	private   privatev1.UsersServer
	inMapper  *GenericMapper[*publicv1.User, *privatev1.User]
	outMapper *GenericMapper[*privatev1.User, *publicv1.User]
}

func NewUsersServer() *UsersServerBuilder {
	return &UsersServerBuilder{}
}

func (b *UsersServerBuilder) SetLogger(value *slog.Logger) *UsersServerBuilder {
	b.logger = value
	return b
}

func (b *UsersServerBuilder) SetNotifier(value events.Notifier) *UsersServerBuilder {
	b.notifier = value
	return b
}

func (b *UsersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *UsersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *UsersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *UsersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *UsersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *UsersServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *UsersServerBuilder) Build() (result *UsersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.User, *privatev1.User]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.User, *publicv1.User]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateUsersServer().
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
	result = &UsersServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *UsersServer) List(ctx context.Context,
	request *publicv1.UsersListRequest) (response *publicv1.UsersListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.UsersListRequest{}
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
	publicItems := make([]*publicv1.User, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.User{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private user to public",
				slog.Any("error", err),
			)
			return nil, err
		}
		publicItems[i] = publicItem
	}

	// Prune credentials and build the response:
	for _, item := range publicItems {
		s.pruneCredentials(item)
	}
	response = &publicv1.UsersListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *UsersServer) Get(ctx context.Context,
	request *publicv1.UsersGetRequest) (response *publicv1.UsersGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.UsersGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.User{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private user to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Prune credentials and build the response:
	s.pruneCredentials(publicObject)
	response = &publicv1.UsersGetResponse{}
	response.SetObject(publicObject)
	return
}

func (s *UsersServer) Create(ctx context.Context,
	request *publicv1.UsersCreateRequest) (response *publicv1.UsersCreateResponse, err error) {
	// Map public request to private format:
	privateObject := &privatev1.User{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public user to private",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Create private request:
	privateRequest := &privatev1.UsersCreateRequest{}
	privateRequest.SetObject(privateObject)

	// Delegate to private server:
	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.User{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private user to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Prune credentials and build the response:
	s.pruneCredentials(publicObject)
	response = &publicv1.UsersCreateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *UsersServer) Update(ctx context.Context,
	request *publicv1.UsersUpdateRequest) (response *publicv1.UsersUpdateResponse, err error) {
	// Map public request to private format:
	privateObject := &privatev1.User{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public user to private",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Create private request:
	privateRequest := &privatev1.UsersUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(request.GetUpdateMask())

	// Delegate to private server:
	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	publicObject := &publicv1.User{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private user to public",
			slog.Any("error", err),
		)
		return nil, err
	}

	// Prune credentials and build the response:
	s.pruneCredentials(publicObject)
	response = &publicv1.UsersUpdateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *UsersServer) Delete(ctx context.Context,
	request *publicv1.UsersDeleteRequest) (response *publicv1.UsersDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.UsersDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Build and return the response:
	response = &publicv1.UsersDeleteResponse{}
	return
}

// pruneCredentials removes credential data from a user object so that sensitive information like passwords is never
// returned in public API responses.
func (s *UsersServer) pruneCredentials(user *publicv1.User) {
	if user.GetSpec() != nil {
		user.GetSpec().ClearCredentials()
	}
}
