/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateFabricDomainsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.FabricDomainsServer = (*PrivateFabricDomainsServer)(nil)

type PrivateFabricDomainsServer struct {
	privatev1.UnimplementedFabricDomainsServer
	generic           *GenericServer[*privatev1.FabricDomain]
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork]
	networkClassDao   *dao.GenericDAO[*privatev1.NetworkClass]
}

func NewPrivateFabricDomainsServer() *PrivateFabricDomainsServerBuilder {
	return &PrivateFabricDomainsServerBuilder{}
}

func (b *PrivateFabricDomainsServerBuilder) SetLogger(value *slog.Logger) *PrivateFabricDomainsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetNotifier(value events.Notifier) *PrivateFabricDomainsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateFabricDomainsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateFabricDomainsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateFabricDomainsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) Build() (result *PrivateFabricDomainsServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	generic, err := NewGenericServer[*privatev1.FabricDomain]().
		SetLogger(b.logger).
		SetService(privatev1.FabricDomains_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	virtualNetworkDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}
	networkClassDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return nil, err
	}

	return &PrivateFabricDomainsServer{
		generic:           generic,
		virtualNetworkDao: virtualNetworkDao,
		networkClassDao:   networkClassDao,
	}, nil
}

func (s *PrivateFabricDomainsServer) List(ctx context.Context, request *privatev1.FabricDomainsListRequest) (*privatev1.FabricDomainsListResponse, error) {
	var response *privatev1.FabricDomainsListResponse
	err := s.generic.List(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Get(ctx context.Context, request *privatev1.FabricDomainsGetRequest) (*privatev1.FabricDomainsGetResponse, error) {
	var response *privatev1.FabricDomainsGetResponse
	err := s.generic.Get(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Create(ctx context.Context, request *privatev1.FabricDomainsCreateRequest) (*privatev1.FabricDomainsCreateResponse, error) {
	if err := s.validateFabricDomain(ctx, request.GetObject()); err != nil {
		return nil, err
	}
	object := request.GetObject()
	object.SetStatus(&privatev1.FabricDomainStatus{Conditions: []*privatev1.FabricDomainCondition{{
		Type:               privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING,
		Status:             privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
		LastTransitionTime: timestamppb.Now(),
	}}})
	var response *privatev1.FabricDomainsCreateResponse
	err := s.generic.Create(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Update(ctx context.Context, request *privatev1.FabricDomainsUpdateRequest) (*privatev1.FabricDomainsUpdateResponse, error) {
	if request.GetObject().GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}
	getRequest := &privatev1.FabricDomainsGetRequest{}
	getRequest.SetId(request.GetObject().GetId())
	var getResponse *privatev1.FabricDomainsGetResponse
	if err := s.generic.Get(ctx, getRequest, &getResponse); err != nil {
		return nil, err
	}
	old := getResponse.GetObject()
	updated := request.GetObject()
	fullUpdate := request.GetUpdateMask() == nil || len(request.GetUpdateMask().GetPaths()) == 0
	if (fullUpdate || updateIncludesField(request.GetUpdateMask(), "spec.type")) && updated.GetSpec().GetType() != old.GetSpec().GetType() {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "type is immutable")
	}
	if (fullUpdate || updateIncludesField(request.GetUpdateMask(), "spec.virtual_networks")) && !sameStrings(updated.GetSpec().GetVirtualNetworks(), old.GetSpec().GetVirtualNetworks()) {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "virtual_networks is immutable")
	}
	if (fullUpdate || updateIncludesField(request.GetUpdateMask(), "spec.servers")) && len(updated.GetSpec().GetServers()) == 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "servers list must not be empty")
	}
	var response *privatev1.FabricDomainsUpdateResponse
	err := s.generic.Update(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Delete(ctx context.Context, request *privatev1.FabricDomainsDeleteRequest) (*privatev1.FabricDomainsDeleteResponse, error) {
	var response *privatev1.FabricDomainsDeleteResponse
	err := s.generic.Delete(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Signal(ctx context.Context, request *privatev1.FabricDomainsSignalRequest) (*privatev1.FabricDomainsSignalResponse, error) {
	var response *privatev1.FabricDomainsSignalResponse
	err := s.generic.Signal(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) validateFabricDomain(ctx context.Context, object *privatev1.FabricDomain) error {
	if object == nil || object.GetSpec() == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "spec is mandatory")
	}
	spec := object.GetSpec()
	if len(spec.GetServers()) == 0 {
		return grpcstatus.Error(grpccodes.InvalidArgument, "servers list must not be empty")
	}
	if len(spec.GetVirtualNetworks()) != 1 {
		return grpcstatus.Error(grpccodes.InvalidArgument, "exactly one VirtualNetwork required in Phase 1")
	}
	if spec.GetType() != privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW {
		return grpcstatus.Error(grpccodes.Unimplemented, "type not yet supported")
	}

	vnResponse, err := s.virtualNetworkDao.Get().SetId(spec.GetVirtualNetworks()[0]).Do(ctx)
	if err != nil {
		return err
	}
	vn := vnResponse.GetObject()
	networkClassID := vn.GetSpec().GetNetworkClass().GetId()
	if networkClassID == "" {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "VirtualNetwork has no NetworkClass")
	}
	ncResponse, err := s.networkClassDao.Get().SetId(networkClassID).Do(ctx)
	if err != nil {
		return err
	}
	nc := ncResponse.GetObject()
	if !nc.GetCapabilities().GetSupportsEastWestEthernet() {
		return grpcstatus.Error(grpccodes.InvalidArgument, "type does not match NetworkClass capability")
	}
	if nc.GetSpec().GetEastWestConfig().GetEthernetEw().GetTemplateId() == "" {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "NetworkClass missing template_id for ethernet_ew")
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
