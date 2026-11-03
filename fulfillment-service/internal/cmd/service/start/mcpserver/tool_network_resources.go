/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions under the License.
*/

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type NetworkResourceType string

const (
	NetworkResourceTypeVirtualNetwork NetworkResourceType = "virtual_network"
	NetworkResourceTypeSubnet         NetworkResourceType = "subnet"
	NetworkResourceTypeSecurityGroup  NetworkResourceType = "security_group"

	maxNetworkSecurityRules = 100
)

// CreateNetworkResourceInput creates one tenant network prerequisite. A
// non-empty ID is an edit request, which the tool rejects without dispatch.
type CreateNetworkResourceInput struct {
	ID               string                     `json:"id,omitempty" jsonschema:"optional existing resource ID; a non-empty value requests an unsupported in-place edit"`
	ResourceType     NetworkResourceType        `json:"resource_type" jsonschema:"network resource type: virtual_network, subnet, or security_group"`
	Name             string                     `json:"name" jsonschema:"name for the new network resource"`
	VirtualNetworkID string                     `json:"virtual_network_id,omitempty" jsonschema:"parent VirtualNetwork ID for subnet and security_group"`
	IPv4Cidr         string                     `json:"ipv4_cidr,omitempty" jsonschema:"canonical IPv4 CIDR for virtual_network and subnet"`
	IngressRules     []NetworkSecurityRuleInput `json:"ingress_rules,omitempty" jsonschema:"optional SecurityGroup ingress rules"`
	EgressRules      []NetworkSecurityRuleInput `json:"egress_rules,omitempty" jsonschema:"optional SecurityGroup egress rules"`
}

type NetworkSecurityRuleInput struct {
	Protocol string `json:"protocol" jsonschema:"rule protocol: tcp, udp, icmp, or all"`
	PortFrom *int32 `json:"port_from,omitempty" jsonschema:"starting TCP or UDP port from 1 through 65535"`
	PortTo   *int32 `json:"port_to,omitempty" jsonschema:"ending TCP or UDP port from 1 through 65535"`
	IPv4Cidr string `json:"ipv4_cidr" jsonschema:"canonical IPv4 CIDR matched by the rule"`
}

type CreateNetworkResourceOutput struct {
	ResourceType NetworkResourceType `json:"resource_type"`
	ID           string              `json:"id"`
	Version      int32               `json:"version"`
	State        string              `json:"state"`
}

type DeleteNetworkResourceInput struct {
	ResourceType NetworkResourceType `json:"resource_type" jsonschema:"network resource type: virtual_network, subnet, or security_group"`
	ID           string              `json:"id" jsonschema:"ID of the exact network resource to delete"`
}

type DeleteNetworkResourceOutput struct {
	ResourceType NetworkResourceType `json:"resource_type"`
	ID           string              `json:"id"`
}

type networkCreateRequest struct {
	resourceType     NetworkResourceType
	name             string
	virtualNetworkID string
	ipv4Cidr         string
	ingress          []*publicv1.SecurityRule
	egress           []*publicv1.SecurityRule
}

func handleCreateNetworkResource(
	virtualNetworks publicv1.VirtualNetworksClient,
	subnets publicv1.SubnetsClient,
	securityGroups publicv1.SecurityGroupsClient,
) mcp.ToolHandlerFor[CreateNetworkResourceInput, CreateNetworkResourceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input CreateNetworkResourceInput,
	) (*mcp.CallToolResult, CreateNetworkResourceOutput, error) {
		if strings.TrimSpace(input.ID) != "" {
			return nil, CreateNetworkResourceOutput{}, safeUnsupportedInPlaceNetworkEditError()
		}
		input = normalizeCreateNetworkResourceInput(input)
		var err error
		request, err := networkCreateRequestFromInput(input)
		if err != nil {
			return nil, CreateNetworkResourceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CreateNetworkResourceOutput{}, err
		}
		switch request.resourceType {
		case NetworkResourceTypeVirtualNetwork:
			response, err := virtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: &request.ipv4Cidr,
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, CreateNetworkResourceOutput{}, safeVirtualNetworkCreateError(err)
			}
			created := response.GetObject()
			return nil, networkCreateOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		case NetworkResourceTypeSubnet:
			response, err := subnets.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: request.virtualNetworkID}.Build(),
						Ipv4Cidr:       &request.ipv4Cidr,
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, CreateNetworkResourceOutput{}, safeDependentNetworkCreateError(
					request.virtualNetworkID, err,
				)
			}
			created := response.GetObject()
			return nil, networkCreateOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		case NetworkResourceTypeSecurityGroup:
			response, err := securityGroups.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: request.virtualNetworkID}.Build(),
						Ingress:        request.ingress,
						Egress:         request.egress,
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, CreateNetworkResourceOutput{}, safeDependentNetworkCreateError(
					request.virtualNetworkID, err,
				)
			}
			created := response.GetObject()
			return nil, networkCreateOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		default:
			return nil, CreateNetworkResourceOutput{}, safeInvalidRequestError(unsupportedNetworkResourceTypeError(input.ResourceType))
		}
	}
}

func handleDeleteNetworkResource(
	virtualNetworks publicv1.VirtualNetworksClient,
	subnets publicv1.SubnetsClient,
	securityGroups publicv1.SecurityGroupsClient,
) mcp.ToolHandlerFor[DeleteNetworkResourceInput, DeleteNetworkResourceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input DeleteNetworkResourceInput,
	) (*mcp.CallToolResult, DeleteNetworkResourceOutput, error) {
		var err error
		input, err = normalizeDeleteNetworkResourceInput(input)
		if err != nil {
			return nil, DeleteNetworkResourceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, DeleteNetworkResourceOutput{}, err
		}
		switch input.ResourceType {
		case NetworkResourceTypeVirtualNetwork:
			_, err = virtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{Id: input.ID}.Build())
		case NetworkResourceTypeSubnet:
			_, err = subnets.Delete(ctx, publicv1.SubnetsDeleteRequest_builder{Id: input.ID}.Build())
		case NetworkResourceTypeSecurityGroup:
			_, err = securityGroups.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{Id: input.ID}.Build())
		}
		if err != nil {
			return nil, DeleteNetworkResourceOutput{}, safeNetworkDeleteError(err)
		}
		return nil, DeleteNetworkResourceOutput(input), nil
	}
}

func normalizeCreateNetworkResourceInput(input CreateNetworkResourceInput) CreateNetworkResourceInput {
	input.ID = strings.TrimSpace(input.ID)
	input.ResourceType = NetworkResourceType(strings.TrimSpace(string(input.ResourceType)))
	input.Name = strings.TrimSpace(input.Name)
	input.VirtualNetworkID = strings.TrimSpace(input.VirtualNetworkID)
	input.IPv4Cidr = strings.TrimSpace(input.IPv4Cidr)
	input.IngressRules = normalizeNetworkSecurityRuleInputs(input.IngressRules)
	input.EgressRules = normalizeNetworkSecurityRuleInputs(input.EgressRules)
	return input
}

func normalizeDeleteNetworkResourceInput(input DeleteNetworkResourceInput) (DeleteNetworkResourceInput, error) {
	input.ResourceType = NetworkResourceType(strings.TrimSpace(string(input.ResourceType)))
	input.ID = strings.TrimSpace(input.ID)
	switch input.ResourceType {
	case NetworkResourceTypeVirtualNetwork, NetworkResourceTypeSubnet, NetworkResourceTypeSecurityGroup:
	default:
		return DeleteNetworkResourceInput{}, unsupportedNetworkResourceTypeError(input.ResourceType)
	}
	if input.ID == "" {
		return DeleteNetworkResourceInput{}, fmt.Errorf("id is required")
	}
	return input, nil
}

func networkCreateRequestFromInput(input CreateNetworkResourceInput) (networkCreateRequest, error) {
	request := networkCreateRequest{
		resourceType:     input.ResourceType,
		name:             input.Name,
		virtualNetworkID: input.VirtualNetworkID,
		ipv4Cidr:         input.IPv4Cidr,
	}
	if input.Name == "" {
		return networkCreateRequest{}, fmt.Errorf("name is required")
	}
	switch input.ResourceType {
	case NetworkResourceTypeVirtualNetwork:
		if input.VirtualNetworkID != "" {
			return networkCreateRequest{}, fmt.Errorf("virtual_network_id is not valid for virtual_network")
		}
		if len(input.IngressRules) != 0 || len(input.EgressRules) != 0 {
			return networkCreateRequest{}, fmt.Errorf("security rules are not valid for virtual_network")
		}
		if err := validation.ValidateCanonicalIPv4CIDR("ipv4_cidr", input.IPv4Cidr); err != nil {
			return networkCreateRequest{}, err
		}
	case NetworkResourceTypeSubnet:
		if input.VirtualNetworkID == "" {
			return networkCreateRequest{}, fmt.Errorf("virtual_network_id is required for subnet")
		}
		if len(input.IngressRules) != 0 || len(input.EgressRules) != 0 {
			return networkCreateRequest{}, fmt.Errorf("security rules are not valid for subnet")
		}
		if err := validation.ValidateCanonicalIPv4CIDR("ipv4_cidr", input.IPv4Cidr); err != nil {
			return networkCreateRequest{}, err
		}
	case NetworkResourceTypeSecurityGroup:
		if input.VirtualNetworkID == "" {
			return networkCreateRequest{}, fmt.Errorf("virtual_network_id is required for security_group")
		}
		if input.IPv4Cidr != "" {
			return networkCreateRequest{}, fmt.Errorf("ipv4_cidr is not valid for security_group")
		}
		if len(input.IngressRules)+len(input.EgressRules) > maxNetworkSecurityRules {
			return networkCreateRequest{}, fmt.Errorf(
				"ingress_rules and egress_rules must not contain more than %d rules in total",
				maxNetworkSecurityRules,
			)
		}
		ingress, err := networkSecurityRulesFromInput(input.IngressRules, "ingress_rules")
		if err != nil {
			return networkCreateRequest{}, err
		}
		egress, err := networkSecurityRulesFromInput(input.EgressRules, "egress_rules")
		if err != nil {
			return networkCreateRequest{}, err
		}
		request.ingress = ingress
		request.egress = egress
	default:
		return networkCreateRequest{}, unsupportedNetworkResourceTypeError(input.ResourceType)
	}
	return request, nil
}

func normalizeNetworkSecurityRuleInputs(inputs []NetworkSecurityRuleInput) []NetworkSecurityRuleInput {
	if inputs == nil {
		return nil
	}
	normalized := make([]NetworkSecurityRuleInput, len(inputs))
	for index, input := range inputs {
		input.Protocol = strings.TrimSpace(input.Protocol)
		input.IPv4Cidr = strings.TrimSpace(input.IPv4Cidr)
		normalized[index] = input
	}
	return normalized
}

func networkSecurityRulesFromInput(
	inputs []NetworkSecurityRuleInput, field string,
) ([]*publicv1.SecurityRule, error) {
	rules := make([]*publicv1.SecurityRule, len(inputs))
	for index, input := range inputs {
		rule, err := networkSecurityRuleFromInput(input, field, index)
		if err != nil {
			return nil, err
		}
		rules[index] = rule
	}
	return rules, nil
}

func networkSecurityRuleFromInput(
	input NetworkSecurityRuleInput, field string, index int,
) (*publicv1.SecurityRule, error) {
	protocol, err := validation.ParseSecurityRuleProtocol(input.Protocol)
	if err != nil {
		return nil, fmt.Errorf("%s[%d].%w", field, index, err)
	}
	securityRule := validation.SecurityRule{
		Protocol: protocol,
		IPv4CIDR: input.IPv4Cidr,
	}
	if input.PortFrom != nil {
		securityRule.PortFrom = *input.PortFrom
		securityRule.HasPortFrom = true
	}
	if input.PortTo != nil {
		securityRule.PortTo = *input.PortTo
		securityRule.HasPortTo = true
	}
	err = validation.ValidateSecurityRule(securityRule, field, index)
	if err != nil {
		return nil, err
	}
	return publicv1.SecurityRule_builder{
		Protocol: publicv1.Protocol(protocol),
		PortFrom: input.PortFrom,
		PortTo:   input.PortTo,
		Ipv4Cidr: &input.IPv4Cidr,
	}.Build(), nil
}

func networkCreateOutput(
	resourceType NetworkResourceType, id string, metadata *publicv1.Metadata, state string,
) CreateNetworkResourceOutput {
	return CreateNetworkResourceOutput{
		ResourceType: resourceType,
		ID:           id,
		Version:      metadata.GetVersion(),
		State:        state,
	}
}

func unsupportedNetworkResourceTypeError(resourceType NetworkResourceType) error {
	return fmt.Errorf(
		"resource_type must be one of %s, %s, or %s",
		NetworkResourceTypeVirtualNetwork,
		NetworkResourceTypeSubnet,
		NetworkResourceTypeSecurityGroup,
	)
}

func safeUnsupportedInPlaceNetworkEditError() error {
	return safeInvalidRequestError(fmt.Errorf(
		"unsupported_in_place: network resources are immutable; create and delete are separate tools",
	))
}

func safeNetworkCreateError(err error) error {
	if status.Code(err) == codes.FailedPrecondition {
		return newToolError(
			"conflict",
			"Aborted",
			"The network resource state conflicts with the request.",
			false,
		)
	}
	return safePublicAPIError(err)
}

func safeDependentNetworkCreateError(parentID string, err error) error {
	statusError := status.Convert(err)
	switch status.Code(err) {
	case codes.NotFound, codes.PermissionDenied:
		return safePrerequisiteError("VirtualNetwork", parentID, err)
	case codes.InvalidArgument:
		message := statusError.Message()
		if isMissingVirtualNetworkParentError(message) {
			return safeMissingPrerequisiteError("VirtualNetwork", parentID)
		}
		return safeNetworkCreateError(err)
	default:
		return safeNetworkCreateError(err)
	}
}

func isMissingVirtualNetworkParentError(message string) bool {
	referenceMissing := strings.Contains(message, "object.spec.virtual_network") &&
		strings.Contains(message, "VirtualNetwork") &&
		strings.Contains(message, "not found")
	lockedMissing := strings.Contains(message, "parent VirtualNetwork") &&
		strings.Contains(message, "does not exist")
	return referenceMissing || lockedMissing
}

func safeVirtualNetworkCreateError(err error) error {
	if status.Code(err) == codes.FailedPrecondition &&
		strings.Contains(status.Convert(err).Message(), "no NetworkClass is configured") {
		return safeMissingPrerequisiteError("NetworkClass", "")
	}
	return safeNetworkCreateError(err)
}

func safeNetworkDeleteError(err error) error {
	if status.Code(err) == codes.FailedPrecondition {
		return newToolError(
			"conflict",
			"Aborted",
			"The network resource has active dependencies. Remove them before deleting it.",
			false,
		)
	}
	return safePublicAPIError(err)
}
