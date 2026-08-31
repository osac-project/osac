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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

// VolumesServer is the public, read-only Volume API. It exposes only List and Get; volume
// lifecycle (create, update, delete) remains on the private API. It delegates to the
// PrivateVolumesServer, which enforces tenant scoping, and maps private Volumes to their public
// representation, dropping internal routing fields (backend, protocol, hub, vendor_volume_id).
type VolumesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.VolumesServer = (*VolumesServer)(nil)

type VolumesServer struct {
	publicv1.UnimplementedVolumesServer

	logger    *slog.Logger
	delegate  privatev1.VolumesServer
	outMapper *GenericMapper[*privatev1.Volume, *publicv1.Volume]
}

func NewVolumesServer() *VolumesServerBuilder {
	return &VolumesServerBuilder{}
}

func (b *VolumesServerBuilder) SetLogger(value *slog.Logger) *VolumesServerBuilder {
	b.logger = value
	return b
}

func (b *VolumesServerBuilder) SetNotifier(value events.Notifier) *VolumesServerBuilder {
	b.notifier = value
	return b
}

func (b *VolumesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *VolumesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *VolumesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *VolumesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *VolumesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *VolumesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *VolumesServerBuilder) Build() (result *VolumesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	outMapper, err := NewGenericMapper[*privatev1.Volume, *publicv1.Volume]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// The read-only delegate needs no tier resolver (that is only used by the private Create path).
	// FilterDesc is set to the public Volume descriptor so that CEL filters can only reference
	// fields that are visible through the public API.
	delegate, err := NewPrivateVolumesServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.Volume)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &VolumesServer{
		logger:    b.logger,
		delegate:  delegate,
		outMapper: outMapper,
	}
	return
}

func (s *VolumesServer) List(ctx context.Context,
	request *publicv1.VolumesListRequest) (response *publicv1.VolumesListResponse, err error) {
	privateRequest := &privatev1.VolumesListRequest{}
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
	publicItems := make([]*publicv1.Volume, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Volume{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private volume to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process volumes")
		}
		publicItems[i] = publicItem
	}

	response = &publicv1.VolumesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *VolumesServer) Get(ctx context.Context,
	request *publicv1.VolumesGetRequest) (response *publicv1.VolumesGetResponse, err error) {
	privateRequest := &privatev1.VolumesGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateVolume := privateResponse.GetObject()
	publicVolume := &publicv1.Volume{}
	err = s.outMapper.Copy(ctx, privateVolume, publicVolume)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private volume to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process volume")
	}

	response = &publicv1.VolumesGetResponse{}
	response.SetObject(publicVolume)
	return
}
