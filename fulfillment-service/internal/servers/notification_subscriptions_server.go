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

// NotificationSubscriptionsServerBuilder builds NotificationSubscriptionsServer.
type NotificationSubscriptionsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	secrets           privatev1.SecretsServer
}

var _ publicv1.NotificationSubscriptionsServer = (*NotificationSubscriptionsServer)(nil)

// NotificationSubscriptionsServer implements the public NotificationSubscriptions service.
type NotificationSubscriptionsServer struct {
	publicv1.UnimplementedNotificationSubscriptionsServer

	logger    *slog.Logger
	delegate  privatev1.NotificationSubscriptionsServer
	inMapper  *GenericMapper[*publicv1.NotificationSubscription, *privatev1.NotificationSubscription]
	outMapper *GenericMapper[*privatev1.NotificationSubscription, *publicv1.NotificationSubscription]
}

// NewNotificationSubscriptionsServer creates a builder.
func NewNotificationSubscriptionsServer() *NotificationSubscriptionsServerBuilder {
	return &NotificationSubscriptionsServerBuilder{}
}

// SetLogger sets the logger. Mandatory.
func (b *NotificationSubscriptionsServerBuilder) SetLogger(
	value *slog.Logger) *NotificationSubscriptionsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used on create.
func (b *NotificationSubscriptionsServerBuilder) SetAttributionLogic(
	value auth.AttributionLogic) *NotificationSubscriptionsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. Mandatory.
func (b *NotificationSubscriptionsServerBuilder) SetTenancyLogic(
	value auth.TenancyLogic) *NotificationSubscriptionsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *NotificationSubscriptionsServerBuilder) SetMetricsRegisterer(
	value prometheus.Registerer) *NotificationSubscriptionsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetSecretsServer sets the private Secrets server used to store signing material.
func (b *NotificationSubscriptionsServerBuilder) SetSecretsServer(
	value privatev1.SecretsServer) *NotificationSubscriptionsServerBuilder {
	b.secrets = value
	return b
}

// Build creates the public notification subscriptions server.
func (b *NotificationSubscriptionsServerBuilder) Build() (
	result *NotificationSubscriptionsServer, err error) {
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
	if b.secrets == nil {
		err = errors.New("secrets server is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.NotificationSubscription, *privatev1.NotificationSubscription]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.NotificationSubscription, *publicv1.NotificationSubscription]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateNotificationSubscriptionsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetSecretsServer(b.secrets).
		SetFilterDesc((*publicv1.NotificationSubscription)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &NotificationSubscriptionsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

// List returns subscriptions visible under tenancy rules.
func (s *NotificationSubscriptionsServer) List(ctx context.Context,
	request *publicv1.NotificationSubscriptionsListRequest) (
	response *publicv1.NotificationSubscriptionsListResponse, err error) {
	privateRequest := &privatev1.NotificationSubscriptionsListRequest{}
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
	publicItems := make([]*publicv1.NotificationSubscription, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.NotificationSubscription{}
		if err = s.outMapper.Copy(ctx, privateItem, publicItem); err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private subscription to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscriptions")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.NotificationSubscriptionsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

// Get returns one subscription by id.
func (s *NotificationSubscriptionsServer) Get(ctx context.Context,
	request *publicv1.NotificationSubscriptionsGetRequest) (
	response *publicv1.NotificationSubscriptionsGetResponse, err error) {
	privateRequest := &privatev1.NotificationSubscriptionsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.NotificationSubscription{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private subscription to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscription")
	}

	response = &publicv1.NotificationSubscriptionsGetResponse{}
	response.SetObject(publicObject)
	return
}

// Create stores a subscription and returns the signing secret once.
func (s *NotificationSubscriptionsServer) Create(ctx context.Context,
	request *publicv1.NotificationSubscriptionsCreateRequest) (
	response *publicv1.NotificationSubscriptionsCreateResponse, err error) {
	publicObject := request.GetObject()
	if publicObject == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}

	privateObject := &privatev1.NotificationSubscription{}
	if err = s.inMapper.Copy(ctx, publicObject, privateObject); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public subscription to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process notification subscription")
	}

	privateRequest := &privatev1.NotificationSubscriptionsCreateRequest{}
	privateRequest.SetObject(privateObject)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublic := &publicv1.NotificationSubscription{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublic); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private subscription to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscription")
	}

	response = &publicv1.NotificationSubscriptionsCreateResponse{}
	response.SetObject(createdPublic)
	response.SetSigningSecret(privateResponse.GetSigningSecret())
	return
}

// Update changes mutable subscription fields.
func (s *NotificationSubscriptionsServer) Update(ctx context.Context,
	request *publicv1.NotificationSubscriptionsUpdateRequest) (
	response *publicv1.NotificationSubscriptionsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err := validatePublicObjectMetadata(request.GetObject()); err != nil {
		return nil, err
	}
	publicObject := request.GetObject()
	if publicObject == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	id := publicObject.GetId()
	if id == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	getRequest := &privatev1.NotificationSubscriptionsGetRequest{}
	getRequest.SetId(id)
	getResponse, getErr := s.delegate.Get(ctx, getRequest)
	if getErr != nil {
		return nil, getErr
	}

	var privateObject *privatev1.NotificationSubscription
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateObject = &privatev1.NotificationSubscription{}
		privateObject.SetId(id)
	} else {
		privateObject = getResponse.GetObject()
	}
	if err = s.inMapper.Copy(ctx, publicObject, privateObject); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public subscription to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process notification subscription")
	}

	privateRequest := &privatev1.NotificationSubscriptionsUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublic := &publicv1.NotificationSubscription{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublic); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private subscription to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscription")
	}

	response = &publicv1.NotificationSubscriptionsUpdateResponse{}
	response.SetObject(updatedPublic)
	return
}

// Delete soft-deletes a subscription.
func (s *NotificationSubscriptionsServer) Delete(ctx context.Context,
	request *publicv1.NotificationSubscriptionsDeleteRequest) (
	response *publicv1.NotificationSubscriptionsDeleteResponse, err error) {
	privateRequest := &privatev1.NotificationSubscriptionsDeleteRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.NotificationSubscriptionsDeleteResponse{}
	return
}
