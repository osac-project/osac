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

// NotificationSubscriptionDeliveriesServerBuilder builds the public deliveries server.
type NotificationSubscriptionDeliveriesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.NotificationSubscriptionDeliveriesServer = (*NotificationSubscriptionDeliveriesServer)(nil)

// NotificationSubscriptionDeliveriesServer implements public List/Get for deliveries.
type NotificationSubscriptionDeliveriesServer struct {
	publicv1.UnimplementedNotificationSubscriptionDeliveriesServer

	logger    *slog.Logger
	delegate  privatev1.NotificationSubscriptionDeliveriesServer
	outMapper *GenericMapper[*privatev1.NotificationSubscriptionDelivery, *publicv1.NotificationSubscriptionDelivery]
}

// NewNotificationSubscriptionDeliveriesServer creates a builder.
func NewNotificationSubscriptionDeliveriesServer() *NotificationSubscriptionDeliveriesServerBuilder {
	return &NotificationSubscriptionDeliveriesServerBuilder{}
}

// SetLogger sets the logger. Mandatory.
func (b *NotificationSubscriptionDeliveriesServerBuilder) SetLogger(
	value *slog.Logger) *NotificationSubscriptionDeliveriesServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used when building the private delegate.
func (b *NotificationSubscriptionDeliveriesServerBuilder) SetAttributionLogic(
	value auth.AttributionLogic) *NotificationSubscriptionDeliveriesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. Mandatory.
func (b *NotificationSubscriptionDeliveriesServerBuilder) SetTenancyLogic(
	value auth.TenancyLogic) *NotificationSubscriptionDeliveriesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *NotificationSubscriptionDeliveriesServerBuilder) SetMetricsRegisterer(
	value prometheus.Registerer) *NotificationSubscriptionDeliveriesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// Build creates the public deliveries server.
func (b *NotificationSubscriptionDeliveriesServerBuilder) Build() (
	result *NotificationSubscriptionDeliveriesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	outMapper, err := NewGenericMapper[*privatev1.NotificationSubscriptionDelivery, *publicv1.NotificationSubscriptionDelivery]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateNotificationSubscriptionDeliveriesServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.NotificationSubscriptionDelivery)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &NotificationSubscriptionDeliveriesServer{
		logger:    b.logger,
		delegate:  delegate,
		outMapper: outMapper,
	}
	return
}

// List returns deliveries visible under tenancy rules.
func (s *NotificationSubscriptionDeliveriesServer) List(ctx context.Context,
	request *publicv1.NotificationSubscriptionDeliveriesListRequest) (
	response *publicv1.NotificationSubscriptionDeliveriesListResponse, err error) {
	privateRequest := &privatev1.NotificationSubscriptionDeliveriesListRequest{}
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
	publicItems := make([]*publicv1.NotificationSubscriptionDelivery, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.NotificationSubscriptionDelivery{}
		if err = s.outMapper.Copy(ctx, privateItem, publicItem); err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private delivery to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscription deliveries")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.NotificationSubscriptionDeliveriesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

// Get returns one delivery by id.
func (s *NotificationSubscriptionDeliveriesServer) Get(ctx context.Context,
	request *publicv1.NotificationSubscriptionDeliveriesGetRequest) (
	response *publicv1.NotificationSubscriptionDeliveriesGetResponse, err error) {
	privateRequest := &privatev1.NotificationSubscriptionDeliveriesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.NotificationSubscriptionDelivery{}
	if err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private delivery to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification subscription delivery")
	}

	response = &publicv1.NotificationSubscriptionDeliveriesGetResponse{}
	response.SetObject(publicObject)
	return
}
