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
	"net/netip"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateSubnetsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.SubnetsServer = (*PrivateSubnetsServer)(nil)

type PrivateSubnetsServer struct {
	privatev1.UnimplementedSubnetsServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.Subnet]
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func NewPrivateSubnetsServer() *PrivateSubnetsServerBuilder {
	return &PrivateSubnetsServerBuilder{}
}

func (b *PrivateSubnetsServerBuilder) SetLogger(value *slog.Logger) *PrivateSubnetsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSubnetsServerBuilder) SetNotifier(value events.Notifier) *PrivateSubnetsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateSubnetsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateSubnetsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateSubnetsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSubnetsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateSubnetsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateSubnetsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateSubnetsServerBuilder) Build() (result *PrivateSubnetsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the VirtualNetwork DAO for parent validation:
	virtualNetworkDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetService(privatev1.Subnets_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateSubnetsServer{
		logger:            b.logger,
		generic:           generic,
		virtualNetworkDao: virtualNetworkDao,
	}
	return
}

func (s *PrivateSubnetsServer) List(ctx context.Context,
	request *privatev1.SubnetsListRequest) (response *privatev1.SubnetsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateSubnetsServer) Get(ctx context.Context,
	request *privatev1.SubnetsGetRequest) (response *privatev1.SubnetsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// SUB-SVC-01: Create creates a new Subnet with validation
func (s *PrivateSubnetsServer) Create(ctx context.Context,
	request *privatev1.SubnetsCreateRequest) (response *privatev1.SubnetsCreateResponse, err error) {
	subnet := request.GetObject()

	// Validate before creating:
	err = s.validateSubnet(ctx, subnet, nil)
	if err != nil {
		return
	}

	// SUB-VAL-10: Set owner reference annotation automatically
	if subnet.GetMetadata() == nil {
		subnet.Metadata = &privatev1.Metadata{}
	}
	if subnet.GetMetadata().GetAnnotations() == nil {
		subnet.Metadata.Annotations = make(map[string]string)
	}
	subnet.Metadata.Annotations["osac.openshift.io/owner-reference"] = subnet.GetSpec().GetVirtualNetwork()

	err = s.generic.Create(ctx, request, &response)
	return
}

// SUB-SVC-04: Update updates an existing Subnet with validation
func (s *PrivateSubnetsServer) Update(ctx context.Context,
	request *privatev1.SubnetsUpdateRequest) (response *privatev1.SubnetsUpdateResponse, err error) {
	// Get existing object for immutability validation:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.SubnetsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.SubnetsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingSubnet := getResponse.GetObject()

	// Validate with existing object context:
	err = s.validateSubnet(ctx, request.GetObject(), existingSubnet)
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateSubnetsServer) Delete(ctx context.Context,
	request *privatev1.SubnetsDeleteRequest) (response *privatev1.SubnetsDeleteResponse, err error) {
	getRequest := &privatev1.SubnetsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.SubnetsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	if err = validateNotDefault(getResponse.GetObject().GetMetadata().GetLabels(), "subnet"); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateSubnetsServer) Signal(ctx context.Context,
	request *privatev1.SubnetsSignalRequest) (response *privatev1.SubnetsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateSubnet validates the Subnet object.
func (s *PrivateSubnetsServer) validateSubnet(ctx context.Context,
	newSubnet *privatev1.Subnet, existingSubnet *privatev1.Subnet) error {

	if newSubnet == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "subnet is mandatory")
	}

	spec := newSubnet.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "subnet spec is mandatory")
	}

	// SUB-VAL-11, SUB-VAL-14, SUB-VAL-15: Check immutable fields (only on Update).
	// Run before SUB-VAL-03 so that explicit-empty-string attempts to clear an immutable CIDR
	// return "field is immutable" rather than "at least one CIDR required".
	if err := validateImmutableFieldsSubnet(newSubnet, existingSubnet); err != nil {
		return err
	}

	// SUB-VAL-03: At least one CIDR must be provided
	if spec.GetIpv4Cidr() == "" && spec.GetIpv6Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"at least one of 'spec.ipv4_cidr' or 'spec.ipv6_cidr' must be provided")
	}

	// SUB-VAL-01, SUB-VAL-02: Validate and canonicalize CIDRs
	if err := canonicalizeDualStackCIDRs(
		spec.GetIpv4Cidr, spec.SetIpv4Cidr,
		spec.GetIpv6Cidr, spec.SetIpv6Cidr,
	); err != nil {
		return err
	}

	// SUB-VAL-04, SUB-VAL-05, SUB-VAL-06, SUB-VAL-07, SUB-VAL-08: Validate parent VirtualNetwork
	// Only on Create (existingSubnet == nil) or if virtual_network differs (SUB-VAL-11 above prevents
	// VN changes on Update, so the second branch is effectively dead but kept for safety).
	if existingSubnet == nil || spec.GetVirtualNetwork() != existingSubnet.GetSpec().GetVirtualNetwork() {
		if err := s.validateVirtualNetworkReference(ctx, spec); err != nil {
			return err
		}
	}

	return nil
}

// validateCIDRSubset validates that subnetCIDR is a proper subset of parentCIDR.
func validateCIDRSubset(subnetCIDR string, parentCIDR string, ipVersion string) error {
	// Parse subnet CIDR
	subnetPrefix, err := netip.ParsePrefix(subnetCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid subnet %s CIDR format '%s': %v", ipVersion, subnetCIDR, err)
	}

	// Parse parent CIDR
	parentPrefix, err := netip.ParsePrefix(parentCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"invalid parent %s CIDR format '%s': %v", ipVersion, parentCIDR, err)
	}

	// Check parent contains subnet network address
	if !parentPrefix.Contains(subnetPrefix.Addr()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' is not within parent VirtualNetwork CIDR '%s'",
			ipVersion, subnetCIDR, parentCIDR)
	}

	// Check subnet mask is more specific than parent mask (prevents subnet larger than parent)
	subnetMaskSize := subnetPrefix.Bits()
	parentMaskSize := parentPrefix.Bits()
	if subnetMaskSize < parentMaskSize {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' (/%d) is less specific than parent CIDR '%s' (/%d)",
			ipVersion, subnetCIDR, subnetMaskSize, parentCIDR, parentMaskSize)
	}

	return nil
}

// validateVirtualNetworkReference validates that the referenced VirtualNetwork exists, is in READY state,
// and has matching IP families.
func (s *PrivateSubnetsServer) validateVirtualNetworkReference(ctx context.Context,
	spec *privatev1.SubnetSpec) error {

	virtualNetworkID := spec.GetVirtualNetwork()
	if virtualNetworkID == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.virtual_network' is required")
	}

	// SUB-VAL-04: Get parent VirtualNetwork by ID
	getResponse, err := s.virtualNetworkDao.Get().
		SetId(virtualNetworkID).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"parent VirtualNetwork '%s' does not exist", virtualNetworkID)
		}
		s.logger.ErrorContext(ctx, "Failed to query VirtualNetwork",
			slog.String("virtual_network_id", virtualNetworkID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate virtual_network")
	}

	virtualNetwork := getResponse.GetObject()

	// SUB-VAL-05: Check parent VirtualNetwork is READY
	if virtualNetwork.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"parent VirtualNetwork '%s' is not in READY state (current state: %s)",
			virtualNetworkID, virtualNetwork.GetStatus().GetState().String())
	}

	parentSpec := virtualNetwork.GetSpec()

	// SUB-VAL-07: Validate IPv4 CIDR only if parent has IPv4
	if spec.GetIpv4Cidr() != "" {
		if parentSpec.GetIpv4Cidr() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"subnet has IPv4 CIDR but parent VirtualNetwork does not support IPv4")
		}
		// SUB-VAL-06: Validate IPv4 CIDR is subset of parent
		if err := validateCIDRSubset(spec.GetIpv4Cidr(), parentSpec.GetIpv4Cidr(), cidrIPv4); err != nil {
			return err
		}
	}

	// SUB-VAL-08: Validate IPv6 CIDR only if parent has IPv6
	if spec.GetIpv6Cidr() != "" {
		if parentSpec.GetIpv6Cidr() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"subnet has IPv6 CIDR but parent VirtualNetwork does not support IPv6")
		}
		// SUB-VAL-06: Validate IPv6 CIDR is subset of parent
		if err := validateCIDRSubset(spec.GetIpv6Cidr(), parentSpec.GetIpv6Cidr(), cidrIPv6); err != nil {
			return err
		}
	}

	// Validate no CIDR overlap with existing subnets in the same VirtualNetwork:
	if err := s.validateNoCIDROverlap(ctx, spec); err != nil {
		return err
	}

	return nil
}

// validateNoCIDROverlap checks that the new subnet's CIDRs don't overlap with any existing
// subnets in the same VirtualNetwork.
// Note: this check is not fully atomic; concurrent subnet creation could bypass overlap
// validation. A locking mechanism would be needed for complete reliability.
func (s *PrivateSubnetsServer) validateNoCIDROverlap(ctx context.Context,
	spec *privatev1.SubnetSpec) error {

	// Fetch all existing subnets for the same VirtualNetwork using pagination:
	filter := fmt.Sprintf("this.spec.virtual_network == %q", spec.GetVirtualNetwork())
	var allSubnets []*privatev1.Subnet
	var offset int32
	for {
		listRequest := &privatev1.SubnetsListRequest{}
		listRequest.SetFilter(filter)
		listRequest.SetOffset(offset)
		var listResponse *privatev1.SubnetsListResponse
		if err := s.generic.List(ctx, listRequest, &listResponse); err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to list sibling subnets",
				slog.String("virtual_network_id", spec.GetVirtualNetwork()),
				slog.Any("error", err),
			)
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate CIDR overlap")
		}
		allSubnets = append(allSubnets, listResponse.GetItems()...)
		if offset+listResponse.GetSize() >= listResponse.GetTotal() {
			break
		}
		offset += listResponse.GetSize()
	}

	for _, existing := range allSubnets {
		existingSpec := existing.GetSpec()

		// Check IPv4 overlap:
		if spec.HasIpv4Cidr() && existingSpec.HasIpv4Cidr() {
			overlap, err := cidrsOverlap(spec.GetIpv4Cidr(), existingSpec.GetIpv4Cidr())
			if err != nil {
				return grpcstatus.Errorf(grpccodes.Internal,
					"failed to parse CIDRs for overlap check: %v", err)
			}
			if overlap {
				return grpcstatus.Errorf(grpccodes.AlreadyExists,
					"subnet IPv4 CIDR '%s' overlaps with existing subnet '%s' (CIDR '%s') "+
						"in VirtualNetwork '%s'",
					spec.GetIpv4Cidr(), existing.GetMetadata().GetName(),
					existingSpec.GetIpv4Cidr(), spec.GetVirtualNetwork())
			}
		}

		// Check IPv6 overlap:
		if spec.HasIpv6Cidr() && existingSpec.HasIpv6Cidr() {
			overlap, err := cidrsOverlap(spec.GetIpv6Cidr(), existingSpec.GetIpv6Cidr())
			if err != nil {
				return grpcstatus.Errorf(grpccodes.Internal,
					"failed to parse CIDRs for overlap check: %v", err)
			}
			if overlap {
				return grpcstatus.Errorf(grpccodes.AlreadyExists,
					"subnet IPv6 CIDR '%s' overlaps with existing subnet '%s' (CIDR '%s') "+
						"in VirtualNetwork '%s'",
					spec.GetIpv6Cidr(), existing.GetMetadata().GetName(),
					existingSpec.GetIpv6Cidr(), spec.GetVirtualNetwork())
			}
		}
	}

	return nil
}

// cidrsOverlap returns true if two CIDRs overlap (one contains any part of the other).
func cidrsOverlap(cidrA, cidrB string) (bool, error) {
	prefixA, errA := netip.ParsePrefix(cidrA)
	prefixB, errB := netip.ParsePrefix(cidrB)
	if errA != nil || errB != nil {
		return false, fmt.Errorf(
			"failed to parse CIDRs: %q: %w, %q: %w",
			cidrA, errA, cidrB, errB,
		)
	}
	return prefixA.Contains(prefixB.Addr()) || prefixB.Contains(prefixA.Addr()), nil
}

// validateImmutableFieldsSubnet validates that immutable fields have not been changed.
func validateImmutableFieldsSubnet(newSubnet *privatev1.Subnet, existingSubnet *privatev1.Subnet) error {
	if existingSubnet == nil {
		return nil // Create operation, no immutability checks
	}

	newSpec := newSubnet.GetSpec()
	existingSpec := existingSubnet.GetSpec()

	// Check immutable virtual_network field
	if newSpec.GetVirtualNetwork() != existingSpec.GetVirtualNetwork() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetVirtualNetwork(), newSpec.GetVirtualNetwork())
	}

	// SUB-VAL-14, SUB-VAL-15: Preserve and check immutable CIDR fields.
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
