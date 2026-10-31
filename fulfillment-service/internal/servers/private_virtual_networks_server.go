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
	"net/netip"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateVirtualNetworksServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	filterDesc         protoreflect.MessageDescriptor
	networkingSettings *NetworkingSettings
}

var _ privatev1.VirtualNetworksServer = (*PrivateVirtualNetworksServer)(nil)

type PrivateVirtualNetworksServer struct {
	privatev1.UnimplementedVirtualNetworksServer

	logger   *slog.Logger
	generic  *GenericServer[*privatev1.VirtualNetwork]
	settings NetworkingSettings
	guards   *networkingGuards
}

func NewPrivateVirtualNetworksServer() *PrivateVirtualNetworksServerBuilder {
	return &PrivateVirtualNetworksServerBuilder{}
}

func (b *PrivateVirtualNetworksServerBuilder) SetLogger(value *slog.Logger) *PrivateVirtualNetworksServerBuilder {
	b.logger = value
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

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateVirtualNetworksServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateVirtualNetworksServerBuilder {
	b.networkingSettings = &value
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
	guards, err := newNetworkingGuards(b.logger)
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetService(privatev1.VirtualNetworks_ServiceDesc.ServiceName).
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
		logger:   b.logger,
		generic:  generic,
		settings: resolveNetworkingSettings(b.networkingSettings),
		guards:   guards,
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
	if err = rejectOutputStatusOnCreate(request.GetObject().HasStatus()); err != nil {
		return
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.VirtualNetwork) error {
			if err := lockTenantForCreate(ctx, candidate.GetMetadata().GetTenant()); err != nil {
				return err
			}
			if err := validateReservedCreate(ctx, candidate.GetMetadata(), ""); err != nil {
				return err
			}
			if err := s.validateVirtualNetwork(ctx, candidate); err != nil {
				return err
			}
			candidate.SetStatus(privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
			}.Build())
			return nil
		})
	return
}

// Update writes status only; the spec is immutable.
func (s *PrivateVirtualNetworksServer) Update(ctx context.Context,
	request *privatev1.VirtualNetworksUpdateRequest) (response *privatev1.VirtualNetworksUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.VirtualNetwork) error {
			if err := validateNetworkingStatusOnlyUpdate("VirtualNetwork", current, candidate); err != nil {
				return err
			}
			return rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata())
		})
	return
}

func (s *PrivateVirtualNetworksServer) Delete(ctx context.Context,
	request *privatev1.VirtualNetworksDeleteRequest) (response *privatev1.VirtualNetworksDeleteResponse, err error) {
	if request.GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "identifier is mandatory")
	}
	virtualNetwork, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "VirtualNetwork")
	if err != nil {
		return nil, err
	}
	if deleteAlreadyAccepted(virtualNetwork.GetMetadata()) {
		return &privatev1.VirtualNetworksDeleteResponse{}, nil
	}
	if err = validateNotDefault(ctx, virtualNetwork.GetMetadata().GetLabels(), "virtual network"); err != nil {
		return
	}
	id := virtualNetwork.GetId()
	if err = rejectGuardedDelete(ctx, s.guards, s.guards.subnets, "Subnet", "VirtualNetwork", virtualNetwork,
		func(subnet *privatev1.Subnet) bool {
			return referenceMatches(subnet.GetSpec().GetVirtualNetwork(), id, virtualNetwork.GetMetadata())
		}); err != nil {
		return
	}
	if err = rejectGuardedDelete(ctx, s.guards, s.guards.securityGroups, "SecurityGroup", "VirtualNetwork",
		virtualNetwork, func(group *privatev1.SecurityGroup) bool {
			return referenceMatches(group.GetSpec().GetVirtualNetwork(), id, virtualNetwork.GetMetadata())
		}); err != nil {
		return
	}
	if err = rejectGuardedDelete(ctx, s.guards, s.guards.natGateways, "NATGateway", "VirtualNetwork",
		virtualNetwork, func(gateway *privatev1.NATGateway) bool {
			return referenceMatches(gateway.GetSpec().GetVirtualNetwork(), id, virtualNetwork.GetMetadata())
		}); err != nil {
		return
	}
	if err = rejectGuardedDelete(ctx, s.guards, s.guards.fabricDomains, "FabricDomain", "VirtualNetwork",
		virtualNetwork, func(domain *privatev1.FabricDomain) bool {
			return domain.GetSpec().GetVirtualNetwork() == id
		}); err != nil {
		return
	}
	if err = markDeleting(ctx, s.generic.dao, virtualNetwork); err != nil {
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

// validateVirtualNetwork checks a VirtualNetwork create: the CIDR, the NetworkClass gate (under the shared NetworkClass
// lock, so a concurrent pool create can't overlap it), the prefix limit, and the excluded and pool ranges.
func (s *PrivateVirtualNetworksServer) validateVirtualNetwork(ctx context.Context,
	vn *privatev1.VirtualNetwork) error {
	cidr := vn.GetSpec().GetIpv4Cidr()
	if cidr == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv4_cidr' is required and must be a canonical IPv4 CIDR")
	}
	if _, err := parseAndValidateCanonicalCIDR(cidr, cidrIPv4); err != nil {
		return err
	}
	prefix := netip.MustParsePrefix(cidr)
	networkClass, err := requireReadyNetworkClass(ctx, s.guards.networkClasses, false)
	if err != nil {
		return err
	}
	if err := validatePrefixLimit("spec.ipv4_cidr", prefix, networkClass); err != nil {
		return err
	}
	if excluded, overlaps := s.settings.firstExcludedOverlap(prefix); overlaps {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv4_cidr' %s overlaps the excluded range %s", cidr, excluded)
	}
	poolID, err := s.guards.firstPoolOverlap(ctx, prefix, "")
	if err != nil {
		return err
	}
	if poolID != "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ipv4_cidr' %s overlaps ExternalIPPool '%s'", cidr, poolID)
	}
	return nil
}

// validatePrefixLimit checks a VirtualNetwork or Subnet prefix against the prefix limit of the NetworkClass address
// plan.
func validatePrefixLimit(field string, prefix netip.Prefix, networkClass *privatev1.NetworkClass) error {
	limit, err := addressPlanPrefixLimit(networkClass.GetSpec().GetAddressPlan())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "the NetworkClass address plan is invalid: %v",
			grpcstatus.Convert(err).Message())
	}
	if prefix.Bits() > limit {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s prefix /%d exceeds the address plan prefix limit /%d", field, prefix.Bits(), limit)
	}
	return nil
}
