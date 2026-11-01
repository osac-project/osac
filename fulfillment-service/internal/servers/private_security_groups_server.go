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
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateSecurityGroupsServerBuilder struct {
	logger             *slog.Logger
	attributionLogic   auth.AttributionLogic
	tenancyLogic       auth.TenancyLogic
	metricsRegisterer  prometheus.Registerer
	filterDesc         protoreflect.MessageDescriptor
	networkingSettings *NetworkingSettings
}

var _ privatev1.SecurityGroupsServer = (*PrivateSecurityGroupsServer)(nil)

type PrivateSecurityGroupsServer struct {
	privatev1.UnimplementedSecurityGroupsServer

	logger   *slog.Logger
	generic  *GenericServer[*privatev1.SecurityGroup]
	settings NetworkingSettings
	guards   *networkingGuards

	// virtualNetworks resolves the parent reference with the caller's visibility.
	virtualNetworks *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func NewPrivateSecurityGroupsServer() *PrivateSecurityGroupsServerBuilder {
	return &PrivateSecurityGroupsServerBuilder{}
}

func (b *PrivateSecurityGroupsServerBuilder) SetLogger(value *slog.Logger) *PrivateSecurityGroupsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSecurityGroupsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateSecurityGroupsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateSecurityGroupsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSecurityGroupsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateSecurityGroupsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateSecurityGroupsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateSecurityGroupsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateSecurityGroupsServerBuilder {
	b.filterDesc = value
	return b
}

// SetNetworkingSettings sets the networking installation settings. This is optional; the default is
// DefaultNetworkingSettings.
func (b *PrivateSecurityGroupsServerBuilder) SetNetworkingSettings(value NetworkingSettings) *PrivateSecurityGroupsServerBuilder {
	b.networkingSettings = &value
	return b
}

func (b *PrivateSecurityGroupsServerBuilder) Build() (result *PrivateSecurityGroupsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
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
	generic, err := NewGenericServer[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetService(privatev1.SecurityGroups_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateSecurityGroupsServer{
		logger:          b.logger,
		generic:         generic,
		settings:        resolveNetworkingSettings(b.networkingSettings),
		guards:          guards,
		virtualNetworks: virtualNetworks,
	}
	return
}

func (s *PrivateSecurityGroupsServer) List(ctx context.Context,
	request *privatev1.SecurityGroupsListRequest) (response *privatev1.SecurityGroupsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateSecurityGroupsServer) Get(ctx context.Context,
	request *privatev1.SecurityGroupsGetRequest) (response *privatev1.SecurityGroupsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// automaticSecurityGroupPrefix is the reserved name prefix of the automatic Cluster SecurityGroups.
const automaticSecurityGroupPrefix = "auto-sg-"

func (s *PrivateSecurityGroupsServer) Create(ctx context.Context,
	request *privatev1.SecurityGroupsCreateRequest) (response *privatev1.SecurityGroupsCreateResponse, err error) {
	if err = rejectOutputStatusOnCreate(request.GetObject().HasStatus()); err != nil {
		return
	}
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, group *privatev1.SecurityGroup) error {
			if err := lockTenantForCreate(ctx, group.GetMetadata().GetTenant()); err != nil {
				return err
			}
			return s.prepareCreate(ctx, group)
		})
	return
}

// prepareCreate validates a SecurityGroup create and sets its owner reference and initial status. The parent
// VirtualNetwork is locked first.
func (s *PrivateSecurityGroupsServer) prepareCreate(ctx context.Context, group *privatev1.SecurityGroup) error {
	if err := validateReservedCreate(ctx, group.GetMetadata(), automaticSecurityGroupPrefix); err != nil {
		return err
	}
	spec := group.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "security group spec is mandatory")
	}
	key := refKey(spec.GetVirtualNetwork())
	if spec.GetVirtualNetwork() == nil || key == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.virtual_network' is required")
	}
	virtualNetwork, err := lockParentVirtualNetwork(ctx, s.guards, s.virtualNetworks, key, group.GetMetadata())
	if err != nil {
		return err
	}
	spec.SetVirtualNetwork(privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetId()}.Build())
	if err := validateSecurityRules(spec); err != nil {
		return err
	}
	if err := s.rejectUnsupportedICMP(ctx, spec); err != nil {
		return err
	}
	if group.GetMetadata().GetAnnotations() == nil {
		group.GetMetadata().SetAnnotations(map[string]string{})
	}
	if _, ok := group.GetMetadata().GetAnnotations()[ownerReferenceAnnotation]; !ok {
		group.GetMetadata().GetAnnotations()[ownerReferenceAnnotation] = virtualNetwork.GetId()
	}
	group.SetStatus(privatev1.SecurityGroupStatus_builder{
		State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
	}.Build())
	return nil
}

// Update writes status only; the spec is immutable. Public rule updates use updateFromPublic.
func (s *PrivateSecurityGroupsServer) Update(ctx context.Context,
	request *privatev1.SecurityGroupsUpdateRequest) (response *privatev1.SecurityGroupsUpdateResponse, err error) {
	if request.GetObject().GetId() == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, current, candidate *privatev1.SecurityGroup) error {
			if err := validateNetworkingStatusOnlyUpdate("SecurityGroup", current, candidate); err != nil {
				return err
			}
			return rejectStateChangeWhileDeleting(current, candidate, current.GetMetadata())
		})
	return
}

// updateFromPublic replaces the rules of a SecurityGroup. The public server fixes the mask to spec.ingress and
// spec.egress.
func (s *PrivateSecurityGroupsServer) updateFromPublic(ctx context.Context,
	request *privatev1.SecurityGroupsUpdateRequest) (*privatev1.SecurityGroupsUpdateResponse, error) {
	var response *privatev1.SecurityGroupsUpdateResponse
	err := s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, current, candidate *privatev1.SecurityGroup) error {
			if err := validateNetworkingStatusOnlyUpdate("SecurityGroup", current, candidate,
				"spec.ingress", "spec.egress"); err != nil {
				return err
			}
			return s.applyRuleUpdate(ctx, current, candidate)
		})
	return response, err
}

// securityGroupUpdatingMessage is the status message of a READY SecurityGroup whose rule update runs.
const securityGroupUpdatingMessage = "Updating"

// applyRuleUpdate checks the preconditions of a rule update of the locked SecurityGroup and, in the same transaction,
// sets the status: a READY group stays READY with the message Updating, and a FAILED group becomes PENDING. With
// provider networking disabled, the rules are only stored.
func (s *PrivateSecurityGroupsServer) applyRuleUpdate(ctx context.Context,
	current, candidate *privatev1.SecurityGroup) error {
	if isAutomatic(current.GetMetadata()) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"SecurityGroup %s is automatic; its rules cannot change", current.GetId())
	}
	if current.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "SecurityGroup %s is being deleted", current.GetId())
	}
	state := current.GetStatus().GetState()
	ready := state == privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY
	updating := ready && current.GetStatus().GetMessage() == securityGroupUpdatingMessage
	if (!ready || updating) && state != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"SecurityGroup %s is being created or updated; retry when it is READY or FAILED", current.GetId())
	}
	if err := validateSecurityRules(candidate.GetSpec()); err != nil {
		return err
	}
	if err := s.rejectUnsupportedICMP(ctx, candidate.GetSpec()); err != nil {
		return err
	}
	status := proto.Clone(current.GetStatus()).(*privatev1.SecurityGroupStatus)
	if status == nil {
		status = &privatev1.SecurityGroupStatus{}
	}
	if s.settings.ProvisioningEnabled {
		if ready {
			status.SetMessage(securityGroupUpdatingMessage)
		} else {
			status.SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING)
			status.ClearMessage()
		}
	}
	candidate.SetStatus(status)
	return nil
}

func (s *PrivateSecurityGroupsServer) Delete(ctx context.Context,
	request *privatev1.SecurityGroupsDeleteRequest) (response *privatev1.SecurityGroupsDeleteResponse, err error) {
	securityGroup, err := lockLifecycleResource(ctx, s.generic.dao, request.GetId(), "SecurityGroup")
	if err != nil {
		return
	}
	if deleteAlreadyAccepted(securityGroup.GetMetadata()) {
		return &privatev1.SecurityGroupsDeleteResponse{}, nil
	}
	if isAutomatic(securityGroup.GetMetadata()) && !auth.IsControllerServiceAccount(ctx) {
		err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete automatic SecurityGroup '%s': it is deleted with its Cluster", securityGroup.GetId())
		return
	}
	if err = validateNotDefault(ctx, securityGroup.GetMetadata().GetLabels(), "security group"); err != nil {
		return
	}
	if err = s.guards.rejectDeleteIfWorkloadsReference(ctx, "SecurityGroup", securityGroup,
		func(*privatev1.SubnetLocalReference) bool { return false },
		func(refs []*privatev1.SecurityGroupLocalReference) bool {
			for _, ref := range refs {
				if referenceMatches(ref, securityGroup.GetId(), securityGroup.GetMetadata()) {
					return true
				}
			}
			return false
		}); err != nil {
		return
	}
	if err = markDeleting(ctx, s.generic.dao, securityGroup); err != nil {
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateSecurityGroupsServer) Signal(ctx context.Context,
	request *privatev1.SecurityGroupsSignalRequest) (response *privatev1.SecurityGroupsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// securityGroupICMPMessage is the error of an icmp rule on a deployment whose network managers cannot apply it. The
// docs and the provider guide quote it.
const securityGroupICMPMessage = "%s rule at index %d: protocol icmp is not supported by this " +
	"deployment's network managers; use protocol all, which includes ICMP"

// firstICMPRule returns the direction and index of the first icmp rule, or an index of -1 when there is none.
func firstICMPRule(spec *privatev1.SecurityGroupSpec) (string, int) {
	isICMP := func(rule *privatev1.SecurityRule) bool {
		return rule.GetProtocol() == privatev1.Protocol_PROTOCOL_ICMP
	}
	if index := slices.IndexFunc(spec.GetIngress(), isICMP); index >= 0 {
		return "ingress", index
	}
	return "egress", slices.IndexFunc(spec.GetEgress(), isICMP)
}

// acceptsICMPRules reports whether SecurityGroup rules can use protocol icmp. A recorded capability decides. Without
// one no manager was resolved: with provider networking disabled the rules are only stored, and otherwise the
// capability is absent.
func acceptsICMPRules(status *privatev1.NetworkClassStatus, provisioningEnabled bool) bool {
	if status.HasSecurityGroupIcmp() {
		return status.GetSecurityGroupIcmp()
	}
	return !provisioningEnabled
}

// rejectUnsupportedICMP rejects icmp rules on a NetworkClass whose managers cannot apply them. The cudn_evpn manager
// keeps its own final error as a safety net.
func (s *PrivateSecurityGroupsServer) rejectUnsupportedICMP(ctx context.Context,
	spec *privatev1.SecurityGroupSpec) error {
	direction, index := firstICMPRule(spec)
	if index < 0 {
		return nil
	}
	nc, err := findSingletonNetworkClass(ctx, s.guards.networkClasses)
	if err != nil {
		return grpcstatus.Error(grpccodes.Internal, "failed to find the NetworkClass")
	}
	if nc == nil {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "no NetworkClass is configured")
	}
	if acceptsICMPRules(nc.GetStatus(), s.settings.ProvisioningEnabled) {
		return nil
	}
	return grpcstatus.Errorf(grpccodes.InvalidArgument, securityGroupICMPMessage, direction, index)
}

// validateSecurityRules validates the ingress and egress security rules.
func validateSecurityRules(spec *privatev1.SecurityGroupSpec) error {
	for i, rule := range spec.GetIngress() {
		if err := validateSecurityRule(rule, "ingress", i); err != nil {
			return err
		}
	}
	for i, rule := range spec.GetEgress() {
		if err := validateSecurityRule(rule, "egress", i); err != nil {
			return err
		}
	}
	return nil
}

// validateSecurityRule validates a single security rule.
func validateSecurityRule(rule *privatev1.SecurityRule, ruleType string, index int) error {
	if rule == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d is nil", ruleType, index)
	}

	switch rule.GetProtocol() {
	case privatev1.Protocol_PROTOCOL_TCP, privatev1.Protocol_PROTOCOL_UDP:
		if !rule.HasPortFrom() || !rule.HasPortTo() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s rule at index %d: tcp and udp rules require port_from and port_to", ruleType, index)
		}
		portFrom := rule.GetPortFrom()
		portTo := rule.GetPortTo()
		if portFrom < 1 || portFrom > 65535 {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s rule at index %d: port_from must be between 1 and 65535", ruleType, index)
		}
		if portTo < 1 || portTo > 65535 {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s rule at index %d: port_to must be between 1 and 65535", ruleType, index)
		}
		if portFrom > portTo {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s rule at index %d: port_from (%d) cannot be greater than port_to (%d)",
				ruleType, index, portFrom, portTo)
		}
	case privatev1.Protocol_PROTOCOL_ICMP, privatev1.Protocol_PROTOCOL_ALL:
		if rule.HasPortFrom() || rule.HasPortTo() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s rule at index %d: icmp and all rules must not set port_from or port_to", ruleType, index)
		}
	default:
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: protocol is required", ruleType, index)
	}

	if rule.GetIpv4Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: ipv4_cidr is required and must be a canonical IPv4 CIDR", ruleType, index)
	}
	if _, err := parseAndValidateCanonicalCIDR(rule.GetIpv4Cidr(), cidrIPv4); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: %v", ruleType, index, err)
	}

	return nil
}
