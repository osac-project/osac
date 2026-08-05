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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateNATGatewaysServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.NATGatewaysServer = (*PrivateNATGatewaysServer)(nil)

type PrivateNATGatewaysServer struct {
	privatev1.UnimplementedNATGatewaysServer

	logger        *slog.Logger
	generic       *GenericServer[*privatev1.NATGateway]
	externalIPDao *dao.GenericDAO[*privatev1.ExternalIP]
}

func NewPrivateNATGatewaysServer() *PrivateNATGatewaysServerBuilder {
	return &PrivateNATGatewaysServerBuilder{}
}

func (b *PrivateNATGatewaysServerBuilder) SetLogger(value *slog.Logger) *PrivateNATGatewaysServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetNotifier(value events.Notifier) *PrivateNATGatewaysServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateNATGatewaysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateNATGatewaysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateNATGatewaysServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) Build() (result *PrivateNATGatewaysServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	externalIPDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.NATGateway]().
		SetLogger(b.logger).
		SetService(privatev1.NATGateways_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateNATGatewaysServer{
		logger:        b.logger,
		generic:       generic,
		externalIPDao: externalIPDao,
	}
	return
}

func (s *PrivateNATGatewaysServer) List(ctx context.Context,
	request *privatev1.NATGatewaysListRequest) (response *privatev1.NATGatewaysListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Get(ctx context.Context,
	request *privatev1.NATGatewaysGetRequest) (response *privatev1.NATGatewaysGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Create(ctx context.Context,
	request *privatev1.NATGatewaysCreateRequest) (response *privatev1.NATGatewaysCreateResponse, err error) {
	natGateway := request.GetObject()

	err = s.validateNATGateway(natGateway)
	if err != nil {
		return
	}

	externalIPID := natGateway.GetSpec().GetExternalIp()

	err = s.validateExternalIPReference(ctx, externalIPID)
	if err != nil {
		return
	}

	if natGateway.GetStatus() == nil {
		natGateway.SetStatus(privatev1.NATGatewayStatus_builder{
			State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
		}.Build())
	} else {
		natGateway.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING)
	}

	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	err = s.updateExternalIPAttachedFlag(ctx, externalIPID, true)
	return
}

func (s *PrivateNATGatewaysServer) Update(ctx context.Context,
	request *privatev1.NATGatewaysUpdateRequest) (response *privatev1.NATGatewaysUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	mask := request.GetUpdateMask()
	if updateIncludesField(mask, "spec.virtual_network", "spec.external_ip") {
		getRequest := &privatev1.NATGatewaysGetRequest{}
		getRequest.SetId(id)
		var getResponse *privatev1.NATGatewaysGetResponse
		err = s.generic.Get(ctx, getRequest, &getResponse)
		if err != nil {
			return
		}
		err = validateImmutableFieldsNATGateway(request.GetObject(), getResponse.GetObject())
		if err != nil {
			return
		}
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Delete(ctx context.Context,
	request *privatev1.NATGatewaysDeleteRequest) (response *privatev1.NATGatewaysDeleteResponse, err error) {
	id := request.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.NATGatewaysGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.NATGatewaysGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	if err = validateNotDefault(getResponse.GetObject().GetMetadata().GetLabels(), "NAT gateway"); err != nil {
		return
	}

	externalIPID := getResponse.GetObject().GetSpec().GetExternalIp()

	err = s.generic.Delete(ctx, request, &response)
	if err != nil {
		return
	}

	if externalIPID != "" {
		err = s.updateExternalIPAttachedFlag(ctx, externalIPID, false)
	}
	return
}

func (s *PrivateNATGatewaysServer) Signal(ctx context.Context,
	request *privatev1.NATGatewaysSignalRequest) (response *privatev1.NATGatewaysSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) validateNATGateway(natGateway *privatev1.NATGateway) error {
	if natGateway == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "NAT gateway is mandatory")
	}
	spec := natGateway.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "NAT gateway spec is mandatory")
	}
	if spec.GetVirtualNetwork() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is required")
	}
	if spec.GetExternalIp() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is required")
	}
	return nil
}

func validateImmutableFieldsNATGateway(
	newGateway, existingGateway *privatev1.NATGateway) error {
	newSpec := newGateway.GetSpec()
	existingSpec := existingGateway.GetSpec()

	if newVN := newSpec.GetVirtualNetwork(); newVN != existingSpec.GetVirtualNetwork() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetVirtualNetwork(), newVN)
	}

	if newEIP := newSpec.GetExternalIp(); newEIP != existingSpec.GetExternalIp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetExternalIp(), newEIP)
	}

	return nil
}

func (s *PrivateNATGatewaysServer) validateExternalIPReference(
	ctx context.Context, externalIPID string) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ExternalIP '%s' does not exist", externalIPID)
		}
		s.logger.ErrorContext(ctx, "Failed to query ExternalIP",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate external_ip")
	}

	externalIP := getResponse.GetObject()

	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is not in ALLOCATED state (current state: %s)",
			externalIPID, externalIP.GetStatus().GetState().String())
	}

	if externalIP.GetStatus().GetAttached() {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is already attached", externalIPID)
	}

	return nil
}

func (s *PrivateNATGatewaysServer) updateExternalIPAttachedFlag(
	ctx context.Context, externalIPID string, attached bool) error {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get ExternalIP for attached flag update",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update ExternalIP attached status")
	}

	externalIP := getResponse.GetObject()
	externalIP.GetStatus().SetAttached(attached)

	_, err = s.externalIPDao.Update().
		SetObject(externalIP).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update ExternalIP attached flag",
			slog.String("external_ip_id", externalIPID),
			slog.Bool("attached", attached),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update ExternalIP attached status")
	}

	return nil
}
