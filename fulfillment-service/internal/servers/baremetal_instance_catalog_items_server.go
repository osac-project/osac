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

type BareMetalInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.BareMetalInstanceCatalogItemsServer = (*BareMetalInstanceCatalogItemsServer)(nil)

type BareMetalInstanceCatalogItemsServer struct {
	publicv1.UnimplementedBareMetalInstanceCatalogItemsServer

	logger           *slog.Logger
	referenceChecker catalogItemReferenceChecker
	delegate         privatev1.BareMetalInstanceCatalogItemsServer
	inMapper         *GenericMapper[*publicv1.BareMetalInstanceCatalogItem, *privatev1.BareMetalInstanceCatalogItem]
	outMapper        *GenericMapper[*privatev1.BareMetalInstanceCatalogItem, *publicv1.BareMetalInstanceCatalogItem]
}

func NewBareMetalInstanceCatalogItemsServer() *BareMetalInstanceCatalogItemsServerBuilder {
	return &BareMetalInstanceCatalogItemsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BareMetalInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *BareMetalInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *BareMetalInstanceCatalogItemsServerBuilder) SetNotifier(value events.Notifier) *BareMetalInstanceCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *BareMetalInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *BareMetalInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *BareMetalInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BareMetalInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *BareMetalInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *BareMetalInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *BareMetalInstanceCatalogItemsServerBuilder) Build() (result *BareMetalInstanceCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.BareMetalInstanceCatalogItem, *privatev1.BareMetalInstanceCatalogItem]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.BareMetalInstanceCatalogItem, *publicv1.BareMetalInstanceCatalogItem]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	bareMetalInstancesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	referenceChecker := &daoReferenceChecker[*privatev1.BareMetalInstance]{resourceDao: bareMetalInstancesDao}

	delegate, err := NewPrivateBareMetalInstanceCatalogItemsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetReferenceChecker(referenceChecker).
		Build()
	if err != nil {
		return
	}

	result = &BareMetalInstanceCatalogItemsServer{
		logger:           b.logger,
		referenceChecker: referenceChecker,
		delegate:         delegate,
		inMapper:         inMapper,
		outMapper:        outMapper,
	}
	return
}

func (s *BareMetalInstanceCatalogItemsServer) List(ctx context.Context,
	request *publicv1.BareMetalInstanceCatalogItemsListRequest) (response *publicv1.BareMetalInstanceCatalogItemsListResponse, err error) {
	privateRequest := &privatev1.BareMetalInstanceCatalogItemsListRequest{}
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
	publicItems := make([]*publicv1.BareMetalInstanceCatalogItem, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.BareMetalInstanceCatalogItem{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private bare metal instance catalog item to public",
				slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog items")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.BareMetalInstanceCatalogItemsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *BareMetalInstanceCatalogItemsServer) Get(ctx context.Context,
	request *publicv1.BareMetalInstanceCatalogItemsGetRequest) (response *publicv1.BareMetalInstanceCatalogItemsGetResponse, err error) {
	privateRequest := &privatev1.BareMetalInstanceCatalogItemsGetRequest{}
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

	publicCatalogItem := &publicv1.BareMetalInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance catalog item to public",
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog item")
	}

	response = &publicv1.BareMetalInstanceCatalogItemsGetResponse{}
	response.SetObject(publicCatalogItem)
	return
}

func (s *BareMetalInstanceCatalogItemsServer) Create(ctx context.Context,
	request *publicv1.BareMetalInstanceCatalogItemsCreateRequest) (response *publicv1.BareMetalInstanceCatalogItemsCreateResponse, err error) {
	publicCatalogItem := request.GetObject()
	if publicCatalogItem == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateCatalogItem := &privatev1.BareMetalInstanceCatalogItem{}
	err = s.inMapper.Copy(ctx, publicCatalogItem, privateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public bare metal instance catalog item to private",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog item")
		return
	}

	privateRequest := &privatev1.BareMetalInstanceCatalogItemsCreateRequest{}
	privateRequest.SetObject(privateCatalogItem)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublicCatalogItem := &publicv1.BareMetalInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance catalog item to public",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog item")
		return
	}

	response = &publicv1.BareMetalInstanceCatalogItemsCreateResponse{}
	response.SetObject(createdPublicCatalogItem)
	return
}

func (s *BareMetalInstanceCatalogItemsServer) Update(ctx context.Context,
	request *publicv1.BareMetalInstanceCatalogItemsUpdateRequest) (response *publicv1.BareMetalInstanceCatalogItemsUpdateResponse, err error) {
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

	getRequest := &privatev1.BareMetalInstanceCatalogItemsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateCatalogItem := getResponse.GetObject()

	err = s.inMapper.Copy(ctx, publicCatalogItem, existingPrivateCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public bare metal instance catalog item to private",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog item")
		return
	}

	privateRequest := &privatev1.BareMetalInstanceCatalogItemsUpdateRequest{}
	privateRequest.SetObject(existingPrivateCatalogItem)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublicCatalogItem := &publicv1.BareMetalInstanceCatalogItem{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublicCatalogItem)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private bare metal instance catalog item to public",
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process bare metal instance catalog item")
		return
	}

	response = &publicv1.BareMetalInstanceCatalogItemsUpdateResponse{}
	response.SetObject(updatedPublicCatalogItem)
	return
}

func (s *BareMetalInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *publicv1.BareMetalInstanceCatalogItemsDeleteRequest) (response *publicv1.BareMetalInstanceCatalogItemsDeleteResponse, err error) {
	privateRequest := &privatev1.BareMetalInstanceCatalogItemsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.BareMetalInstanceCatalogItemsDeleteResponse{}
	return
}

func (s *BareMetalInstanceCatalogItemsServer) addPublishedFilter(filter string) (string, error) {
	if filter == "" {
		return "this.published", nil
	}
	if err := validateCELSyntax(filter); err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
	}
	return "(" + filter + ") && this.published", nil
}
