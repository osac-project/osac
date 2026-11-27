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

// PrivateNotificationSubscriptionDeliveriesServerBuilder builds the private deliveries server.
type PrivateNotificationSubscriptionDeliveriesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.NotificationSubscriptionDeliveriesServer = (*PrivateNotificationSubscriptionDeliveriesServer)(nil)

// PrivateNotificationSubscriptionDeliveriesServer implements private delivery CRUD.
type PrivateNotificationSubscriptionDeliveriesServer struct {
	privatev1.UnimplementedNotificationSubscriptionDeliveriesServer

	generic *GenericServer[*privatev1.NotificationSubscriptionDelivery]
}

// NewPrivateNotificationSubscriptionDeliveriesServer creates a builder.
func NewPrivateNotificationSubscriptionDeliveriesServer() *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	return &PrivateNotificationSubscriptionDeliveriesServerBuilder{}
}

// SetLogger sets the logger. Mandatory.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) SetLogger(
	value *slog.Logger) *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used on create.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) SetAttributionLogic(
	value auth.AttributionLogic) *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. Mandatory.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) SetTenancyLogic(
	value auth.TenancyLogic) *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) SetMetricsRegisterer(
	value prometheus.Registerer) *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used for CEL list filters.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) SetFilterDesc(
	value protoreflect.MessageDescriptor) *PrivateNotificationSubscriptionDeliveriesServerBuilder {
	b.filterDesc = value
	return b
}

// Build creates the private deliveries server.
func (b *PrivateNotificationSubscriptionDeliveriesServerBuilder) Build() (
	result *PrivateNotificationSubscriptionDeliveriesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.NotificationSubscriptionDelivery]().
		SetLogger(b.logger).
		SetService(privatev1.NotificationSubscriptionDeliveries_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateNotificationSubscriptionDeliveriesServer{generic: generic}
	return
}

// List returns deliveries visible under tenancy rules.
func (s *PrivateNotificationSubscriptionDeliveriesServer) List(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesListRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

// Get returns one delivery by id.
func (s *PrivateNotificationSubscriptionDeliveriesServer) Get(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesGetRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// Create stores a delivery after validating the match key fields.
func (s *PrivateNotificationSubscriptionDeliveriesServer) Create(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesCreateRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesCreateResponse, err error) {
	object := request.GetObject()
	if object == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	if err = validateDeliveryCreate(object); err != nil {
		return nil, err
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

// Update changes delivery status fields.
func (s *PrivateNotificationSubscriptionDeliveriesServer) Update(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesUpdateRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

// Delete soft-deletes a delivery.
func (s *PrivateNotificationSubscriptionDeliveriesServer) Delete(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesDeleteRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

// Signal is unused for deliveries.
func (s *PrivateNotificationSubscriptionDeliveriesServer) Signal(ctx context.Context,
	request *privatev1.NotificationSubscriptionDeliveriesSignalRequest) (
	response *privatev1.NotificationSubscriptionDeliveriesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func validateDeliveryCreate(object *privatev1.NotificationSubscriptionDelivery) error {
	spec := object.GetSpec()
	if spec == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "object spec is mandatory")
	}
	sub := spec.GetSubscription()
	if sub == nil || sub.GetId() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.subscription.id' is required")
	}
	event := spec.GetEvent()
	if event == nil || event.GetId() == "" || event.GetSource() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.event' id and source are required")
	}
	status := object.GetStatus()
	if status == nil {
		status = &privatev1.NotificationSubscriptionDeliveryStatus{}
		object.SetStatus(status)
	}
	if status.GetState() == privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_UNSPECIFIED {
		status.SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING)
	}
	return nil
}
