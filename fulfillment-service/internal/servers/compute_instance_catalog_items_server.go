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
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type ComputeInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ComputeInstanceCatalogItemsServer = (*ComputeInstanceCatalogItemsServer)(nil)

type ComputeInstanceCatalogItemsServer struct {
	publicv1.UnimplementedComputeInstanceCatalogItemsServer

	logger           *slog.Logger
	referenceChecker catalogItemReferenceChecker
	delegate         privatev1.ComputeInstanceCatalogItemsServer
	inMapper         *GenericMapper[*publicv1.ComputeInstanceCatalogItem, *privatev1.ComputeInstanceCatalogItem]
	outMapper        *GenericMapper[*privatev1.ComputeInstanceCatalogItem, *publicv1.ComputeInstanceCatalogItem]
}

func NewComputeInstanceCatalogItemsServer() *ComputeInstanceCatalogItemsServerBuilder {
	return &ComputeInstanceCatalogItemsServerBuilder{}
}

func (b *ComputeInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *ComputeInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *ComputeInstanceCatalogItemsServerBuilder) SetNotifier(value events.Notifier) *ComputeInstanceCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *ComputeInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ComputeInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *ComputeInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ComputeInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *ComputeInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ComputeInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ComputeInstanceCatalogItemsServerBuilder) Build() (result *ComputeInstanceCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.ComputeInstanceCatalogItem, *privatev1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ComputeInstanceCatalogItem, *publicv1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	computeInstancesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	referenceChecker := &daoReferenceChecker[*privatev1.ComputeInstance]{resourceDao: computeInstancesDao}

	delegate, err := NewPrivateComputeInstanceCatalogItemsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ComputeInstanceCatalogItemsServer{
		logger:           b.logger,
		referenceChecker: referenceChecker,
		delegate:         delegate,
		inMapper:         inMapper,
		outMapper:        outMapper,
	}
	return
}

func (s *ComputeInstanceCatalogItemsServer) List(ctx context.Context,
	request *publicv1.ComputeInstanceCatalogItemsListRequest) (response *publicv1.ComputeInstanceCatalogItemsListResponse, err error) {
	privateRequest := &privatev1.ComputeInstanceCatalogItemsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	composedFilter, err := s.addPublishedFilter(request.GetFilter())
	if err != nil {
		return nil, err
	}
	privateRequest.SetFilter(composedFilter)
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ComputeInstanceCatalogItem, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ComputeInstanceCatalogItem{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private compute instance catalog item to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog items")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ComputeInstanceCatalogItemsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ComputeInstanceCatalogItemsServer) Get(ctx context.Context,
	request *publicv1.ComputeInstanceCatalogItemsGetRequest) (response *publicv1.ComputeInstanceCatalogItemsGetResponse, err error) {
	privateRequest := &privatev1.ComputeInstanceCatalogItemsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	if !privateResponse.GetObject().GetPublished() {
		hasRef, refErr := s.referenceChecker.hasReference(ctx, request.GetId())
		if refErr != nil {
			return nil, refErr
		}
		if !hasRef {
			return nil, grpcstatus.Errorf(grpccodes.NotFound, "catalog item not found")
		}
	}

	publicCatalogItem := &publicv1.ComputeInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private compute instance catalog item to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog item")
	}

	response = &publicv1.ComputeInstanceCatalogItemsGetResponse{}
	response.SetObject(publicCatalogItem)
	return
}

func (s *ComputeInstanceCatalogItemsServer) Create(ctx context.Context,
	request *publicv1.ComputeInstanceCatalogItemsCreateRequest) (response *publicv1.ComputeInstanceCatalogItemsCreateResponse, err error) {
	publicCatalogItem := request.GetObject()
	if publicCatalogItem == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateCatalogItem := &privatev1.ComputeInstanceCatalogItem{}
	err = s.inMapper.Copy(ctx, publicCatalogItem, privateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public compute instance catalog item to private", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog item")
		return
	}

	privateRequest := &privatev1.ComputeInstanceCatalogItemsCreateRequest{}
	privateRequest.SetObject(privateCatalogItem)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublicCatalogItem := &publicv1.ComputeInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private compute instance catalog item to public", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog item")
		return
	}

	response = &publicv1.ComputeInstanceCatalogItemsCreateResponse{}
	response.SetObject(createdPublicCatalogItem)
	response.SetWarnings(privateResponse.GetWarnings())
	return
}

func (s *ComputeInstanceCatalogItemsServer) Update(ctx context.Context,
	request *publicv1.ComputeInstanceCatalogItemsUpdateRequest) (response *publicv1.ComputeInstanceCatalogItemsUpdateResponse, err error) {
	publicCatalogItem := request.GetObject()
	if publicCatalogItem == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicCatalogItem.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.ComputeInstanceCatalogItemsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateCatalogItem := getResponse.GetObject()

	err = s.inMapper.Copy(ctx, publicCatalogItem, existingPrivateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public compute instance catalog item to private", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog item")
		return
	}

	privateRequest := &privatev1.ComputeInstanceCatalogItemsUpdateRequest{}
	privateRequest.SetObject(existingPrivateCatalogItem)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublicCatalogItem := &publicv1.ComputeInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private compute instance catalog item to public", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance catalog item")
		return
	}

	response = &publicv1.ComputeInstanceCatalogItemsUpdateResponse{}
	response.SetObject(updatedPublicCatalogItem)
	response.SetWarnings(privateResponse.GetWarnings())
	return
}

func (s *ComputeInstanceCatalogItemsServer) addPublishedFilter(filter string) (string, error) {
	if filter == "" {
		return "this.published", nil
	}
	if err := validateCELSyntax(filter); err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
	}
	return "(" + filter + ") && this.published", nil
}

func (s *ComputeInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *publicv1.ComputeInstanceCatalogItemsDeleteRequest) (response *publicv1.ComputeInstanceCatalogItemsDeleteResponse, err error) {
	privateRequest := &privatev1.ComputeInstanceCatalogItemsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ComputeInstanceCatalogItemsDeleteResponse{}
	return
}
