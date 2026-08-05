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

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateBareMetalInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	referenceChecker  catalogItemReferenceChecker
}

var _ privatev1.BareMetalInstanceCatalogItemsServer = (*PrivateBareMetalInstanceCatalogItemsServer)(nil)

type PrivateBareMetalInstanceCatalogItemsServer struct {
	privatev1.UnimplementedBareMetalInstanceCatalogItemsServer
	logger           *slog.Logger
	generic          *GenericServer[*privatev1.BareMetalInstanceCatalogItem]
	referenceChecker catalogItemReferenceChecker
}

func NewPrivateBareMetalInstanceCatalogItemsServer() *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	return &PrivateBareMetalInstanceCatalogItemsServerBuilder{}
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetNotifier(value events.Notifier) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetReferenceChecker(value catalogItemReferenceChecker) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.referenceChecker = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) Build() (result *PrivateBareMetalInstanceCatalogItemsServer, err error) {
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

	generic, err := NewGenericServer[*privatev1.BareMetalInstanceCatalogItem]().
		SetLogger(b.logger).
		SetService(privatev1.BareMetalInstanceCatalogItems_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	refChecker := b.referenceChecker
	if refChecker == nil {
		bmiDao, daoErr := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
			SetLogger(b.logger).
			SetTenancyLogic(b.tenancyLogic).
			SetMetricsRegisterer(b.metricsRegisterer).
			Build()
		if daoErr != nil {
			err = daoErr
			return
		}
		refChecker = &daoReferenceChecker[*privatev1.BareMetalInstance]{resourceDao: bmiDao}
	}

	result = &PrivateBareMetalInstanceCatalogItemsServer{
		logger:           b.logger,
		generic:          generic,
		referenceChecker: refChecker,
	}
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) List(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsListRequest) (response *privatev1.BareMetalInstanceCatalogItemsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Get(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsGetRequest) (response *privatev1.BareMetalInstanceCatalogItemsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Create(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsCreateRequest) (response *privatev1.BareMetalInstanceCatalogItemsCreateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if err = validateFieldDefinitions(object.GetFieldDefinitions()); err != nil {
			return
		}
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Update(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsUpdateRequest) (response *privatev1.BareMetalInstanceCatalogItemsUpdateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if err = validateFieldDefinitions(object.GetFieldDefinitions()); err != nil {
			return
		}
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsDeleteRequest) (response *privatev1.BareMetalInstanceCatalogItemsDeleteResponse, err error) {
	hasRef, err := s.referenceChecker.hasReference(ctx, request.GetId())
	if err != nil {
		return
	}
	if hasRef {
		err = grpcstatus.Errorf(
			grpccodes.FailedPrecondition,
			"cannot delete catalog item '%s': it is still referenced by one or more bare metal instances",
			request.GetId(),
		)
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Signal(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsSignalRequest) (response *privatev1.BareMetalInstanceCatalogItemsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
