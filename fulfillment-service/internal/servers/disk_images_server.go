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
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

var diskImageFilterDefaults = []filterDefault{
	{
		field:     "this.spec.lifecycle",
		predicate: fmt.Sprintf("this.spec.lifecycle != %d", int32(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE)),
	},
}

type DiskImagesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.DiskImagesServer = (*DiskImagesServer)(nil)

type DiskImagesServer struct {
	publicv1.UnimplementedDiskImagesServer

	logger    *slog.Logger
	delegate  privatev1.DiskImagesServer
	inMapper  *GenericMapper[*publicv1.DiskImage, *privatev1.DiskImage]
	outMapper *GenericMapper[*privatev1.DiskImage, *publicv1.DiskImage]
}

func NewDiskImagesServer() *DiskImagesServerBuilder {
	return &DiskImagesServerBuilder{}
}

func (b *DiskImagesServerBuilder) SetLogger(value *slog.Logger) *DiskImagesServerBuilder {
	b.logger = value
	return b
}

func (b *DiskImagesServerBuilder) SetNotifier(value events.Notifier) *DiskImagesServerBuilder {
	b.notifier = value
	return b
}

func (b *DiskImagesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *DiskImagesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *DiskImagesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *DiskImagesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *DiskImagesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *DiskImagesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *DiskImagesServerBuilder) Build() (result *DiskImagesServer, err error) {
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

	inMapper, err := NewGenericMapper[*publicv1.DiskImage, *privatev1.DiskImage]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.DiskImage, *publicv1.DiskImage]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateDiskImagesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &DiskImagesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *DiskImagesServer) List(ctx context.Context,
	request *publicv1.DiskImagesListRequest) (response *publicv1.DiskImagesListResponse, err error) {
	privateRequest := &privatev1.DiskImagesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	composedFilter, err := composeFilterDefaults(request.GetFilter(), diskImageFilterDefaults)
	if err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%v", err)
	}
	privateRequest.SetFilter(composedFilter)
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.DiskImage, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.DiskImage{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private disk image to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process disk images")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.DiskImagesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *DiskImagesServer) Get(ctx context.Context,
	request *publicv1.DiskImagesGetRequest) (response *publicv1.DiskImagesGetResponse, err error) {
	privateRequest := &privatev1.DiskImagesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateDiskImage := privateResponse.GetObject()
	publicDiskImage := &publicv1.DiskImage{}
	err = s.outMapper.Copy(ctx, privateDiskImage, publicDiskImage)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private disk image to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process disk image")
	}

	response = &publicv1.DiskImagesGetResponse{}
	response.SetObject(publicDiskImage)
	return
}

func (s *DiskImagesServer) Create(ctx context.Context,
	request *publicv1.DiskImagesCreateRequest) (response *publicv1.DiskImagesCreateResponse, err error) {
	publicDiskImage := request.GetObject()
	if publicDiskImage == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateDiskImage := &privatev1.DiskImage{}
	err = s.inMapper.Copy(ctx, publicDiskImage, privateDiskImage)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public disk image to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process disk image")
		return
	}

	privateRequest := &privatev1.DiskImagesCreateRequest{}
	privateRequest.SetObject(privateDiskImage)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPrivateDiskImage := privateResponse.GetObject()
	createdPublicDiskImage := &publicv1.DiskImage{}
	err = s.outMapper.Copy(ctx, createdPrivateDiskImage, createdPublicDiskImage)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private disk image to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process disk image")
		return
	}

	response = &publicv1.DiskImagesCreateResponse{}
	response.SetObject(createdPublicDiskImage)
	return
}

func (s *DiskImagesServer) Update(ctx context.Context,
	request *publicv1.DiskImagesUpdateRequest) (response *publicv1.DiskImagesUpdateResponse, err error) {
	publicDiskImage := request.GetObject()
	if publicDiskImage == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicDiskImage.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	var privateDiskImage *privatev1.DiskImage
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateDiskImage = &privatev1.DiskImage{}
		privateDiskImage.SetId(id)
	} else {
		getRequest := &privatev1.DiskImagesGetRequest{}
		getRequest.SetId(id)
		getResponse, err := s.delegate.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateDiskImage = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicDiskImage, privateDiskImage)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public disk image to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process disk image")
		return
	}

	privateRequest := &privatev1.DiskImagesUpdateRequest{}
	privateRequest.SetObject(privateDiskImage)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPrivateDiskImage := privateResponse.GetObject()
	updatedPublicDiskImage := &publicv1.DiskImage{}
	err = s.outMapper.Copy(ctx, updatedPrivateDiskImage, updatedPublicDiskImage)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private disk image to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process disk image")
		return
	}

	response = &publicv1.DiskImagesUpdateResponse{}
	response.SetObject(updatedPublicDiskImage)
	return
}

func (s *DiskImagesServer) Delete(ctx context.Context,
	request *publicv1.DiskImagesDeleteRequest) (response *publicv1.DiskImagesDeleteResponse, err error) {
	privateRequest := &privatev1.DiskImagesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.DiskImagesDeleteResponse{}
	return
}
