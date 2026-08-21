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
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateVirtualNetworksServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.VirtualNetworksServer = (*PrivateVirtualNetworksServer)(nil)

type PrivateVirtualNetworksServer struct {
	privatev1.UnimplementedVirtualNetworksServer

	logger          *slog.Logger
	generic         *GenericServer[*privatev1.VirtualNetwork]
	networkClassDao *dao.GenericDAO[*privatev1.NetworkClass]
}

func NewPrivateVirtualNetworksServer() *PrivateVirtualNetworksServerBuilder {
	return &PrivateVirtualNetworksServerBuilder{}
}

func (b *PrivateVirtualNetworksServerBuilder) SetLogger(value *slog.Logger) *PrivateVirtualNetworksServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateVirtualNetworksServerBuilder) SetNotifier(value events.Notifier) *PrivateVirtualNetworksServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateVirtualNetworksServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateVirtualNetworksServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateVirtualNetworksServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateVirtualNetworksServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateVirtualNetworksServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateVirtualNetworksServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateVirtualNetworksServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateVirtualNetworksServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateVirtualNetworksServerBuilder) Build() (result *PrivateVirtualNetworksServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the NetworkClass DAO:
	networkClassDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetService(privatev1.VirtualNetworks_ServiceDesc.ServiceName).
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
	result = &PrivateVirtualNetworksServer{
		logger:          b.logger,
		generic:         generic,
		networkClassDao: networkClassDao,
	}
	return
}

func (s *PrivateVirtualNetworksServer) List(ctx context.Context,
	request *privatev1.VirtualNetworksListRequest) (response *privatev1.VirtualNetworksListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateVirtualNetworksServer) Get(ctx context.Context,
	request *privatev1.VirtualNetworksGetRequest) (response *privatev1.VirtualNetworksGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateVirtualNetworksServer) Create(ctx context.Context,
	request *privatev1.VirtualNetworksCreateRequest) (response *privatev1.VirtualNetworksCreateResponse, err error) {
	// Validate before creating:
	err = s.validateVirtualNetwork(ctx, request.GetObject(), nil)
	if err != nil {
		return
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateVirtualNetworksServer) Update(ctx context.Context,
	request *privatev1.VirtualNetworksUpdateRequest) (response *privatev1.VirtualNetworksUpdateResponse, err error) {
	// Get existing object for immutability validation:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.VirtualNetworksGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.VirtualNetworksGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingVN := getResponse.GetObject()

	// Validate with existing object context:
	err = s.validateVirtualNetwork(ctx, request.GetObject(), existingVN)
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateVirtualNetworksServer) Delete(ctx context.Context,
	request *privatev1.VirtualNetworksDeleteRequest) (response *privatev1.VirtualNetworksDeleteResponse, err error) {
	getRequest := &privatev1.VirtualNetworksGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.VirtualNetworksGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	if err = validateNotDefault(getResponse.GetObject().GetMetadata().GetLabels(), "virtual network"); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateVirtualNetworksServer) Signal(ctx context.Context,
	request *privatev1.VirtualNetworksSignalRequest) (response *privatev1.VirtualNetworksSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateVirtualNetwork validates the VirtualNetwork object.
func (s *PrivateVirtualNetworksServer) validateVirtualNetwork(ctx context.Context,
	newVN *privatev1.VirtualNetwork, existingVN *privatev1.VirtualNetwork) (err error) {

	if newVN == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "virtual network is mandatory")
		return
	}

	spec := newVN.GetSpec()
	if spec == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "virtual network spec is mandatory")
		return
	}

	// VN-VAL-08: Region is required
	if spec.GetRegion() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.region' is required")
		return
	}

	// VN-VAL-09, VN-VAL-10, VN-VAL-11, VN-VAL-12: Check immutable fields (only on Update).
	// Run before VN-VAL-03 so that explicit-empty-string attempts to clear an immutable CIDR
	// return "field is immutable" rather than "at least one CIDR required".
	if err = validateImmutableFields(newVN, existingVN); err != nil {
		return
	}

	// VN-VAL-03: At least one CIDR must be provided
	if spec.GetIpv4Cidr() == "" && spec.GetIpv6Cidr() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"at least one of 'spec.ipv4_cidr' or 'spec.ipv6_cidr' must be provided")
		return
	}

	// VN-VAL-01, VN-VAL-02: Validate and canonicalize CIDRs
	if err = canonicalizeDualStackCIDRs(
		spec.GetIpv4Cidr, spec.SetIpv4Cidr,
		spec.GetIpv6Cidr, spec.SetIpv6Cidr,
	); err != nil {
		return
	}

	// VN-VAL-04, VN-VAL-05, VN-VAL-06: Validate NetworkClass
	// Only on Create (existingVN == nil) or if network_class differs (VN-VAL-10 above prevents NC
	// changes on Update, so the second branch is effectively dead but kept for safety).
	if existingVN == nil || refKey(spec.GetNetworkClass()) != refKey(existingVN.GetSpec().GetNetworkClass()) {
		err = s.validateNetworkClassReference(ctx, spec)
		if err != nil {
			return
		}
	}

	return
}

// validateImmutableFields validates that immutable fields have not been changed.
func validateImmutableFields(newVN *privatev1.VirtualNetwork, existingVN *privatev1.VirtualNetwork) error {
	if existingVN == nil {
		return nil // Create operation, no immutability checks
	}

	newSpec := newVN.GetSpec()
	existingSpec := existingVN.GetSpec()

	// Check immutable region field
	if newSpec.GetRegion() != existingSpec.GetRegion() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.region' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetRegion(), newSpec.GetRegion())
	}

	// Check immutable network_class field
	if refKey(newSpec.GetNetworkClass()) != refKey(existingSpec.GetNetworkClass()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.network_class' is immutable and cannot be changed from '%s' to '%s'",
			refKey(existingSpec.GetNetworkClass()), refKey(newSpec.GetNetworkClass()))
	}

	// VN-VAL-11, VN-VAL-12: Preserve and check immutable CIDR fields.
	// If the request omits a CIDR (Has*Cidr() false), copy the existing value to prevent
	// erasure — the private API has no Merge() step to preserve absent optional fields.
	for _, field := range []immutableCIDRField{
		{
			fieldName:       "spec.ipv4_cidr",
			ipVersion:       cidrIPv4,
			existingHasCIDR: existingSpec.HasIpv4Cidr,
			existingCIDR:    existingSpec.GetIpv4Cidr,
			newHasCIDR:      newSpec.HasIpv4Cidr,
			newCIDR:         newSpec.GetIpv4Cidr,
			setNewCIDR:      newSpec.SetIpv4Cidr,
		},
		{
			fieldName:       "spec.ipv6_cidr",
			ipVersion:       cidrIPv6,
			existingHasCIDR: existingSpec.HasIpv6Cidr,
			existingCIDR:    existingSpec.GetIpv6Cidr,
			newHasCIDR:      newSpec.HasIpv6Cidr,
			newCIDR:         newSpec.GetIpv6Cidr,
			setNewCIDR:      newSpec.SetIpv6Cidr,
		},
	} {
		if err := field.preserveAndValidate(); err != nil {
			return err
		}
	}

	return nil
}

// validateNetworkClassReference validates that the referenced NetworkClass exists and is in READY state.
func (s *PrivateVirtualNetworksServer) validateNetworkClassReference(ctx context.Context,
	spec *privatev1.VirtualNetworkSpec) (err error) {

	networkClassRef := spec.GetNetworkClass()
	var networkClass *privatev1.NetworkClass
	var networkClassKey string
	if networkClassRef == nil {
		var defaultNC *privatev1.NetworkClass
		defaultNC, err = findDefaultNetworkClass(ctx, s.logger, s.networkClassDao)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to query default NetworkClass",
				slog.Any("error", err),
			)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_class")
		}
		if defaultNC == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.network_class' is required (no default NetworkClass is configured)")
		}
		resolvedRef := &privatev1.NetworkClassReference{}
		resolvedRef.SetId(defaultNC.GetId())
		spec.SetNetworkClass(resolvedRef)
		networkClassKey = defaultNC.GetId()
		networkClass = defaultNC
	} else {
		networkClassKey = refKey(networkClassRef)
		id := networkClassRef.GetId()
		name := networkClassRef.GetName()

		// Look up NetworkClass by ID and/or metadata.name using a single List call.
		// We avoid Get() here because a NotFound error from Get poisons the shared
		// database transaction (via ReportError), causing subsequent writes to roll back.
		//
		// Filter only on the field(s) the caller actually set, matching the convention in
		// internal/references/lookups.go. An id == metadata.name OR filter would be
		// order-dependent when an id happens to collide with a different NetworkClass's
		// name, silently resolving to the wrong object.
		//
		// metadata.name is DNS-label-derived (hyphenated, e.g. "cudn-net"), so a caller
		// still passing the pre-OSAC-1468 underscore-delimited implementation_strategy
		// value (e.g. "cudn_net") as network_class will no longer resolve. Tracked as a
		// follow-up: OSAC-4125.
		var filter string
		switch {
		case id != "" && name != "":
			filter = fmt.Sprintf("this.id == %q && this.metadata.name == %q", id, name)
		case id != "":
			filter = fmt.Sprintf("this.id == %q", id)
		default:
			filter = fmt.Sprintf("this.metadata.name == %q", name)
		}

		listResponse, listErr := s.networkClassDao.List().
			SetFilter(filter).
			SetLimit(1).
			Do(ctx)
		if listErr != nil {
			s.logger.ErrorContext(ctx, "Failed to query NetworkClass",
				slog.String("network_class", networkClassKey),
				slog.Any("error", listErr))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_class")
		}
		if len(listResponse.GetItems()) == 0 {
			if id != "" && name != "" {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"network_class id '%s' and name '%s' do not both resolve to the same NetworkClass", id, name)
			}
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not exist", networkClassKey)
		}
		networkClass = listResponse.GetItems()[0]
	}

	if networkClass.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"network_class '%s' does not exist", networkClassKey)
	}

	// VN-VAL-05: Check NetworkClass is READY
	if networkClass.GetStatus().GetState() != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"network_class '%s' is not in READY state (current state: %s)",
			networkClassKey, networkClass.GetStatus().GetState().String())
	}

	// VN-VAL-06: Validate the addressing mode implied by ipv4_cidr/ipv6_cidr against the
	// NetworkClass's capabilities.
	ncCaps := networkClass.GetCapabilities()
	if ncCaps != nil {
		hasIpv4 := spec.GetIpv4Cidr() != ""
		hasIpv6 := spec.GetIpv6Cidr() != ""
		if hasIpv4 && !ncCaps.GetSupportsIpv4() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support IPv4", networkClassKey)
		}
		if hasIpv6 && !ncCaps.GetSupportsIpv6() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support IPv6", networkClassKey)
		}
		if hasIpv4 && hasIpv6 && !ncCaps.GetSupportsDualStack() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support dual-stack", networkClassKey)
		}
	}

	return nil
}
