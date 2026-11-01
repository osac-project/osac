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
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateSubnetsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.SubnetsServer = (*PrivateSubnetsServer)(nil)

type PrivateSubnetsServer struct {
	privatev1.UnimplementedSubnetsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.Subnet]
	guards  *networkingGuards

	// virtualNetworks resolves the parent reference with the caller's visibility.
	virtualNetworks *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func NewPrivateSubnetsServer() *PrivateSubnetsServerBuilder {
	return &PrivateSubnetsServerBuilder{}
}

func (b *PrivateSubnetsServerBuilder) SetLogger(value *slog.Logger) *PrivateSubnetsServerBuilder {
	b.logger = value
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

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateSubnetsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateSubnetsServerBuilder {
	b.filterDesc = value
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
	guards, err := newNetworkingGuards(b.logger)
	if err != nil {
		return
	}
	virtualNetworks, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetService(privatev1.Subnets_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateSubnetsServer{
		logger:          b.logger,
		generic:         generic,
		guards:          guards,
		virtualNetworks: virtualNetworks,
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

// Create creates a Subnet after checking its CIDR, its parent VirtualNetwork and its siblings under the parent lock.
func (s *PrivateSubnetsServer) Create(ctx context.Context,
	request *privatev1.SubnetsCreateRequest) (response *privatev1.SubnetsCreateResponse, err error) {
	if err = rejectOutputStatusOnCreate(request.GetObject().HasStatus()); err != nil {
		return
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, subnet *privatev1.Subnet) error {
			if err := lockTenantForCreate(ctx, subnet.GetMetadata().GetTenant()); err != nil {
				return err
			}
			if err := validateReservedCreate(ctx, subnet.GetMetadata(), ""); err != nil {
				return err
			}
			if err := s.validateSubnet(ctx, subnet); err != nil {
				return err
			}
			if subnet.GetMetadata().GetAnnotations() == nil {
				subnet.GetMetadata().SetAnnotations(map[string]string{})
			}
			subnet.GetMetadata().GetAnnotations()[ownerReferenceAnnotation] = subnet.GetSpec().GetVirtualNetwork().GetId()
			subnet.SetStatus(privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_PENDING,
			}.Build())
			return nil
		})
	return
}

// Update writes status only; the spec is immutable.
func (s *PrivateSubnetsServer) Update(ctx context.Context,
	request *privatev1.SubnetsUpdateRequest) (response *privatev1.SubnetsUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.Subnet) error {
			if err := validateNetworkingStatusOnlyUpdate("Subnet", current, candidate); err != nil {
				return err
			}
			return rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata())
		})
	return
}

func (s *PrivateSubnetsServer) Delete(ctx context.Context,
	request *privatev1.SubnetsDeleteRequest) (response *privatev1.SubnetsDeleteResponse, err error) {
	subnet, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "Subnet")
	if err != nil {
		return
	}
	if deleteAlreadyAccepted(subnet.GetMetadata()) {
		return &privatev1.SubnetsDeleteResponse{}, nil
	}
	if err = validateNotDefault(ctx, subnet.GetMetadata().GetLabels(), "subnet"); err != nil {
		return
	}
	if err = s.guards.rejectDeleteIfWorkloadsReference(ctx, "Subnet", subnet,
		func(ref *privatev1.SubnetLocalReference) bool {
			return ref != nil && referenceMatches(ref, subnet.GetId(), subnet.GetMetadata())
		},
		func([]*privatev1.SecurityGroupLocalReference) bool { return false }); err != nil {
		return
	}
	if err = markDeleting(ctx, s.generic.dao, subnet); err != nil {
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

// validateSubnet checks a Subnet create. The parent VirtualNetwork is locked first, so the sibling overlap check and
// the default Subnet gate run under the parent lock.
func (s *PrivateSubnetsServer) validateSubnet(ctx context.Context, subnet *privatev1.Subnet) error {
	spec := subnet.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "subnet spec is mandatory")
	}
	if spec.GetIpv4Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv4_cidr' is required and must be a canonical IPv4 CIDR")
	}
	if _, err := parseAndValidateCanonicalCIDR(spec.GetIpv4Cidr(), cidrIPv4); err != nil {
		return err
	}
	prefix := netip.MustParsePrefix(spec.GetIpv4Cidr())

	virtualNetwork, err := s.lockParent(ctx, subnet)
	if err != nil {
		return err
	}
	if err := validateCIDRSubset(spec.GetIpv4Cidr(), virtualNetwork.GetSpec().GetIpv4Cidr(), cidrIPv4); err != nil {
		return err
	}
	networkClass, err := findSingletonNetworkClass(ctx, s.guards.networkClasses)
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to find the NetworkClass")
	}
	if networkClass == nil {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "no NetworkClass is configured")
	}
	if err := validatePrefixLimit("spec.ipv4_cidr", prefix, networkClass); err != nil {
		return err
	}

	var siblings []*privatev1.Subnet
	err = forEachMatchingRecord(ctx, s.guards.subnets,
		fmt.Sprintf("this.spec.virtual_network.id == %q", virtualNetwork.GetId()),
		func(sibling *privatev1.Subnet) { siblings = append(siblings, sibling) })
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to list sibling subnets", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate CIDR overlap")
	}
	for _, sibling := range siblings {
		siblingPrefix, parseErr := netip.ParsePrefix(sibling.GetSpec().GetIpv4Cidr())
		if parseErr == nil && siblingPrefix.Overlaps(prefix) {
			return grpcstatus.Errorf(grpccodes.AlreadyExists,
				"subnet IPv4 CIDR '%s' overlaps with existing subnet '%s' (CIDR '%s') in VirtualNetwork '%s'",
				spec.GetIpv4Cidr(), sibling.GetId(), sibling.GetSpec().GetIpv4Cidr(), virtualNetwork.GetId())
		}
	}

	// In a tenant's default VirtualNetwork, any other Subnet needs the default Subnet READY, so the default Subnet
	// owns the hub network of cudn_evpn.
	if virtualNetwork.GetMetadata().GetLabels()[defaultLabel] == "true" && !isDefaultSubnet(subnet) {
		ready := false
		for _, sibling := range siblings {
			if isDefaultSubnet(sibling) && !sibling.GetMetadata().HasDeletionTimestamp() &&
				sibling.GetStatus().GetState() == privatev1.SubnetState_SUBNET_STATE_READY {
				ready = true
			}
		}
		if !ready {
			return grpcstatus.Error(grpccodes.FailedPrecondition,
				"the default Subnet of this VirtualNetwork is not READY yet")
		}
	}
	return nil
}

// defaultSubnetName is the name of the default Subnet of a tenant's default VirtualNetwork.
const defaultSubnetName = "default-ipv4"

func isDefaultSubnet(subnet *privatev1.Subnet) bool {
	return subnet.GetMetadata().GetName() == defaultSubnetName &&
		subnet.GetMetadata().GetLabels()[defaultLabel] == "true"
}

// lockParent locks the parent VirtualNetwork of a Subnet create and checks that it is READY, of the same tenant and
// not being deleted. It canonicalizes the reference to the VirtualNetwork ID.
func (s *PrivateSubnetsServer) lockParent(ctx context.Context, subnet *privatev1.Subnet) (
	*privatev1.VirtualNetwork, error) {
	ref := subnet.GetSpec().GetVirtualNetwork()
	key := refKey(ref)
	if ref == nil || key == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.virtual_network' is required")
	}
	virtualNetwork, err := lockParentVirtualNetwork(ctx, s.guards, s.virtualNetworks, key, subnet.GetMetadata())
	if err != nil {
		return nil, err
	}
	subnet.GetSpec().SetVirtualNetwork(privatev1.VirtualNetworkLocalReference_builder{
		Id:   virtualNetwork.GetId(),
		Name: virtualNetwork.GetMetadata().GetName(),
	}.Build())
	return virtualNetwork, nil
}

// lockParentVirtualNetwork finds the parent VirtualNetwork of a create by ID or by name in the tenant and project of the
// new record, with the caller-scoped DAO, then locks it by ID and checks that it is READY and not being deleted. A
// missing VirtualNetwork, or one of another tenant or project, fails with INVALID_ARGUMENT.
func lockParentVirtualNetwork(ctx context.Context, guards *networkingGuards,
	scoped *dao.GenericDAO[*privatev1.VirtualNetwork], key string, owner *privatev1.Metadata) (
	*privatev1.VirtualNetwork, error) {
	match, err := findInScope(ctx, scoped, owner.GetTenant(), owner.GetProject(), key)
	if err != nil {
		return nil, err
	}
	if match == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "parent VirtualNetwork '%s' does not exist", key)
	}
	locked, err := guards.virtualNetworks.Get().SetId(match.GetId()).SetLock(true).Do(ctx)
	if err != nil {
		var notFound *dao.ErrNotFound
		if errors.As(err, &notFound) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "parent VirtualNetwork '%s' does not exist", key)
		}
		return nil, translateLifecycleError(err)
	}
	virtualNetwork := locked.GetObject()
	if virtualNetwork.GetMetadata().HasDeletionTimestamp() {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"parent VirtualNetwork '%s' is being deleted", virtualNetwork.GetId())
	}
	if virtualNetwork.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"parent VirtualNetwork '%s' is not in READY state (current state: %s)",
			virtualNetwork.GetId(), virtualNetwork.GetStatus().GetState())
	}
	return virtualNetwork, nil
}

// validateCIDRSubset validates that subnetCIDR is a proper subset of parentCIDR.
func validateCIDRSubset(subnetCIDR string, parentCIDR string, ipVersion string) error {
	subnetPrefix, err := netip.ParsePrefix(subnetCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"invalid subnet %s CIDR format '%s': %v", ipVersion, subnetCIDR, err)
	}
	parentPrefix, err := netip.ParsePrefix(parentCIDR)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"invalid parent %s CIDR format '%s': %v", ipVersion, parentCIDR, err)
	}
	if !parentPrefix.Contains(subnetPrefix.Addr()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' is not within parent VirtualNetwork CIDR '%s'",
			ipVersion, subnetCIDR, parentCIDR)
	}
	if subnetPrefix.Bits() < parentPrefix.Bits() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"subnet %s CIDR '%s' (/%d) is less specific than parent CIDR '%s' (/%d)",
			ipVersion, subnetCIDR, subnetPrefix.Bits(), parentCIDR, parentPrefix.Bits())
	}
	return nil
}
