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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateVirtualNetworksServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
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
	implementationStrategy, err := s.validateVirtualNetwork(ctx, request.GetObject(), nil)
	if err != nil {
		return
	}

	// Set the implementation_strategy from the NetworkClass
	// This is a system-managed field derived from the selected network_class
	if request.GetObject().GetSpec() != nil && implementationStrategy != "" {
		request.GetObject().GetSpec().SetImplementationStrategy(implementationStrategy)
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
	_, err = s.validateVirtualNetwork(ctx, request.GetObject(), existingVN)
	if err != nil {
		return
	}

	// Preserve immutable implementation_strategy from existing object.
	if request.GetObject().GetSpec() != nil && existingVN.GetSpec() != nil {
		request.GetObject().GetSpec().SetImplementationStrategy(existingVN.GetSpec().GetImplementationStrategy())
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
// Returns the implementation_strategy from the NetworkClass (only populated on Create).
func (s *PrivateVirtualNetworksServer) validateVirtualNetwork(ctx context.Context,
	newVN *privatev1.VirtualNetwork, existingVN *privatev1.VirtualNetwork) (implementationStrategy string, err error) {

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
	if existingVN == nil || spec.GetNetworkClass() != existingVN.GetSpec().GetNetworkClass() {
		implementationStrategy, err = s.validateNetworkClassReference(ctx, spec)
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
	if newSpec.GetNetworkClass() != existingSpec.GetNetworkClass() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.network_class' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetNetworkClass(), newSpec.GetNetworkClass())
	}

	// Check immutable implementation_strategy field.
	if newStrategy := newSpec.GetImplementationStrategy(); newStrategy != "" && newStrategy != existingSpec.GetImplementationStrategy() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.implementation_strategy' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetImplementationStrategy(), newStrategy)
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
// Returns the implementation_strategy from the NetworkClass for storage in VirtualNetwork spec.
func (s *PrivateVirtualNetworksServer) validateNetworkClassReference(ctx context.Context,
	spec *privatev1.VirtualNetworkSpec) (implementationStrategy string, err error) {

	networkClassRef := spec.GetNetworkClass()
	var networkClass *privatev1.NetworkClass
	if networkClassRef == "" {
		var defaultNC *privatev1.NetworkClass
		defaultNC, err = findDefaultNetworkClass(ctx, s.logger, s.networkClassDao)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to query default NetworkClass",
				slog.Any("error", err),
			)
			return "", grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_class")
		}
		if defaultNC == nil {
			return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'spec.network_class' is required (no default NetworkClass is configured)")
		}
		networkClassRef = defaultNC.GetId()
		spec.SetNetworkClass(networkClassRef)
		networkClass = defaultNC
	} else {
		// Look up NetworkClass by ID or implementation_strategy using a single List call.
		// We avoid Get() here because a NotFound error from Get poisons the shared
		// database transaction (via ReportError), causing subsequent writes to roll back.
		listResponse, listErr := s.networkClassDao.List().
			SetFilter(fmt.Sprintf(
				"this.id == %[1]q || this.implementation_strategy == %[1]q",
				networkClassRef,
			)).
			SetLimit(1).
			Do(ctx)
		if listErr != nil {
			s.logger.ErrorContext(ctx, "Failed to query NetworkClass",
				slog.String("network_class", networkClassRef),
				slog.Any("error", listErr))
			err = grpcstatus.Errorf(grpccodes.Internal, "failed to validate network_class")
			return
		}
		if len(listResponse.GetItems()) == 0 {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not exist", networkClassRef)
			return
		}
		networkClass = listResponse.GetItems()[0]
	}

	if networkClass.GetMetadata().HasDeletionTimestamp() {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"network_class '%s' does not exist", networkClassRef)
		return
	}

	// VN-VAL-05: Check NetworkClass is READY
	if networkClass.GetStatus().GetState() != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY {
		err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"network_class '%s' is not in READY state (current state: %s)",
			networkClassRef, networkClass.GetStatus().GetState().String())
		return
	}

	// VN-VAL-06: Validate capabilities match
	vnCaps := spec.GetCapabilities()
	ncCaps := networkClass.GetCapabilities()
	if vnCaps != nil && ncCaps != nil {
		if vnCaps.GetEnableIpv4() && !ncCaps.GetSupportsIpv4() {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support IPv4", networkClassRef)
			return
		}
		if vnCaps.GetEnableIpv6() && !ncCaps.GetSupportsIpv6() {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support IPv6", networkClassRef)
			return
		}
		if vnCaps.GetEnableDualStack() && !ncCaps.GetSupportsDualStack() {
			err = grpcstatus.Errorf(grpccodes.InvalidArgument,
				"network_class '%s' does not support dual-stack", networkClassRef)
			return
		}
	}

	// Return the implementation_strategy for storage in VirtualNetwork spec
	implementationStrategy = networkClass.GetImplementationStrategy()
	return
}
