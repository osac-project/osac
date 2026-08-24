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
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateInstanceTypesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.InstanceTypesServer = (*PrivateInstanceTypesServer)(nil)

type PrivateInstanceTypesServer struct {
	privatev1.UnimplementedInstanceTypesServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.InstanceType]
}

func NewPrivateInstanceTypesServer() *PrivateInstanceTypesServerBuilder {
	return &PrivateInstanceTypesServerBuilder{}
}

func (b *PrivateInstanceTypesServerBuilder) SetLogger(value *slog.Logger) *PrivateInstanceTypesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateInstanceTypesServerBuilder) SetNotifier(value events.Notifier) *PrivateInstanceTypesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateInstanceTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateInstanceTypesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateInstanceTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateInstanceTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateInstanceTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateInstanceTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateInstanceTypesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateInstanceTypesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateInstanceTypesServerBuilder) Build() (result *PrivateInstanceTypesServer, err error) {
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
	generic, err := NewGenericServer[*privatev1.InstanceType]().
		SetLogger(b.logger).
		SetService(privatev1.InstanceTypes_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateInstanceTypesServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateInstanceTypesServer) List(ctx context.Context,
	request *privatev1.InstanceTypesListRequest) (response *privatev1.InstanceTypesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateInstanceTypesServer) Get(ctx context.Context,
	request *privatev1.InstanceTypesGetRequest) (response *privatev1.InstanceTypesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateInstanceTypesServer) Create(ctx context.Context,
	request *privatev1.InstanceTypesCreateRequest) (response *privatev1.InstanceTypesCreateResponse, err error) {
	if request.GetObject() == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}

	spec := request.GetObject().GetSpec()
	if spec == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object spec is mandatory")
		return
	}

	// Set id from metadata.name (name-as-primary-key per Phase 1 D-01):
	request.GetObject().SetId(request.GetObject().GetMetadata().GetName())

	// Default state to ACTIVE if unspecified:
	if spec.GetState() == privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_UNSPECIFIED {
		spec.SetState(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)
	}
	if spec.GetState() != privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.state' must be ACTIVE on create")
		return
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateInstanceTypesServer) Update(ctx context.Context,
	request *privatev1.InstanceTypesUpdateRequest) (response *privatev1.InstanceTypesUpdateResponse, err error) {
	// Get the object identifier:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Fetch the existing object:
	getRequest := &privatev1.InstanceTypesGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.InstanceTypesGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existing := getResponse.GetObject()

	// Merge the update into a clone of the existing object:
	merged := cloneInstanceType(existing)
	applyInstanceTypeUpdate(merged, request.GetObject(), request.GetUpdateMask())

	// Validate immutable fields:
	err = validateInstanceTypeImmutability(merged, existing)
	if err != nil {
		return
	}

	// Handle state transitions with timestamp auto-population:
	stateChanged := handleInstanceTypeStateTransition(existing, merged)

	// If state changed, ensure the deprecation fields are included in the update mask
	// so the generic server persists the timestamps set by handleInstanceTypeStateTransition.
	if stateChanged && len(request.GetUpdateMask().GetPaths()) > 0 {
		mask := request.GetUpdateMask()
		mask.Paths = append(mask.Paths, "spec.deprecation")
	}

	// Set the merged spec back into the request for the generic update:
	request.GetObject().SetSpec(merged.GetSpec())

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateInstanceTypesServer) Delete(ctx context.Context,
	request *privatev1.InstanceTypesDeleteRequest) (response *privatev1.InstanceTypesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateInstanceTypesServer) Signal(ctx context.Context,
	request *privatev1.InstanceTypesSignalRequest) (response *privatev1.InstanceTypesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// cloneInstanceType creates a deep copy of an InstanceType.
func cloneInstanceType(it *privatev1.InstanceType) *privatev1.InstanceType {
	return proto.Clone(it).(*privatev1.InstanceType)
}

// applyInstanceTypeUpdate applies the update fields onto the base object, respecting the field mask.
// If no mask is provided, all fields from the update are applied.
// Field mask paths use the spec prefix (e.g., "spec.description", "spec.state") per D-00d.
func applyInstanceTypeUpdate(base, update *privatev1.InstanceType, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		proto.Merge(base, update)
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "spec.description":
			base.GetSpec().SetDescription(update.GetSpec().GetDescription())
		case "spec.state":
			base.GetSpec().SetState(update.GetSpec().GetState())
		case "spec.deprecation":
			base.GetSpec().SetDeprecation(update.GetSpec().GetDeprecation())
		case "spec.deprecation.replacement":
			dep := base.GetSpec().GetDeprecation()
			if dep == nil {
				dep = &privatev1.InstanceTypeDeprecation{}
				base.GetSpec().SetDeprecation(dep)
			}
			dep.SetReplacement(update.GetSpec().GetDeprecation().GetReplacement())
		case "spec.deprecation.deprecation_timestamp":
			dep := base.GetSpec().GetDeprecation()
			if dep == nil {
				dep = &privatev1.InstanceTypeDeprecation{}
				base.GetSpec().SetDeprecation(dep)
			}
			dep.SetDeprecationTimestamp(update.GetSpec().GetDeprecation().GetDeprecationTimestamp())
		case "spec.deprecation.obsolescence_timestamp":
			dep := base.GetSpec().GetDeprecation()
			if dep == nil {
				dep = &privatev1.InstanceTypeDeprecation{}
				base.GetSpec().SetDeprecation(dep)
			}
			dep.SetObsolescenceTimestamp(update.GetSpec().GetDeprecation().GetObsolescenceTimestamp())
		default:
			// For unknown paths, fall through - the generic handler will
			// reject invalid paths if needed.
		}
	}
}

// validateInstanceTypeImmutability checks that immutable fields have not been changed.
func validateInstanceTypeImmutability(merged, existing *privatev1.InstanceType) error {
	if merged.GetSpec().GetCores() != existing.GetSpec().GetCores() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cores' is immutable and cannot be changed from '%d' to '%d'",
			existing.GetSpec().GetCores(), merged.GetSpec().GetCores())
	}
	if merged.GetSpec().GetMemoryGib() != existing.GetSpec().GetMemoryGib() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.memory_gib' is immutable and cannot be changed from '%d' to '%d'",
			existing.GetSpec().GetMemoryGib(), merged.GetSpec().GetMemoryGib())
	}
	if !proto.Equal(merged.GetSpec().GetGpu(), existing.GetSpec().GetGpu()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.gpu' is immutable and cannot be changed after creation")
	}
	if merged.GetMetadata().GetName() != existing.GetMetadata().GetName() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'name' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetMetadata().GetName(), merged.GetMetadata().GetName())
	}
	return nil
}

// handleInstanceTypeStateTransition handles state transitions with timestamp auto-population.
// Rules per D-01 through D-05:
//   - D-01: All 6 bidirectional state transitions are valid.
//   - D-02: Same-state update is a silent no-op (no timestamp changes).
//   - D-03: Only set the timestamp for the target state.
//   - D-04: Backward transitions retain existing timestamps as historical record.
//   - D-05: Re-entering a state updates the corresponding timestamp to now.
func handleInstanceTypeStateTransition(existing, merged *privatev1.InstanceType) bool {
	oldState := existing.GetSpec().GetState()
	newState := merged.GetSpec().GetState()

	// D-02: Same-state update is a no-op for timestamps.
	if oldState == newState {
		return false
	}

	// Ensure deprecation message exists:
	dep := merged.GetSpec().GetDeprecation()
	if dep == nil {
		dep = &privatev1.InstanceTypeDeprecation{}
		merged.GetSpec().SetDeprecation(dep)
	}

	// D-03, D-05: Set timestamp only for the target state.
	switch newState {
	case privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED:
		dep.SetDeprecationTimestamp(timestamppb.Now())
	case privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE:
		dep.SetObsolescenceTimestamp(timestamppb.Now())
	case privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE:
		// D-04: Backward transition to ACTIVE retains existing timestamps.
	}

	// Sync deprecation state with the spec-level state (Pitfall 1 from RESEARCH.md):
	dep.SetState(newState)

	return true
}
