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
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateDiskImagesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.DiskImagesServer = (*PrivateDiskImagesServer)(nil)

type PrivateDiskImagesServer struct {
	privatev1.UnimplementedDiskImagesServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.DiskImage]
}

func NewPrivateDiskImagesServer() *PrivateDiskImagesServerBuilder {
	return &PrivateDiskImagesServerBuilder{}
}

func (b *PrivateDiskImagesServerBuilder) SetLogger(value *slog.Logger) *PrivateDiskImagesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateDiskImagesServerBuilder) SetNotifier(value events.Notifier) *PrivateDiskImagesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateDiskImagesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateDiskImagesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateDiskImagesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateDiskImagesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateDiskImagesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateDiskImagesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateDiskImagesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateDiskImagesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateDiskImagesServerBuilder) Build() (result *PrivateDiskImagesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.DiskImage]().
		SetLogger(b.logger).
		SetService(privatev1.DiskImages_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	result = &PrivateDiskImagesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateDiskImagesServer) List(ctx context.Context,
	request *privatev1.DiskImagesListRequest) (response *privatev1.DiskImagesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateDiskImagesServer) Get(ctx context.Context,
	request *privatev1.DiskImagesGetRequest) (response *privatev1.DiskImagesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateDiskImagesServer) Create(ctx context.Context,
	request *privatev1.DiskImagesCreateRequest) (response *privatev1.DiskImagesCreateResponse, err error) {
	if request.GetObject() == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}

	spec := request.GetObject().GetSpec()
	if spec == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object spec is mandatory")
		return
	}

	if spec.GetSourceType() == privatev1.SourceType_SOURCE_TYPE_UNSPECIFIED {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.source_type' is required")
		return
	}

	if spec.GetLifecycle() == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_UNSPECIFIED {
		spec.SetLifecycle(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)
	}
	if spec.GetLifecycle() != privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.lifecycle' must be AVAILABLE on create")
		return
	}

	if spec.GetGuestOsFamily() == privatev1.GuestOSFamily_GUEST_OS_FAMILY_UNSPECIFIED {
		spec.SetGuestOsFamily(privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX)
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateDiskImagesServer) Update(ctx context.Context,
	request *privatev1.DiskImagesUpdateRequest) (response *privatev1.DiskImagesUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.DiskImagesGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.DiskImagesGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existing := getResponse.GetObject()

	merged := cloneDiskImage(existing)
	applyDiskImageUpdate(merged, request.GetObject(), request.GetUpdateMask())

	err = validateDiskImageImmutability(merged, existing)
	if err != nil {
		return
	}

	if len(merged.GetSpec().GetArchitecture()) == 0 {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.architecture' must contain at least one architecture")
		return
	}

	lifecycleChanged := handleDiskImageLifecycleTransition(existing, merged)

	if lifecycleChanged && len(request.GetUpdateMask().GetPaths()) > 0 {
		mask := request.GetUpdateMask()
		mask.Paths = append(mask.Paths, "spec.deprecation")
	}

	// SetObject(merged) instead of SetSpec(merged.GetSpec()): GenericServer's no-mask
	// path does tmpObject = requestObject, so the request needs complete metadata from
	// the DB clone — SetSpec would leave metadata empty, triggering check_immutable_columns.
	// Restore the client's original version so optimistic locking still works.
	clientVersion := request.GetObject().GetMetadata().GetVersion()
	request.SetObject(merged)
	request.GetObject().GetMetadata().SetVersion(clientVersion)

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateDiskImagesServer) Delete(ctx context.Context,
	request *privatev1.DiskImagesDeleteRequest) (response *privatev1.DiskImagesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateDiskImagesServer) Signal(ctx context.Context,
	request *privatev1.DiskImagesSignalRequest) (response *privatev1.DiskImagesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func cloneDiskImage(di *privatev1.DiskImage) *privatev1.DiskImage {
	return proto.Clone(di).(*privatev1.DiskImage)
}

func applyDiskImageUpdate(base, update *privatev1.DiskImage, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		proto.Merge(base, update)
		// proto.Merge appends repeated fields instead of replacing them.
		// Replace the architecture repeated field from the update to avoid duplication.
		if update.GetSpec() != nil && len(update.GetSpec().GetArchitecture()) > 0 {
			base.GetSpec().SetArchitecture(update.GetSpec().GetArchitecture())
		}
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "spec.lifecycle":
			base.GetSpec().SetLifecycle(update.GetSpec().GetLifecycle())
		case "spec.architecture":
			base.GetSpec().SetArchitecture(update.GetSpec().GetArchitecture())
		case "spec.deprecation":
			base.GetSpec().SetDeprecation(update.GetSpec().GetDeprecation())
		case "spec.deprecation.deprecation_timestamp":
			dep := base.GetSpec().GetDeprecation()
			if dep == nil {
				dep = &privatev1.DiskImageDeprecation{}
				base.GetSpec().SetDeprecation(dep)
			}
			dep.SetDeprecationTimestamp(update.GetSpec().GetDeprecation().GetDeprecationTimestamp())
		case "spec.deprecation.obsolescence_timestamp":
			dep := base.GetSpec().GetDeprecation()
			if dep == nil {
				dep = &privatev1.DiskImageDeprecation{}
				base.GetSpec().SetDeprecation(dep)
			}
			dep.SetObsolescenceTimestamp(update.GetSpec().GetDeprecation().GetObsolescenceTimestamp())
		}
	}
}

func validateDiskImageImmutability(merged, existing *privatev1.DiskImage) error {
	if merged.GetSpec().GetSourceType() != existing.GetSpec().GetSourceType() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.source_type' is immutable and cannot be changed")
	}
	if merged.GetSpec().GetSourceRef() != existing.GetSpec().GetSourceRef() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.source_ref' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetSpec().GetSourceRef(), merged.GetSpec().GetSourceRef())
	}
	if merged.GetSpec().GetGuestOsFamily() != existing.GetSpec().GetGuestOsFamily() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.guest_os_family' is immutable and cannot be changed")
	}
	if merged.GetMetadata().GetName() != existing.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'name' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetMetadata().GetName(), merged.GetMetadata().GetName())
	}
	return nil
}

// handleDiskImageLifecycleTransition handles lifecycle transitions with timestamp auto-population.
// Unlike InstanceType, transitioning to AVAILABLE clears the deprecation field entirely.
func handleDiskImageLifecycleTransition(existing, merged *privatev1.DiskImage) bool {
	oldLifecycle := existing.GetSpec().GetLifecycle()
	newLifecycle := merged.GetSpec().GetLifecycle()

	if newLifecycle == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_UNSPECIFIED {
		merged.GetSpec().SetLifecycle(oldLifecycle)
		return false
	}

	if oldLifecycle == newLifecycle {
		return false
	}

	switch newLifecycle {
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED:
		dep := merged.GetSpec().GetDeprecation()
		if dep == nil {
			dep = &privatev1.DiskImageDeprecation{}
			merged.GetSpec().SetDeprecation(dep)
		}
		dep.SetDeprecationTimestamp(timestamppb.Now())
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE:
		dep := merged.GetSpec().GetDeprecation()
		if dep == nil {
			dep = &privatev1.DiskImageDeprecation{}
			merged.GetSpec().SetDeprecation(dep)
		}
		dep.SetObsolescenceTimestamp(timestamppb.Now())
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE:
		merged.GetSpec().ClearDeprecation()
	}

	return true
}
