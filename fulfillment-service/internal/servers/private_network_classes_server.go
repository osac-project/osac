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
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateNetworkClassesServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	filterDesc         protoreflect.MessageDescriptor
	networkingSettings *NetworkingSettings
}

var _ privatev1.NetworkClassesServer = (*PrivateNetworkClassesServer)(nil)

type PrivateNetworkClassesServer struct {
	privatev1.UnimplementedNetworkClassesServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.NetworkClass]
	settings          NetworkingSettings
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork]
	externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
}

func NewPrivateNetworkClassesServer() *PrivateNetworkClassesServerBuilder {
	return &PrivateNetworkClassesServerBuilder{}
}

func (b *PrivateNetworkClassesServerBuilder) SetLogger(value *slog.Logger) *PrivateNetworkClassesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateNetworkClassesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateNetworkClassesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateNetworkClassesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateNetworkClassesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateNetworkClassesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateNetworkClassesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateNetworkClassesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateNetworkClassesServerBuilder {
	b.filterDesc = value
	return b
}

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateNetworkClassesServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateNetworkClassesServerBuilder {
	b.networkingSettings = &value
	return b
}

func (b *PrivateNetworkClassesServerBuilder) Build() (result *PrivateNetworkClassesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	virtualNetworkDao, err := newGuardDAO[*privatev1.VirtualNetwork](b.logger)
	if err != nil {
		return
	}
	externalIPPoolDao, err := newGuardDAO[*privatev1.ExternalIPPool](b.logger)
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetService(privatev1.NetworkClasses_ServiceDesc.ServiceName).
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
	result = &PrivateNetworkClassesServer{
		logger:            b.logger,
		generic:           generic,
		settings:          resolveNetworkingSettings(b.networkingSettings),
		virtualNetworkDao: virtualNetworkDao,
		externalIPPoolDao: externalIPPoolDao,
	}
	return
}

func (s *PrivateNetworkClassesServer) List(ctx context.Context,
	request *privatev1.NetworkClassesListRequest) (response *privatev1.NetworkClassesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateNetworkClassesServer) Get(ctx context.Context,
	request *privatev1.NetworkClassesGetRequest) (response *privatev1.NetworkClassesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateNetworkClassesServer) Create(ctx context.Context,
	request *privatev1.NetworkClassesCreateRequest) (response *privatev1.NetworkClassesCreateResponse, err error) {
	nc := request.GetObject()
	if nc == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "network class is mandatory")
		return
	}
	if err = rejectOutputStatusOnCreate(nc.HasStatus()); err != nil {
		return
	}
	if err = s.validateNetworkClass(nc); err != nil {
		return
	}

	// The gate: no other NetworkClass record exists, also one being deleted. The network_classes_singleton unique
	// index (all rows) is the race-free enforcement; GenericServer maps its violation to FAILED_PRECONDITION.
	if err = s.checkSingleNetworkClass(ctx); err != nil {
		return
	}

	// Clear any caller-provided ID so the DAO always generates a UUID.
	nc.SetId("")
	nc.SetStatus(privatev1.NetworkClassStatus_builder{
		State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
	}.Build())
	if nc.GetMetadata().GetName() == "" {
		if nc.GetMetadata() == nil {
			nc.SetMetadata(&privatev1.Metadata{})
		}
		nc.GetMetadata().SetName(toDNSLabel(nc.GetFabricManager()))
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

// checkSingleNetworkClass enforces "one NetworkClass per deployment": no other NetworkClass record exists, including
// one being deleted.
func (s *PrivateNetworkClassesServer) checkSingleNetworkClass(ctx context.Context) error {
	listResponse, err := s.generic.dao.List().SetLimit(1).Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to check for existing NetworkClass", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to check for existing NetworkClass")
	}
	if items := listResponse.GetItems(); len(items) > 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"only one NetworkClass per deployment is allowed (existing NetworkClass id '%s')",
			items[0].GetId())
	}
	return nil
}

// Update writes status only: the NetworkClass spec, address plan included, is immutable after creation.
func (s *PrivateNetworkClassesServer) Update(ctx context.Context,
	request *privatev1.NetworkClassesUpdateRequest) (response *privatev1.NetworkClassesUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, current, candidate *privatev1.NetworkClass) error {
			return validateNetworkingStatusOnlyUpdate("NetworkClass", current, candidate)
		})
	return
}

func (s *PrivateNetworkClassesServer) Delete(ctx context.Context,
	request *privatev1.NetworkClassesDeleteRequest) (response *privatev1.NetworkClassesDeleteResponse, err error) {
	networkClass, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "NetworkClass")
	if err != nil {
		return
	}
	// A NetworkClass has no DELETING state: the deletion timestamp marks the accepted delete.
	if deleteAlreadyAccepted(networkClass.GetMetadata()) {
		response = &privatev1.NetworkClassesDeleteResponse{}
		return
	}
	if err = rejectDeleteIfAnyExists(ctx, s.virtualNetworkDao, "VirtualNetwork", "NetworkClass",
		networkClass.GetId()); err != nil {
		return
	}
	if err = rejectDeleteIfAnyExists(ctx, s.externalIPPoolDao, "ExternalIPPool", "NetworkClass",
		networkClass.GetId()); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateNetworkClassesServer) Signal(ctx context.Context,
	request *privatev1.NetworkClassesSignalRequest) (response *privatev1.NetworkClassesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateNetworkClass validates a NetworkClass create.
func (s *PrivateNetworkClassesServer) validateNetworkClass(nc *privatev1.NetworkClass) error {
	if nc.GetTitle() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'title' is required")
	}
	if nc.GetFabricManager() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'fabric_manager' is required")
	}
	if nc.HasK8SManager() && nc.GetK8SManager() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'k8s_manager' must not be empty when set")
	}
	defaults := nc.GetSpec().GetDefaults()
	if defaults == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.defaults' is required")
	}
	if err := validateNetworkDefaults(defaults); err != nil {
		return err
	}
	return s.validateAddressPlanAndDefaults(nc)
}

// validateAddressPlanAndDefaults checks the address plan and the defaults that depend on it or on the installation
// settings: the default Subnet meets the plan's prefix limit, and the default VirtualNetwork CIDR doesn't overlap an
// excluded range.
func (s *PrivateNetworkClassesServer) validateAddressPlanAndDefaults(nc *privatev1.NetworkClass) error {
	limit, err := addressPlanPrefixLimit(nc.GetSpec().GetAddressPlan())
	if err != nil {
		return err
	}
	defaults := nc.GetSpec().GetDefaults()
	subnetPrefix, err := netip.ParsePrefix(defaults.GetSubnetIpv4Cidr())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "defaults.subnet_ipv4_cidr: %v", err)
	}
	if subnetPrefix.Bits() > limit {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"defaults.subnet_ipv4_cidr '%s' exceeds the address plan prefix limit /%d",
			defaults.GetSubnetIpv4Cidr(), limit)
	}
	vnPrefix, err := netip.ParsePrefix(defaults.GetVirtualNetworkIpv4Cidr())
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "defaults.virtual_network_ipv4_cidr: %v", err)
	}
	if excluded, overlaps := s.settings.firstExcludedOverlap(vnPrefix); overlaps {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"defaults.virtual_network_ipv4_cidr '%s' overlaps the excluded range %s",
			defaults.GetVirtualNetworkIpv4Cidr(), excluded)
	}
	return nil
}

// validateNetworkDefaults validates the NetworkDefaults configuration.
func validateNetworkDefaults(defaults *privatev1.NetworkDefaults) error {
	if defaults.GetVirtualNetworkIpv4Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.defaults.virtual_network_ipv4_cidr' is required")
	}
	if defaults.GetSubnetIpv4Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.defaults.subnet_ipv4_cidr' is required")
	}
	if err := validateDefaultCIDRPair(
		defaults.GetVirtualNetworkIpv4Cidr(), defaults.GetSubnetIpv4Cidr(),
		"virtual_network_ipv4_cidr", "subnet_ipv4_cidr",
	); err != nil {
		return err
	}
	for i, rule := range defaults.GetIngressRules() {
		if err := validateSecurityRule(rule, "defaults.ingress", i); err != nil {
			return err
		}
	}
	for i, rule := range defaults.GetEgressRules() {
		if err := validateSecurityRule(rule, "defaults.egress", i); err != nil {
			return err
		}
	}
	return nil
}

// validateDefaultCIDRPair validates the canonical default VirtualNetwork and Subnet CIDRs and their containment.
func validateDefaultCIDRPair(vnCIDR, subnetCIDR, vnField, subnetField string) error {
	if _, err := parseAndValidateCanonicalCIDR(vnCIDR, cidrIPv4); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "defaults.%s: %v", vnField, err)
	}
	if _, err := parseAndValidateCanonicalCIDR(subnetCIDR, cidrIPv4); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "defaults.%s: %v", subnetField, err)
	}
	vnPrefix := netip.MustParsePrefix(vnCIDR)
	subnetPrefix := netip.MustParsePrefix(subnetCIDR)
	if !vnPrefix.Contains(subnetPrefix.Addr()) || subnetPrefix.Bits() < vnPrefix.Bits() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"defaults.%s '%s' is not within defaults.%s '%s'", subnetField, subnetCIDR, vnField, vnCIDR)
	}
	return nil
}
