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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type FabricDomainsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.FabricDomainsServer = (*FabricDomainsServer)(nil)

type FabricDomainsServer struct {
	publicv1.UnimplementedFabricDomainsServer
	delegate  privatev1.FabricDomainsServer
	inMapper  *GenericMapper[*publicv1.FabricDomain, *privatev1.FabricDomain]
	outMapper *GenericMapper[*privatev1.FabricDomain, *publicv1.FabricDomain]
}

func NewFabricDomainsServer() *FabricDomainsServerBuilder { return &FabricDomainsServerBuilder{} }

func (b *FabricDomainsServerBuilder) SetLogger(value *slog.Logger) *FabricDomainsServerBuilder {
	b.logger = value
	return b
}
func (b *FabricDomainsServerBuilder) SetNotifier(value events.Notifier) *FabricDomainsServerBuilder {
	b.notifier = value
	return b
}
func (b *FabricDomainsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *FabricDomainsServerBuilder {
	b.attributionLogic = value
	return b
}
func (b *FabricDomainsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *FabricDomainsServerBuilder {
	b.tenancyLogic = value
	return b
}
func (b *FabricDomainsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *FabricDomainsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *FabricDomainsServerBuilder) Build() (*FabricDomainsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	inMapper, err := NewGenericMapper[*publicv1.FabricDomain, *privatev1.FabricDomain]().SetLogger(b.logger).SetStrict(true).Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.FabricDomain, *publicv1.FabricDomain]().SetLogger(b.logger).SetStrict(false).Build()
	if err != nil {
		return nil, err
	}
	delegate, err := NewPrivateFabricDomainsServer().
		SetLogger(b.logger).SetNotifier(b.notifier).SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).SetMetricsRegisterer(b.metricsRegisterer).Build()
	if err != nil {
		return nil, err
	}
	return &FabricDomainsServer{delegate: delegate, inMapper: inMapper, outMapper: outMapper}, nil
}

func (s *FabricDomainsServer) List(ctx context.Context, request *publicv1.FabricDomainsListRequest) (*publicv1.FabricDomainsListResponse, error) {
	privateRequest := &privatev1.FabricDomainsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items := make([]*publicv1.FabricDomain, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		items[i] = &publicv1.FabricDomain{}
		if err := s.outMapper.Copy(ctx, item, items[i]); err != nil {
			return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domains")
		}
	}
	return publicv1.FabricDomainsListResponse_builder{Size: privateResponse.GetSize(), Total: privateResponse.GetTotal(), Items: items}.Build(), nil
}

func (s *FabricDomainsServer) Get(ctx context.Context, request *publicv1.FabricDomainsGetRequest) (*publicv1.FabricDomainsGetResponse, error) {
	privateRequest := &privatev1.FabricDomainsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	item := &publicv1.FabricDomain{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), item); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domain")
	}
	return publicv1.FabricDomainsGetResponse_builder{Object: item}.Build(), nil
}

func (s *FabricDomainsServer) Create(ctx context.Context, request *publicv1.FabricDomainsCreateRequest) (*publicv1.FabricDomainsCreateResponse, error) {
	if request.GetObject() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateObject := &privatev1.FabricDomain{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), privateObject); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domain")
	}
	privateResponse, err := s.delegate.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: privateObject}.Build())
	if err != nil {
		return nil, err
	}
	item := &publicv1.FabricDomain{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), item); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domain")
	}
	return publicv1.FabricDomainsCreateResponse_builder{Object: item}.Build(), nil
}

func (s *FabricDomainsServer) Update(ctx context.Context, request *publicv1.FabricDomainsUpdateRequest) (*publicv1.FabricDomainsUpdateResponse, error) {
	if request.GetObject() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	privateObject := &privatev1.FabricDomain{}
	if err := s.inMapper.Copy(ctx, request.GetObject(), privateObject); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domain")
	}
	privateRequest := privatev1.FabricDomainsUpdateRequest_builder{Object: privateObject, UpdateMask: request.GetUpdateMask(), Lock: request.GetLock()}.Build()
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	item := &publicv1.FabricDomain{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), item); err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to process fabric domain")
	}
	return publicv1.FabricDomainsUpdateResponse_builder{Object: item}.Build(), nil
}

func (s *FabricDomainsServer) Delete(ctx context.Context, request *publicv1.FabricDomainsDeleteRequest) (*publicv1.FabricDomainsDeleteResponse, error) {
	privateRequest := &privatev1.FabricDomainsDeleteRequest{}
	privateRequest.SetId(request.GetId())
	_, err := s.delegate.Delete(ctx, privateRequest)
	return &publicv1.FabricDomainsDeleteResponse{}, err
}
