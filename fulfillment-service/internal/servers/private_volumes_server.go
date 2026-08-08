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
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateVolumesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.VolumesServer = (*PrivateVolumesServer)(nil)

type PrivateVolumesServer struct {
	privatev1.UnimplementedVolumesServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.Volume]
}

func NewPrivateVolumesServer() *PrivateVolumesServerBuilder {
	return &PrivateVolumesServerBuilder{}
}

func (b *PrivateVolumesServerBuilder) SetLogger(value *slog.Logger) *PrivateVolumesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateVolumesServerBuilder) SetNotifier(value events.Notifier) *PrivateVolumesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateVolumesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateVolumesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateVolumesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateVolumesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateVolumesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateVolumesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateVolumesServerBuilder) Build() (result *PrivateVolumesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.Volume]().
		SetLogger(b.logger).
		SetService(privatev1.Volumes_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateVolumesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateVolumesServer) List(ctx context.Context,
	request *privatev1.VolumesListRequest) (response *privatev1.VolumesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateVolumesServer) Get(ctx context.Context,
	request *privatev1.VolumesGetRequest) (response *privatev1.VolumesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateVolumesServer) Create(ctx context.Context,
	request *privatev1.VolumesCreateRequest) (response *privatev1.VolumesCreateResponse, err error) {
	vol := request.GetObject()

	err = s.validateVolumeCreate(vol)
	if err != nil {
		return
	}

	if vol.GetStatus() == nil {
		vol.SetStatus(&privatev1.VolumeStatus{})
	}
	vol.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_CREATING)

	vol.SetId("")

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateVolumesServer) validateVolumeCreate(vol *privatev1.Volume) error {
	if vol == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "volume is mandatory")
	}
	if vol.GetMetadata() == nil || vol.GetMetadata().GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'metadata.name' is required")
	}
	spec := vol.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec' is required")
	}
	if spec.GetStorageTier() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.storage_tier' is required")
	}
	if spec.GetSizeGib() <= 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.size_gib' must be greater than zero")
	}
	if spec.GetAccessMode() == privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_UNSPECIFIED {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.access_mode' is required")
	}
	return nil
}

func (s *PrivateVolumesServer) Update(ctx context.Context,
	request *privatev1.VolumesUpdateRequest) (response *privatev1.VolumesUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateVolumesServer) Delete(ctx context.Context,
	request *privatev1.VolumesDeleteRequest) (response *privatev1.VolumesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateVolumesServer) Signal(ctx context.Context,
	request *privatev1.VolumesSignalRequest) (response *privatev1.VolumesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
