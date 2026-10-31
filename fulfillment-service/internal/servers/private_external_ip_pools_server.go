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
	"log/slog"
	"net/netip"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateExternalIPPoolsServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	filterDesc         protoreflect.MessageDescriptor
	networkingSettings *NetworkingSettings
}

var _ privatev1.ExternalIPPoolsServer = (*PrivateExternalIPPoolsServer)(nil)

type PrivateExternalIPPoolsServer struct {
	privatev1.UnimplementedExternalIPPoolsServer

	logger   *slog.Logger
	generic  *GenericServer[*privatev1.ExternalIPPool]
	settings NetworkingSettings
	guards   *networkingGuards
}

func NewPrivateExternalIPPoolsServer() *PrivateExternalIPPoolsServerBuilder {
	return &PrivateExternalIPPoolsServerBuilder{}
}

func (b *PrivateExternalIPPoolsServerBuilder) SetLogger(value *slog.Logger) *PrivateExternalIPPoolsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateExternalIPPoolsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateExternalIPPoolsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateExternalIPPoolsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateExternalIPPoolsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateExternalIPPoolsServerBuilder {
	b.filterDesc = value
	return b
}

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateExternalIPPoolsServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateExternalIPPoolsServerBuilder {
	b.networkingSettings = &value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) Build() (result *PrivateExternalIPPoolsServer, err error) {
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

	generic, err := NewGenericServer[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetService(privatev1.ExternalIPPools_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	result = &PrivateExternalIPPoolsServer{
		logger:   b.logger,
		generic:  generic,
		settings: resolveNetworkingSettings(b.networkingSettings),
		guards:   guards,
	}
	return
}

func (s *PrivateExternalIPPoolsServer) List(ctx context.Context,
	request *privatev1.ExternalIPPoolsListRequest) (response *privatev1.ExternalIPPoolsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Get(ctx context.Context,
	request *privatev1.ExternalIPPoolsGetRequest) (response *privatev1.ExternalIPPoolsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Create(ctx context.Context,
	request *privatev1.ExternalIPPoolsCreateRequest) (response *privatev1.ExternalIPPoolsCreateResponse, err error) {
	if err = rejectOutputStatusOnCreate(request.GetObject().HasStatus()); err != nil {
		return
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, pool *privatev1.ExternalIPPool) error {
			if err := s.validateCreate(ctx, pool); err != nil {
				return err
			}
			total := calculatePoolCapacity(pool.GetSpec().GetCidrs())
			pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
				State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING,
				Total:     total,
				Available: total,
			}.Build())
			return nil
		})
	return
}

// Update writes status only. The capacity counters (total, allocated, available) are kept by the fulfillment
// service, so the values in the request are ignored.
func (s *PrivateExternalIPPoolsServer) Update(ctx context.Context,
	request *privatev1.ExternalIPPoolsUpdateRequest) (response *privatev1.ExternalIPPoolsUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.ExternalIPPool) error {
			if err := validateNetworkingStatusOnlyUpdate("ExternalIPPool", current, candidate); err != nil {
				return err
			}
			if err := rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata()); err != nil {
				return err
			}
			if !candidate.HasStatus() {
				candidate.SetStatus(&privatev1.ExternalIPPoolStatus{})
			}
			candidate.GetStatus().SetTotal(current.GetStatus().GetTotal())
			candidate.GetStatus().SetAllocated(current.GetStatus().GetAllocated())
			candidate.GetStatus().SetAvailable(current.GetStatus().GetAvailable())
			return nil
		})
	return
}

// validateCreate checks a pool create under the exclusive NetworkClass lock, so a concurrent VirtualNetwork or Cluster
// create can't overlap it.
func (s *PrivateExternalIPPoolsServer) validateCreate(ctx context.Context,
	pool *privatev1.ExternalIPPool) error {
	spec := pool.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP pool spec is mandatory")
	}
	if spec.GetIpFamily() != privatev1.IPFamily_IP_FAMILY_IPV4 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ip_family' must be IP_FAMILY_IPV4; IPv6 and unspecified-family pools are not supported")
	}
	canonicalCIDRs, err := validateSingleIPv4PoolCIDR(spec.GetCidrs())
	if err != nil {
		return err
	}
	prefix := netip.MustParsePrefix(canonicalCIDRs[0])

	networkClass, err := requireReadyNetworkClass(ctx, s.guards.networkClasses, true)
	if err != nil {
		return err
	}
	if excluded, overlaps := s.settings.firstExcludedOverlap(prefix); overlaps {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs[0]' overlaps the excluded range %s", excluded)
	}
	if defaultCIDR, err := netip.ParsePrefix(
		networkClass.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()); err == nil && defaultCIDR.Overlaps(prefix) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs[0]' overlaps the default VirtualNetwork CIDR %s of the NetworkClass", defaultCIDR)
	}
	poolID, err := s.guards.firstPoolOverlap(ctx, prefix, "")
	if err != nil {
		return err
	}
	if poolID != "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs[0]' overlaps ExternalIPPool '%s'", poolID)
	}
	other, err := s.guards.firstVirtualNetworkOrClusterOverlap(ctx, prefix)
	if err != nil {
		return err
	}
	if other != "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.cidrs[0]' overlaps %s", other)
	}
	return nil
}

func (s *PrivateExternalIPPoolsServer) Delete(ctx context.Context,
	request *privatev1.ExternalIPPoolsDeleteRequest) (response *privatev1.ExternalIPPoolsDeleteResponse, err error) {
	pool, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "ExternalIPPool")
	if err != nil {
		return
	}
	if deleteAlreadyAccepted(pool.GetMetadata()) {
		return &privatev1.ExternalIPPoolsDeleteResponse{}, nil
	}
	if err = rejectGuardedDelete(ctx, s.guards, s.guards.externalIPs, "ExternalIP", "ExternalIPPool", pool,
		func(externalIP *privatev1.ExternalIP) bool {
			return referenceMatches(externalIP.GetSpec().GetPool(), pool.GetId(), pool.GetMetadata())
		}); err != nil {
		return
	}
	if err = markDeleting(ctx, s.generic.dao, pool); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Signal(ctx context.Context,
	request *privatev1.ExternalIPPoolsSignalRequest) (response *privatev1.ExternalIPPoolsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
