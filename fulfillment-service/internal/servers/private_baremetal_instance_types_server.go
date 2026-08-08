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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateBareMetalInstanceTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.BareMetalInstanceTypesServer = (*PrivateBareMetalInstanceTypesServer)(nil)

type PrivateBareMetalInstanceTypesServer struct {
	privatev1.UnimplementedBareMetalInstanceTypesServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.BareMetalInstanceType]
}

func NewPrivateBareMetalInstanceTypesServer() *PrivateBareMetalInstanceTypesServerBuilder {
	return &PrivateBareMetalInstanceTypesServerBuilder{}
}

func (b *PrivateBareMetalInstanceTypesServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstanceTypesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstanceTypesServerBuilder) SetNotifier(value events.Notifier) *PrivateBareMetalInstanceTypesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateBareMetalInstanceTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateBareMetalInstanceTypesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateBareMetalInstanceTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateBareMetalInstanceTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateBareMetalInstanceTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateBareMetalInstanceTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateBareMetalInstanceTypesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateBareMetalInstanceTypesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateBareMetalInstanceTypesServerBuilder) Build() (result *PrivateBareMetalInstanceTypesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.BareMetalInstanceType]().
		SetLogger(b.logger).
		SetService(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateBareMetalInstanceTypesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateBareMetalInstanceTypesServer) List(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesListRequest) (response *privatev1.BareMetalInstanceTypesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTypesServer) Get(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesGetRequest) (response *privatev1.BareMetalInstanceTypesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTypesServer) Create(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesCreateRequest) (response *privatev1.BareMetalInstanceTypesCreateResponse, err error) {
	request.GetObject().SetId(request.GetObject().GetMetadata().GetName())
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTypesServer) Update(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesUpdateRequest) (response *privatev1.BareMetalInstanceTypesUpdateResponse, err error) {
	// Get the object identifier:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Fetch the existing object:
	getRequest := &privatev1.BareMetalInstanceTypesGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.BareMetalInstanceTypesGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existing := getResponse.GetObject()

	// Merge the update into a clone of the existing object:
	merged := cloneBareMetalInstanceType(existing)
	applyBareMetalInstanceTypeUpdate(merged, request.GetObject(), request.GetUpdateMask())

	// Validate immutable fields:
	err = validateBareMetalInstanceTypeImmutability(merged, existing)
	if err != nil {
		return
	}

	// Set the merged spec back into the request for the generic update:
	request.GetObject().SetSpec(merged.GetSpec())

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTypesServer) Delete(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesDeleteRequest) (response *privatev1.BareMetalInstanceTypesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTypesServer) Signal(ctx context.Context,
	request *privatev1.BareMetalInstanceTypesSignalRequest) (response *privatev1.BareMetalInstanceTypesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// cloneBareMetalInstanceType creates a deep copy of a BareMetalInstanceType.
func cloneBareMetalInstanceType(bmt *privatev1.BareMetalInstanceType) *privatev1.BareMetalInstanceType {
	return proto.Clone(bmt).(*privatev1.BareMetalInstanceType)
}

// applyBareMetalInstanceTypeUpdate applies the update fields onto the base object, respecting the field mask.
// If no mask is provided, all fields from the update are applied.
// Field mask paths use the spec prefix (e.g., "spec.description", "spec.hardware") per API conventions.
func applyBareMetalInstanceTypeUpdate(base, update *privatev1.BareMetalInstanceType, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		proto.Merge(base, update)
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "spec.description":
			base.GetSpec().SetDescription(update.GetSpec().GetDescription())
		case "spec.hardware":
			base.GetSpec().SetHardware(update.GetSpec().GetHardware())
		case "spec.host_label_selector":
			base.GetSpec().SetHostLabelSelector(update.GetSpec().GetHostLabelSelector())
		default:
			// For unknown paths, fall through - the generic handler will
			// reject invalid paths if needed.
		}
	}
}

// validateBareMetalInstanceTypeImmutability checks that immutable fields have not been changed.
// Core hardware specifications (CPU cores, memory total_gb) are immutable after creation
// following the same pattern as PrivateInstanceTypesServer for consistency.
func validateBareMetalInstanceTypeImmutability(merged, existing *privatev1.BareMetalInstanceType) error {
	// Validate immutable metadata fields:
	if merged.GetMetadata().GetName() != existing.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'name' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetMetadata().GetName(), merged.GetMetadata().GetName())
	}

	// Hardware is immutable after creation:
	if !proto.Equal(existing.GetSpec().GetHardware(), merged.GetSpec().GetHardware()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.hardware' is immutable and cannot be changed after creation")
	}

	return nil
}
