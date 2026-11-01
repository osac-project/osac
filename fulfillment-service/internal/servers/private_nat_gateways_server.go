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
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateNATGatewaysServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.NATGatewaysServer = (*PrivateNATGatewaysServer)(nil)

type PrivateNATGatewaysServer struct {
	privatev1.UnimplementedNATGatewaysServer

	logger             *slog.Logger
	tenancyLogic       auth.TenancyLogic
	generic            *GenericServer[*privatev1.NATGateway]
	externalIPDao      *dao.GenericDAO[*privatev1.ExternalIP]
	virtualNetworksDao *dao.GenericDAO[*privatev1.VirtualNetwork]
	networkClassesDao  *dao.GenericDAO[*privatev1.NetworkClass]
	lifecycle          *externalIPLifecycle
	guards             *networkingGuards
}

func NewPrivateNATGatewaysServer() *PrivateNATGatewaysServerBuilder {
	return &PrivateNATGatewaysServerBuilder{}
}

func (b *PrivateNATGatewaysServerBuilder) SetLogger(value *slog.Logger) *PrivateNATGatewaysServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateNATGatewaysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateNATGatewaysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateNATGatewaysServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateNATGatewaysServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateNATGatewaysServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateNATGatewaysServerBuilder) Build() (result *PrivateNATGatewaysServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	externalIPDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	externalIPAttachmentDao, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.NATGateway]().
		SetLogger(b.logger).
		SetService(privatev1.NATGateways_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	guards, err := newNetworkingGuards(b.logger)
	if err != nil {
		return
	}
	result = &PrivateNATGatewaysServer{
		guards:             guards,
		logger:             b.logger,
		tenancyLogic:       b.tenancyLogic,
		generic:            generic,
		externalIPDao:      externalIPDao,
		virtualNetworksDao: virtualNetworksDao,
		networkClassesDao:  networkClassesDao,
	}
	result.lifecycle = newExternalIPLifecycle(
		externalIPDao,
		externalIPAttachmentDao,
		generic.dao,
		nil,
		nil,
		nil,
		nil,
		virtualNetworksDao,
	)
	return
}

func (s *PrivateNATGatewaysServer) List(ctx context.Context,
	request *privatev1.NATGatewaysListRequest) (response *privatev1.NATGatewaysListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Get(ctx context.Context,
	request *privatev1.NATGatewaysGetRequest) (response *privatev1.NATGatewaysGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) Create(ctx context.Context,
	request *privatev1.NATGatewaysCreateRequest) (response *privatev1.NATGatewaysCreateResponse, err error) {
	natGateway := request.GetObject()
	if err = rejectOutputStatusOnCreate(natGateway != nil && natGateway.HasStatus()); err != nil {
		return
	}
	err = s.validateNATGateway(natGateway)
	if err != nil {
		return
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.NATGateway) error {
			if err := lockTenantForCreate(ctx, candidate.GetMetadata().GetTenant()); err != nil {
				return err
			}
			return s.prepareCreate(ctx, candidate)
		})
	return
}

// isDefaultNATGateway reports whether a create is the system's create of a tenant's default NATGateway.
func isDefaultNATGateway(ctx context.Context, metadata *privatev1.Metadata) bool {
	return isSystemCreate(ctx, metadata) && metadata.GetName() == "default" &&
		metadata.GetLabels()[defaultLabel] == "true"
}

// prepareCreate checks the NATGateway gates. It locks the VirtualNetwork, then the ExternalIP.
func (s *PrivateNATGatewaysServer) prepareCreate(ctx context.Context, natGateway *privatev1.NATGateway) error {
	metadata := natGateway.GetMetadata()
	if err := validateReservedCreate(ctx, metadata, ""); err != nil {
		return err
	}
	tenant := metadata.GetTenant()
	virtualNetwork, err := lockParentVirtualNetwork(ctx, s.guards, s.virtualNetworksDao,
		refKey(natGateway.GetSpec().GetVirtualNetwork()), metadata)
	if err != nil {
		return err
	}
	canonicalizeResourceReference(natGateway.GetSpec().GetVirtualNetwork(), virtualNetwork)

	// Only the system creates the NATGateway of a tenant default VirtualNetwork. This check runs first, so the
	// code doesn't depend on whether onboarding already created it.
	defaultGateway := isDefaultNATGateway(ctx, metadata)
	if virtualNetwork.GetMetadata().GetLabels()[defaultLabel] == "true" && !defaultGateway {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"VirtualNetwork '%s' is a tenant default; only the system creates its NATGateway", virtualNetwork.GetId())
	}
	// The VirtualNetwork has no NATGateway, also not one being deleted. The check runs under the VirtualNetwork lock.
	count, firstID, err := countRecords(ctx, s.guards.natGateways,
		fmt.Sprintf("this.spec.virtual_network.id == %q", virtualNetwork.GetId()))
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to check the NATGateways of the VirtualNetwork")
	}
	if count > 0 {
		return grpcstatus.Errorf(grpccodes.AlreadyExists,
			"VirtualNetwork '%s' already has NATGateway '%s'", virtualNetwork.GetId(), firstID)
	}

	externalIPKey, err := s.resolveExternalIPID(ctx, natGateway)
	if err != nil {
		return err
	}
	externalIP, err := s.validateExternalIPReference(ctx, externalIPKey)
	if err != nil {
		return err
	}
	if err = validateTenantMatch(tenant, externalIP, "ExternalIP", externalIPKey); err != nil {
		return err
	}
	canonicalizeResourceReference(natGateway.GetSpec().GetExternalIp(), externalIP)
	if isAutomatic(externalIP.GetMetadata()) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is automatic and can't be used by a NATGateway", externalIP.GetId())
	}
	if externalIP.GetMetadata().GetLabels()[defaultLabel] == "true" && !defaultGateway {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is a tenant default; only the default NATGateway uses it", externalIP.GetId())
	}
	if err = s.lifecycle.ensureExternalIPAvailable(ctx, externalIP.GetId(),
		externalIP.GetMetadata().GetName()); err != nil {
		return err
	}

	canonicalHubID, err := canonicalNetworkingHubID(ctx, s.logger, s.networkClassesDao)
	if err != nil {
		return err
	}
	if err = validateNetworkingHubReferences(canonicalHubID, networkingHubReference{
		resourceType: "VirtualNetwork",
		id:           virtualNetwork.GetId(),
		hubID:        virtualNetwork.GetStatus().GetHub(),
	}, networkingHubReference{
		resourceType: "ExternalIP",
		id:           externalIP.GetId(),
		hubID:        externalIP.GetStatus().GetHub(),
	}); err != nil {
		return err
	}
	natGateway.SetStatus(privatev1.NATGatewayStatus_builder{
		State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
	}.Build())
	return nil
}

func (s *PrivateNATGatewaysServer) Update(ctx context.Context,
	request *privatev1.NATGatewaysUpdateRequest) (response *privatev1.NATGatewaysUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	mask := request.GetUpdateMask()
	if err = validatePrivateLifecycleUpdateMask(mask,
		[]string{"status.state", "status.message", "status.hub", "status.state_transition_time"},
		nil); err != nil {
		return
	}
	if _, _, err = s.lifecycle.lockNATGateway(ctx, id); err != nil {
		err = translateLifecycleError(err)
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.NATGateway) error {
			if err := validateNetworkingStatusOnlyUpdate("NATGateway", current, candidate); err != nil {
				return err
			}
			return rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata())
		})
	return
}

func (s *PrivateNATGatewaysServer) Delete(ctx context.Context,
	request *privatev1.NATGatewaysDeleteRequest) (response *privatev1.NATGatewaysDeleteResponse, err error) {
	id := request.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	_, natGateway, err := s.lifecycle.lockNATGateway(ctx, id)
	if err != nil {
		err = translateLifecycleError(err)
		return
	}
	if deleteAlreadyAccepted(natGateway.GetMetadata()) {
		response = &privatev1.NATGatewaysDeleteResponse{}
		return
	}
	if err = validateNotDefault(ctx, natGateway.GetMetadata().GetLabels(), "NAT gateway"); err != nil {
		return
	}
	if err = s.rejectDeleteIfClustersUseVirtualNetwork(ctx, natGateway); err != nil {
		return
	}
	if err = markDeleting(ctx, s.generic.dao, natGateway); err != nil {
		return
	}
	err = translateLifecycleError(s.lifecycle.deleteLockedNATGateway(ctx, natGateway))
	if err == nil {
		response = &privatev1.NATGatewaysDeleteResponse{}
	}
	return
}

// rejectDeleteIfClustersUseVirtualNetwork rejects a NATGateway delete while any Cluster, in any state, has its network
// attachment on a Subnet of the NATGateway's VirtualNetwork: the workers reach the management endpoints through it.
func (s *PrivateNATGatewaysServer) rejectDeleteIfClustersUseVirtualNetwork(ctx context.Context,
	natGateway *privatev1.NATGateway) error {
	virtualNetworkID := refKey(natGateway.GetSpec().GetVirtualNetwork())
	subnets := map[string]bool{}
	err := forEachRecord(ctx, s.guards.subnets, func(subnet *privatev1.Subnet) {
		if refKey(subnet.GetSpec().GetVirtualNetwork()) == virtualNetworkID {
			subnets[subnet.GetId()] = true
		}
	})
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to check NATGateway references")
	}
	count := 0
	firstID := ""
	err = forEachRecord(ctx, s.guards.clusters, func(cluster *privatev1.Cluster) {
		if subnets[refKey(cluster.GetSpec().GetNetworkAttachment().GetSubnet())] {
			count++
			if firstID == "" {
				firstID = cluster.GetId()
			}
		}
	})
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to check NATGateway references")
	}
	if count > 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete NATGateway '%s': %d Cluster(s) have a network attachment on VirtualNetwork '%s' "+
				"(including '%s')", natGateway.GetId(), count, virtualNetworkID, firstID)
	}
	return nil
}

func (s *PrivateNATGatewaysServer) Signal(ctx context.Context,
	request *privatev1.NATGatewaysSignalRequest) (response *privatev1.NATGatewaysSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateNATGatewaysServer) validateNATGateway(natGateway *privatev1.NATGateway) error {
	if natGateway == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "NAT gateway is mandatory")
	}
	spec := natGateway.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "NAT gateway spec is mandatory")
	}
	if spec.GetVirtualNetwork() == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is required")
	}
	if spec.GetExternalIp() == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.external_ip' is required")
	}
	return nil
}

// resolveExternalIPID returns the identifier of the ExternalIP that the NATGateway references. A reference with only
// an ID is used as is. A reference with a name is looked up with the filter that the reference interceptor builds for
// names: the name, in the tenant and project of the NATGateway, and also the ID when both are set. The interceptor
// skips this reference so that the default VirtualNetwork check runs first.
func (s *PrivateNATGatewaysServer) resolveExternalIPID(ctx context.Context,
	natGateway *privatev1.NATGateway) (string, error) {
	reference := natGateway.GetSpec().GetExternalIp()
	if reference.GetName() == "" {
		return reference.GetId(), nil
	}
	filter := fmt.Sprintf("this.metadata.name == %s", strconv.Quote(reference.GetName()))
	if reference.GetId() != "" {
		filter = fmt.Sprintf("this.id == %s && %s", strconv.Quote(reference.GetId()), filter)
	}
	metadata := natGateway.GetMetadata()
	filter += fmt.Sprintf(" && this.metadata.tenant == %s && this.metadata.project == %s",
		strconv.Quote(metadata.GetTenant()), strconv.Quote(metadata.GetProject()))
	response, err := s.externalIPDao.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to query ExternalIP",
			slog.String("external_ip_name", reference.GetName()),
			slog.Any("error", err))
		return "", grpcstatus.Errorf(grpccodes.Internal, "failed to validate external_ip")
	}
	if len(response.GetItems()) == 0 {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument,
			"ExternalIP '%s' does not exist", reference.GetName())
	}
	return response.GetItems()[0].GetId(), nil
}

func (s *PrivateNATGatewaysServer) validateExternalIPReference(
	ctx context.Context, externalIPID string) (*privatev1.ExternalIP, error) {
	getResponse, err := s.externalIPDao.Get().
		SetId(externalIPID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"ExternalIP '%s' does not exist", externalIPID)
		}
		s.logger.ErrorContext(ctx, "Failed to query ExternalIP",
			slog.String("external_ip_id", externalIPID),
			slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to validate external_ip")
	}

	externalIP := getResponse.GetObject()
	if externalIP.GetMetadata().HasDeletionTimestamp() {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is being deleted", externalIPID)
	}

	if externalIP.GetStatus().GetState() != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"ExternalIP '%s' is not in ALLOCATED state (current state: %s)",
			externalIPID, externalIP.GetStatus().GetState().String())
	}
	return externalIP, nil
}
