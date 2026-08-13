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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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
	// Get the object identifier:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Fetch the existing object:
	getRequest := &privatev1.VolumesGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.VolumesGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existing := getResponse.GetObject()

	// Merge the update into a clone of the existing object:
	merged := cloneVolume(existing)
	applyVolumeUpdate(merged, request.GetObject(), request.GetUpdateMask())

	// Validate immutable fields:
	err = validateVolumeImmutability(merged, existing)
	if err != nil {
		return
	}

	// Set the merged spec back into the request for the generic update:
	request.GetObject().SetSpec(merged.GetSpec())

	err = s.generic.Update(ctx, request, &response)
	return
}

// cloneVolume creates a deep copy of a Volume.
func cloneVolume(v *privatev1.Volume) *privatev1.Volume {
	return proto.Clone(v).(*privatev1.Volume)
}

// applyVolumeUpdate applies the update fields onto the base object, respecting the field mask.
// If no mask is provided, all fields from the update are applied.
// Field mask paths use the spec/status prefix (e.g., "spec.storage_tier", "status.state") per API conventions.
func applyVolumeUpdate(base, update *privatev1.Volume, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		proto.Merge(base, update)
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "spec.storage_tier":
			base.GetSpec().SetStorageTier(update.GetSpec().GetStorageTier())
		case "spec.size_gib":
			base.GetSpec().SetSizeGib(update.GetSpec().GetSizeGib())
		case "spec.access_mode":
			base.GetSpec().SetAccessMode(update.GetSpec().GetAccessMode())
		case "spec.pvc_ref":
			// pvc_ref is intentionally mutable: set by the CSI driver post-creation.
			base.GetSpec().SetPvcRef(update.GetSpec().GetPvcRef())
		default:
			// Unknown paths are handled by the generic update layer.
		}
	}
}

// validateVolumeImmutability checks that immutable spec fields have not been changed.
// storage_tier, size_gib, and access_mode are immutable after creation because they are
// provisioned directly into the vendor CSI call and cannot be modified post-creation.
func validateVolumeImmutability(merged, existing *privatev1.Volume) error {
	if merged.GetSpec().GetStorageTier() != existing.GetSpec().GetStorageTier() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.storage_tier' is immutable and cannot be changed after creation")
	}
	if merged.GetSpec().GetSizeGib() != existing.GetSpec().GetSizeGib() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.size_gib' is immutable and cannot be changed after creation")
	}
	if merged.GetSpec().GetAccessMode() != existing.GetSpec().GetAccessMode() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.access_mode' is immutable and cannot be changed after creation")
	}
	return nil
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
