/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateFabricDomainsServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	networkingSettings *NetworkingSettings
	filterDesc         protoreflect.MessageDescriptor
}

var _ privatev1.FabricDomainsServer = (*PrivateFabricDomainsServer)(nil)

type PrivateFabricDomainsServer struct {
	privatev1.UnimplementedFabricDomainsServer
	generic  *GenericServer[*privatev1.FabricDomain]
	settings NetworkingSettings
	guards   *networkingGuards

	// virtualNetworks resolves the parent reference with the caller's visibility.
	virtualNetworks *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func NewPrivateFabricDomainsServer() *PrivateFabricDomainsServerBuilder {
	return &PrivateFabricDomainsServerBuilder{}
}

func (b *PrivateFabricDomainsServerBuilder) SetLogger(value *slog.Logger) *PrivateFabricDomainsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateFabricDomainsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateFabricDomainsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateFabricDomainsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of the private FabricDomain type is used.
func (b *PrivateFabricDomainsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateFabricDomainsServerBuilder {
	b.filterDesc = value
	return b
}

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateFabricDomainsServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateFabricDomainsServerBuilder {
	b.networkingSettings = &value
	return b
}

func (b *PrivateFabricDomainsServerBuilder) Build() (result *PrivateFabricDomainsServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	generic, err := NewGenericServer[*privatev1.FabricDomain]().
		SetLogger(b.logger).
		SetService(privatev1.FabricDomains_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return nil, err
	}
	guards, err := newNetworkingGuards(b.logger)
	if err != nil {
		return nil, err
	}
	virtualNetworks, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		Build()
	if err != nil {
		return nil, err
	}

	return &PrivateFabricDomainsServer{
		generic:         generic,
		settings:        resolveNetworkingSettings(b.networkingSettings),
		guards:          guards,
		virtualNetworks: virtualNetworks,
	}, nil
}

func (s *PrivateFabricDomainsServer) List(ctx context.Context, request *privatev1.FabricDomainsListRequest) (*privatev1.FabricDomainsListResponse, error) {
	var response *privatev1.FabricDomainsListResponse
	err := s.generic.List(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Get(ctx context.Context, request *privatev1.FabricDomainsGetRequest) (*privatev1.FabricDomainsGetResponse, error) {
	var response *privatev1.FabricDomainsGetResponse
	err := s.generic.Get(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) Create(ctx context.Context, request *privatev1.FabricDomainsCreateRequest) (*privatev1.FabricDomainsCreateResponse, error) {
	if err := rejectOutputStatusOnCreate(request.GetObject().HasStatus()); err != nil {
		return nil, err
	}
	var response *privatev1.FabricDomainsCreateResponse
	err := s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _ *privatev1.FabricDomain, object *privatev1.FabricDomain) error {
			if err := lockTenantForCreate(ctx, object.GetMetadata().GetTenant()); err != nil {
				return err
			}
			// Tenant assignment precedes relationship validation, including for administrators.
			if err := s.validateFabricDomain(ctx, object); err != nil {
				return err
			}
			object.SetStatus(&privatev1.FabricDomainStatus{Conditions: []*privatev1.FabricDomainCondition{{
				Type:               privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING,
				Status:             privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				LastTransitionTime: timestamppb.Now(),
			}}})
			return nil
		})
	return response, err
}

// fabricDomainControllerStatusFields are the status fields the controllers write.
var fabricDomainControllerStatusFields = map[string]bool{
	"status.backend_id": true,
	"status.members":    true,
	"status.hub":        true,
	"status.conditions": true,
}

// Update writes status only; the spec is immutable, and the servers can't change in this release.
func (s *PrivateFabricDomainsServer) Update(ctx context.Context, request *privatev1.FabricDomainsUpdateRequest) (*privatev1.FabricDomainsUpdateResponse, error) {
	if request.GetObject().GetId() == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}
	mask := request.GetUpdateMask()
	if mask != nil && len(mask.GetPaths()) > 0 && updateIncludesField(mask, "status") {
		if !auth.IsControllerServiceAccount(ctx) && !auth.IsServiceAccount(ctx, "osac-operator") {
			return nil, grpcstatus.Error(grpccodes.InvalidArgument, "status output fields can only be updated by an authorized controller")
		}
		for _, path := range mask.GetPaths() {
			if (path == "status" || strings.HasPrefix(path, "status.")) && !fabricDomainControllerStatusFields[path] {
				return nil, grpcstatus.Error(grpccodes.InvalidArgument, "status field is not writable by the fulfillment controller")
			}
		}
	}
	var response *privatev1.FabricDomainsUpdateResponse
	err := s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.FabricDomain) error {
			if !slices.Equal(current.GetSpec().GetServers(), candidate.GetSpec().GetServers()) {
				return grpcstatus.Error(grpccodes.InvalidArgument,
					"FabricDomain servers cannot change in this release; delete and recreate the FabricDomain")
			}
			return validateNetworkingStatusOnlyUpdate("FabricDomain", current, candidate)
		})
	return response, err
}

func (s *PrivateFabricDomainsServer) Delete(ctx context.Context, request *privatev1.FabricDomainsDeleteRequest) (*privatev1.FabricDomainsDeleteResponse, error) {
	domain, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "FabricDomain")
	if err != nil {
		return nil, err
	}
	if deleteAlreadyAccepted(domain.GetMetadata()) {
		return &privatev1.FabricDomainsDeleteResponse{Object: domain}, nil
	}
	object, err := s.generic.deleteObject(ctx, request)
	if err != nil {
		return nil, err
	}
	return &privatev1.FabricDomainsDeleteResponse{Object: object}, nil
}

func (s *PrivateFabricDomainsServer) Signal(ctx context.Context, request *privatev1.FabricDomainsSignalRequest) (*privatev1.FabricDomainsSignalResponse, error) {
	var response *privatev1.FabricDomainsSignalResponse
	err := s.generic.Signal(ctx, request, &response)
	return response, err
}

func (s *PrivateFabricDomainsServer) validateFabricDomain(ctx context.Context, object *privatev1.FabricDomain) error {
	if object == nil || object.GetSpec() == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "spec is mandatory")
	}
	spec := object.GetSpec()
	if len(spec.GetServers()) == 0 {
		return grpcstatus.Error(grpccodes.InvalidArgument, "servers list must not be empty")
	}
	if spec.GetVirtualNetwork() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "virtual_network is required")
	}
	switch spec.GetType() {
	case privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW:
		// Ethernet east-west is the only implemented FabricDomain type.
	case privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW,
		privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_NVLINK:
		return grpcstatus.Error(grpccodes.Unimplemented, "type not yet supported")
	default:
		return grpcstatus.Error(grpccodes.InvalidArgument, "unknown or unspecified FabricDomain type")
	}
	if !s.settings.BareMetalEnabled {
		return grpcstatus.Error(grpccodes.FailedPrecondition,
			"FabricDomains need BMaaS, which is not enabled in this deployment")
	}
	// Hold the VirtualNetwork row lock, as VirtualNetworks.Delete does, until this request transaction finishes.
	vn, err := lockParentVirtualNetwork(ctx, s.guards, s.virtualNetworks,
		spec.GetVirtualNetwork(), "", object.GetMetadata())
	if err != nil {
		return err
	}
	spec.SetVirtualNetwork(vn.GetId())

	nc, err := findSingletonNetworkClass(ctx, s.guards.networkClasses)
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to find the NetworkClass")
	}
	if nc == nil {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "no NetworkClass is configured")
	}
	if !nc.GetSpec().GetEastWestCapabilities().GetSupportsEastWestEthernet() {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"the NetworkClass does not set east_west_capabilities.supports_east_west_ethernet")
	}

	servers := slices.Clone(spec.GetServers())
	slices.Sort(servers)
	servers = slices.Compact(servers)
	if err := s.lockFabricDomainServerHosts(ctx, object, servers); err != nil {
		return err
	}

	return nil
}

func (s *PrivateFabricDomainsServer) lockFabricDomainServerHosts(
	ctx context.Context, domain *privatev1.FabricDomain, hosts []string,
) error {
	for _, host := range hosts {
		if strings.TrimSpace(host) == "" {
			return grpcstatus.Error(grpccodes.InvalidArgument, "FabricDomain server name must not be empty")
		}
	}

	matchedIDs := make(map[string]string, len(hosts))
	ambiguousHosts := make(map[string]bool)
	quotedHosts := make([]string, len(hosts))
	for i, host := range hosts {
		quotedHosts[i] = strconv.Quote(host)
	}
	domainMeta := domain.GetMetadata()
	filter := "this.metadata.tenant == " + strconv.Quote(domainMeta.GetTenant()) +
		" && this.metadata.project == " + strconv.Quote(domainMeta.GetProject()) +
		" && this.status.external_host_name in [" +
		strings.Join(quotedHosts, ", ") + "]"
	// Resolve requested hosts in one project-filtered inventory pass, then lock and revalidate the matches.
	err := forEachMatchingRecord(ctx, s.guards.bareMetalInstances, filter, func(instance *privatev1.BareMetalInstance) {
		host := instance.GetStatus().GetExternalHostName()
		if matchedIDs[host] != "" && matchedIDs[host] != instance.GetId() {
			ambiguousHosts[host] = true
			return
		}
		matchedIDs[host] = instance.GetId()
	})
	if err != nil {
		s.guards.logger.ErrorContext(ctx, "Failed to resolve FabricDomain server host", slog.Any("error", err))
		return grpcstatus.Error(grpccodes.Internal, "failed to resolve FabricDomain server host")
	}

	ids := make([]string, 0, len(hosts))
	hostByID := make(map[string]string, len(hosts))
	for _, host := range hosts {
		matchedID := matchedIDs[host]
		if ambiguousHosts[host] || matchedID == "" {
			return invalidFabricDomainServer(host)
		}
		ids = append(ids, matchedID)
		hostByID[matchedID] = host
	}
	locked, err := s.guards.bareMetalInstances.Lock().AddIds(ids...).SetReturnObjects(true).Do(ctx)
	if err != nil {
		var notFound *dao.ErrNotFound
		if errors.As(err, &notFound) && len(notFound.IDs) > 0 {
			return invalidFabricDomainServer(hostByID[notFound.IDs[0]])
		}
		return ConvertDAOErrorToGRPC(err, "lock", "")
	}
	instancesByID := make(map[string]*privatev1.BareMetalInstance, len(ids))
	for _, instance := range locked.GetObjects() {
		instancesByID[instance.GetId()] = instance
	}
	for _, host := range hosts {
		instance := instancesByID[matchedIDs[host]]
		instanceMeta := instance.GetMetadata()
		if instanceMeta.GetTenant() != domainMeta.GetTenant() ||
			instanceMeta.GetProject() != domainMeta.GetProject() ||
			instance.GetStatus().GetExternalHostName() != host || instanceMeta.HasDeletionTimestamp() {
			return invalidFabricDomainServer(host)
		}
		if refKey(instance.GetStatus().GetCluster()) != "" || instance.GetStatus().GetNodeSet() != "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server %s is a Cluster worker", host)
		}
	}
	return nil
}

func invalidFabricDomainServer(host string) error {
	return grpcstatus.Errorf(grpccodes.InvalidArgument,
		"FabricDomain server %s is not a bare-metal host of this project", host)
}
