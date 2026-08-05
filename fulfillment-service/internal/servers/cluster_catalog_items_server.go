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

type ClusterCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.ClusterCatalogItemsServer = (*ClusterCatalogItemsServer)(nil)

type ClusterCatalogItemsServer struct {
	publicv1.UnimplementedClusterCatalogItemsServer

	logger           *slog.Logger
	referenceChecker catalogItemReferenceChecker
	delegate         privatev1.ClusterCatalogItemsServer
	inMapper         *GenericMapper[*publicv1.ClusterCatalogItem, *privatev1.ClusterCatalogItem]
	outMapper        *GenericMapper[*privatev1.ClusterCatalogItem, *publicv1.ClusterCatalogItem]
}

func NewClusterCatalogItemsServer() *ClusterCatalogItemsServerBuilder {
	return &ClusterCatalogItemsServerBuilder{}
}

func (b *ClusterCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *ClusterCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *ClusterCatalogItemsServerBuilder) SetNotifier(value events.Notifier) *ClusterCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *ClusterCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ClusterCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *ClusterCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ClusterCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *ClusterCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ClusterCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ClusterCatalogItemsServerBuilder) Build() (result *ClusterCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.ClusterCatalogItem, *privatev1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ClusterCatalogItem, *publicv1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	referenceChecker := &daoReferenceChecker[*privatev1.Cluster]{resourceDao: clustersDao}

	delegate, err := NewPrivateClusterCatalogItemsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &ClusterCatalogItemsServer{
		logger:           b.logger,
		referenceChecker: referenceChecker,
		delegate:         delegate,
		inMapper:         inMapper,
		outMapper:        outMapper,
	}
	return
}

func (s *ClusterCatalogItemsServer) List(ctx context.Context,
	request *publicv1.ClusterCatalogItemsListRequest) (response *publicv1.ClusterCatalogItemsListResponse, err error) {
	privateRequest := &privatev1.ClusterCatalogItemsListRequest{}
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
	publicItems := make([]*publicv1.ClusterCatalogItem, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ClusterCatalogItem{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private cluster catalog item to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog items")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.ClusterCatalogItemsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ClusterCatalogItemsServer) Get(ctx context.Context,
	request *publicv1.ClusterCatalogItemsGetRequest) (response *publicv1.ClusterCatalogItemsGetResponse, err error) {
	privateRequest := &privatev1.ClusterCatalogItemsGetRequest{}
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

	publicCatalogItem := &publicv1.ClusterCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private cluster catalog item to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog item")
	}

	response = &publicv1.ClusterCatalogItemsGetResponse{}
	response.SetObject(publicCatalogItem)
	return
}

func (s *ClusterCatalogItemsServer) Create(ctx context.Context,
	request *publicv1.ClusterCatalogItemsCreateRequest) (response *publicv1.ClusterCatalogItemsCreateResponse, err error) {
	publicCatalogItem := request.GetObject()
	if publicCatalogItem == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateCatalogItem := &privatev1.ClusterCatalogItem{}
	err = s.inMapper.Copy(ctx, publicCatalogItem, privateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public cluster catalog item to private", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog item")
		return
	}

	privateRequest := &privatev1.ClusterCatalogItemsCreateRequest{}
	privateRequest.SetObject(privateCatalogItem)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublicCatalogItem := &publicv1.ClusterCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private cluster catalog item to public", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog item")
		return
	}

	response = &publicv1.ClusterCatalogItemsCreateResponse{}
	response.SetObject(createdPublicCatalogItem)
	return
}

func (s *ClusterCatalogItemsServer) Update(ctx context.Context,
	request *publicv1.ClusterCatalogItemsUpdateRequest) (response *publicv1.ClusterCatalogItemsUpdateResponse, err error) {
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

	getRequest := &privatev1.ClusterCatalogItemsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateCatalogItem := getResponse.GetObject()

	err = s.inMapper.Copy(ctx, publicCatalogItem, existingPrivateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public cluster catalog item to private", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog item")
		return
	}

	privateRequest := &privatev1.ClusterCatalogItemsUpdateRequest{}
	privateRequest.SetObject(existingPrivateCatalogItem)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublicCatalogItem := &publicv1.ClusterCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private cluster catalog item to public", slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster catalog item")
		return
	}

	response = &publicv1.ClusterCatalogItemsUpdateResponse{}
	response.SetObject(updatedPublicCatalogItem)
	return
}

func (s *ClusterCatalogItemsServer) addPublishedFilter(filter string) (string, error) {
	if filter == "" {
		return "this.published", nil
	}
	if err := validateCELSyntax(filter); err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
	}
	return "(" + filter + ") && this.published", nil
}

func (s *ClusterCatalogItemsServer) Delete(ctx context.Context,
	request *publicv1.ClusterCatalogItemsDeleteRequest) (response *publicv1.ClusterCatalogItemsDeleteResponse, err error) {
	privateRequest := &privatev1.ClusterCatalogItemsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.ClusterCatalogItemsDeleteResponse{}
	return
}
