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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

const securityGroupImplementationStrategy = "network_policy"

type PrivateSecurityGroupsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.SecurityGroupsServer = (*PrivateSecurityGroupsServer)(nil)

type PrivateSecurityGroupsServer struct {
	privatev1.UnimplementedSecurityGroupsServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.SecurityGroup]
	virtualNetworkDao *dao.GenericDAO[*privatev1.VirtualNetwork]
}

func NewPrivateSecurityGroupsServer() *PrivateSecurityGroupsServerBuilder {
	return &PrivateSecurityGroupsServerBuilder{}
}

func (b *PrivateSecurityGroupsServerBuilder) SetLogger(value *slog.Logger) *PrivateSecurityGroupsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSecurityGroupsServerBuilder) SetNotifier(value events.Notifier) *PrivateSecurityGroupsServerBuilder {
	b.notifier = value
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
	generic, err := NewGenericServer[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetService(privatev1.SecurityGroups_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateSecurityGroupsServer{
		logger:            b.logger,
		generic:           generic,
		virtualNetworkDao: virtualNetworkDao,
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

func (s *PrivateSecurityGroupsServer) Create(ctx context.Context,
	request *privatev1.SecurityGroupsCreateRequest) (response *privatev1.SecurityGroupsCreateResponse, err error) {
	securityGroup := request.GetObject()

	// Validate before creating:
	err = s.validateSecurityGroup(ctx, securityGroup, nil)
	if err != nil {
		return
	}

	// Set owner reference annotation automatically
	if securityGroup.GetMetadata() == nil {
		securityGroup.Metadata = &privatev1.Metadata{}
	}
	if securityGroup.GetMetadata().GetAnnotations() == nil {
		securityGroup.Metadata.Annotations = make(map[string]string)
	}
	securityGroup.Metadata.Annotations["osac.openshift.io/owner-reference"] = securityGroup.GetSpec().GetVirtualNetwork()

	// Set implementation strategy (system-managed, immutable after creation)
	securityGroup.GetSpec().SetImplementationStrategy(securityGroupImplementationStrategy)

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateSecurityGroupsServer) Update(ctx context.Context,
	request *privatev1.SecurityGroupsUpdateRequest) (response *privatev1.SecurityGroupsUpdateResponse, err error) {
	// Get existing object for immutability validation:
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.SecurityGroupsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.SecurityGroupsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingSecurityGroup := getResponse.GetObject()

	// Validate with existing object context:
	err = s.validateSecurityGroup(ctx, request.GetObject(), existingSecurityGroup)
	if err != nil {
		return
	}

	// Preserve immutable implementation_strategy from existing object. Validation that the
	// caller didn't attempt to change it is handled in validateImmutableFieldsSecurityGroup.
	if request.GetObject().GetSpec() != nil && existingSecurityGroup.GetSpec() != nil {
		request.GetObject().GetSpec().SetImplementationStrategy(existingSecurityGroup.GetSpec().GetImplementationStrategy())
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateSecurityGroupsServer) Delete(ctx context.Context,
	request *privatev1.SecurityGroupsDeleteRequest) (response *privatev1.SecurityGroupsDeleteResponse, err error) {
	getRequest := &privatev1.SecurityGroupsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.SecurityGroupsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}
	if err = validateNotDefault(getResponse.GetObject().GetMetadata().GetLabels(), "security group"); err != nil {
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

// validateSecurityGroup validates the SecurityGroup object.
func (s *PrivateSecurityGroupsServer) validateSecurityGroup(ctx context.Context,
	newSecurityGroup *privatev1.SecurityGroup, existingSecurityGroup *privatev1.SecurityGroup) error {

	if newSecurityGroup == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "security group is mandatory")
	}

	spec := newSecurityGroup.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "security group spec is mandatory")
	}

	// Check immutable fields (only on Update)
	if err := validateImmutableFieldsSecurityGroup(newSecurityGroup, existingSecurityGroup); err != nil {
		return err
	}

	// Validate parent VirtualNetwork
	// Only validate on Create or if virtual_network changed (though it shouldn't on Update)
	if existingSecurityGroup == nil || spec.GetVirtualNetwork() != existingSecurityGroup.GetSpec().GetVirtualNetwork() {
		if err := s.validateVirtualNetworkReference(ctx, spec); err != nil {
			return err
		}
	}

	// Validate security rules
	if err := s.validateSecurityRules(spec); err != nil {
		return err
	}

	return nil
}

// validateVirtualNetworkReference validates that the referenced VirtualNetwork exists and is in READY state.
func (s *PrivateSecurityGroupsServer) validateVirtualNetworkReference(ctx context.Context,
	spec *privatev1.SecurityGroupSpec) error {

	virtualNetworkID := spec.GetVirtualNetwork()
	if virtualNetworkID == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.virtual_network' is required")
	}

	// Get parent VirtualNetwork by ID
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

	// Check parent VirtualNetwork is READY
	if virtualNetwork.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"parent VirtualNetwork '%s' is not in READY state (current state: %s)",
			virtualNetworkID, virtualNetwork.GetStatus().GetState().String())
	}

	return nil
}

// validateSecurityRules validates the ingress and egress security rules.
func (s *PrivateSecurityGroupsServer) validateSecurityRules(spec *privatev1.SecurityGroupSpec) error {
	// Validate ingress rules
	for i, rule := range spec.GetIngress() {
		if err := validateSecurityRule(rule, "ingress", i); err != nil {
			return err
		}
	}

	// Validate egress rules
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

	// Protocol is required (UNSPECIFIED is not allowed)
	if rule.GetProtocol() == privatev1.Protocol_PROTOCOL_UNSPECIFIED {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: protocol is required", ruleType, index)
	}

	// Port validation for TCP/UDP protocols
	if rule.GetProtocol() == privatev1.Protocol_PROTOCOL_TCP || rule.GetProtocol() == privatev1.Protocol_PROTOCOL_UDP {
		// If port range is specified, validate it
		if rule.HasPortFrom() || rule.HasPortTo() {
			portFrom := rule.GetPortFrom()
			portTo := rule.GetPortTo()

			// Both must be set if either is set
			if !rule.HasPortFrom() || !rule.HasPortTo() {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"%s rule at index %d: both port_from and port_to must be set together", ruleType, index)
			}

			// Validate port range
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
		}
	}

	// At least one CIDR must be specified
	if rule.GetIpv4Cidr() == "" && rule.GetIpv6Cidr() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: at least one of ipv4_cidr or ipv6_cidr must be provided", ruleType, index)
	}

	if err := canonicalizeDualStackCIDRs(
		rule.GetIpv4Cidr, rule.SetIpv4Cidr,
		rule.GetIpv6Cidr, rule.SetIpv6Cidr,
	); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s rule at index %d: %v", ruleType, index, err)
	}

	return nil
}

// validateImmutableFieldsSecurityGroup validates that immutable fields have not been changed.
func validateImmutableFieldsSecurityGroup(newSecurityGroup *privatev1.SecurityGroup, existingSecurityGroup *privatev1.SecurityGroup) error {
	if existingSecurityGroup == nil {
		return nil // Create operation, no immutability checks
	}

	newSpec := newSecurityGroup.GetSpec()
	existingSpec := existingSecurityGroup.GetSpec()

	// Check immutable virtual_network field
	if newSpec.GetVirtualNetwork() != existingSpec.GetVirtualNetwork() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.virtual_network' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetVirtualNetwork(), newSpec.GetVirtualNetwork())
	}

	// Check immutable implementation_strategy field. The field is OUTPUT_ONLY so well-behaved
	// clients won't set it (it arrives as ""); only reject when the caller explicitly sends a
	// non-empty value that differs from the stored one.
	if newStrategy := newSpec.GetImplementationStrategy(); newStrategy != "" && newStrategy != existingSpec.GetImplementationStrategy() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.implementation_strategy' is immutable and cannot be changed from '%s' to '%s'",
			existingSpec.GetImplementationStrategy(), newStrategy)
	}

	return nil
}
