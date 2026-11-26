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
	NetworkResourceTypeVirtualNetwork       NetworkResourceType = "virtual_network"
	NetworkResourceTypeSubnet               NetworkResourceType = "subnet"
	NetworkResourceTypeSecurityGroup        NetworkResourceType = "security_group"
	NetworkResourceTypeExternalIP           NetworkResourceType = "external_ip"
	NetworkResourceTypeExternalIPAttachment NetworkResourceType = "external_ip_attachment"
	NetworkResourceTypeNATGateway           NetworkResourceType = "nat_gateway"
)

type NetworkClusterEndpoint string

const (
	NetworkClusterEndpointAPI     NetworkClusterEndpoint = "api"
	NetworkClusterEndpointIngress NetworkClusterEndpoint = "ingress"
)

const maxNetworkSecurityRules = 100

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
	ResourceType NetworkResourceType `json:"resource_type" jsonschema:"network resource type: virtual_network, subnet, security_group, external_ip, external_ip_attachment, or nat_gateway"`
	ID           string              `json:"id" jsonschema:"ID of the exact network resource to delete"`
}

type DeleteNetworkResourceOutput struct {
	ResourceType NetworkResourceType `json:"resource_type"`
	ID           string              `json:"id"`
}

// ExposeNetworkResourceInput creates one public network exposure. A
// non-empty ID is an edit request, which the tool rejects without dispatch.
type ExposeNetworkResourceInput struct {
	ID                  string                 `json:"id,omitempty" jsonschema:"optional existing resource ID; a non-empty value requests an unsupported in-place edit"`
	ResourceType        NetworkResourceType    `json:"resource_type" jsonschema:"exposure resource type: external_ip, external_ip_attachment, or nat_gateway"`
	Name                string                 `json:"name" jsonschema:"name for the new exposure resource"`
	ExternalIPPoolID    string                 `json:"external_ip_pool_id,omitempty" jsonschema:"provider-managed ExternalIPPool ID for external_ip"`
	ExternalIPID        string                 `json:"external_ip_id,omitempty" jsonschema:"ExternalIP ID for external_ip_attachment and nat_gateway"`
	VirtualNetworkID    string                 `json:"virtual_network_id,omitempty" jsonschema:"VirtualNetwork ID for nat_gateway"`
	ComputeInstanceID   string                 `json:"compute_instance_id,omitempty" jsonschema:"ComputeInstance target ID for external_ip_attachment"`
	ClusterID           string                 `json:"cluster_id,omitempty" jsonschema:"Cluster target ID for external_ip_attachment"`
	BareMetalInstanceID string                 `json:"baremetal_instance_id,omitempty" jsonschema:"BareMetalInstance target ID for external_ip_attachment"`
	ClusterEndpoint     NetworkClusterEndpoint `json:"cluster_endpoint,omitempty" jsonschema:"cluster endpoint for cluster attachments: api or ingress"`
}

type ExposeNetworkResourceOutput struct {
	ResourceType NetworkResourceType `json:"resource_type"`
	ID           string              `json:"id"`
	Version      int32               `json:"version"`
	State        string              `json:"state"`
}

type networkCreateRequest struct {
	resourceType     NetworkResourceType
	name             string
	virtualNetworkID string
	ipv4Cidr         string
	ingress          []*publicv1.SecurityRule
	egress           []*publicv1.SecurityRule
}

type networkExposeRequest struct {
	resourceType             NetworkResourceType
	name                     string
	externalIPPoolID         string
	externalIPID             string
	virtualNetworkID         string
	attachmentTargetKind     string
	attachmentTargetID       string
	attachmentTargetEndpoint publicv1.ExternalIPAttachmentEndpoint
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

func handleExposeNetworkResource(
	externalIPs publicv1.ExternalIPsClient,
	externalIPAttachments publicv1.ExternalIPAttachmentsClient,
	natGateways publicv1.NATGatewaysClient,
) mcp.ToolHandlerFor[ExposeNetworkResourceInput, ExposeNetworkResourceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input ExposeNetworkResourceInput,
	) (*mcp.CallToolResult, ExposeNetworkResourceOutput, error) {
		var err error
		input, err = normalizeExposeNetworkResourceInput(input)
		if err != nil {
			return nil, ExposeNetworkResourceOutput{}, safeInvalidRequestError(err)
		}
		if input.ID != "" {
			return nil, ExposeNetworkResourceOutput{}, safeUnsupportedInPlaceNetworkEditError()
		}
		request, err := networkExposeRequestFromInput(input)
		if err != nil {
			return nil, ExposeNetworkResourceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, ExposeNetworkResourceOutput{}, err
		}
		switch request.resourceType {
		case NetworkResourceTypeExternalIP:
			response, err := externalIPs.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec: publicv1.ExternalIPSpec_builder{
						Pool: publicv1.ExternalIPPoolReference_builder{
							Id: request.externalIPPoolID,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, ExposeNetworkResourceOutput{}, safeNetworkExposeCreateError(request, err)
			}
			created := response.GetObject()
			return nil, networkExposeOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		case NetworkResourceTypeExternalIPAttachment:
			spec := publicv1.ExternalIPAttachmentSpec_builder{
				ExternalIp: publicv1.ExternalIPLocalReference_builder{
					Id: request.externalIPID,
				}.Build(),
				TargetEndpoint: request.attachmentTargetEndpoint,
			}
			switch request.attachmentTargetKind {
			case "ComputeInstance":
				spec.ComputeInstance = publicv1.ComputeInstanceLocalReference_builder{
					Id: request.attachmentTargetID,
				}.Build()
			case "Cluster":
				spec.Cluster = publicv1.ClusterLocalReference_builder{
					Id: request.attachmentTargetID,
				}.Build()
			case "BareMetalInstance":
				spec.BaremetalInstance = publicv1.BareMetalInstanceLocalReference_builder{
					Id: request.attachmentTargetID,
				}.Build()
			}
			response, err := externalIPAttachments.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
				Object: publicv1.ExternalIPAttachment_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec:     spec.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, ExposeNetworkResourceOutput{}, safeNetworkExposeCreateError(request, err)
			}
			created := response.GetObject()
			return nil, networkExposeOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		case NetworkResourceTypeNATGateway:
			response, err := natGateways.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
				Object: publicv1.NATGateway_builder{
					Metadata: publicv1.Metadata_builder{Name: request.name}.Build(),
					Spec: publicv1.NATGatewaySpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{
							Id: request.virtualNetworkID,
						}.Build(),
						ExternalIp: publicv1.ExternalIPLocalReference_builder{
							Id: request.externalIPID,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, ExposeNetworkResourceOutput{}, safeNetworkExposeCreateError(request, err)
			}
			created := response.GetObject()
			return nil, networkExposeOutput(request.resourceType, created.GetId(), created.GetMetadata(), created.GetStatus().GetState().String()), nil
		default:
			return nil, ExposeNetworkResourceOutput{}, safeInvalidRequestError(unsupportedExposeNetworkResourceTypeError(input.ResourceType))
		}
	}
}

func handleDeleteNetworkResource(
	virtualNetworks publicv1.VirtualNetworksClient,
	subnets publicv1.SubnetsClient,
	securityGroups publicv1.SecurityGroupsClient,
	externalIPs publicv1.ExternalIPsClient,
	externalIPAttachments publicv1.ExternalIPAttachmentsClient,
	natGateways publicv1.NATGatewaysClient,
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
		case NetworkResourceTypeExternalIP:
			_, err = externalIPs.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: input.ID}.Build())
		case NetworkResourceTypeExternalIPAttachment:
			_, err = externalIPAttachments.Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: input.ID,
			}.Build())
		case NetworkResourceTypeNATGateway:
			_, err = natGateways.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{Id: input.ID}.Build())
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
	case NetworkResourceTypeVirtualNetwork, NetworkResourceTypeSubnet, NetworkResourceTypeSecurityGroup,
		NetworkResourceTypeExternalIP, NetworkResourceTypeExternalIPAttachment, NetworkResourceTypeNATGateway:
	default:
		return DeleteNetworkResourceInput{}, unsupportedDeleteNetworkResourceTypeError(input.ResourceType)
	}
	if input.ID == "" {
		return DeleteNetworkResourceInput{}, fmt.Errorf("id is required")
	}
	return input, nil
}

func normalizeExposeNetworkResourceInput(input ExposeNetworkResourceInput) (ExposeNetworkResourceInput, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.ResourceType = NetworkResourceType(strings.TrimSpace(string(input.ResourceType)))
	input.Name = strings.TrimSpace(input.Name)
	input.ExternalIPPoolID = strings.TrimSpace(input.ExternalIPPoolID)
	input.ExternalIPID = strings.TrimSpace(input.ExternalIPID)
	input.VirtualNetworkID = strings.TrimSpace(input.VirtualNetworkID)
	input.ComputeInstanceID = strings.TrimSpace(input.ComputeInstanceID)
	input.ClusterID = strings.TrimSpace(input.ClusterID)
	input.BareMetalInstanceID = strings.TrimSpace(input.BareMetalInstanceID)
	input.ClusterEndpoint = NetworkClusterEndpoint(strings.TrimSpace(string(input.ClusterEndpoint)))
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

func networkExposeRequestFromInput(input ExposeNetworkResourceInput) (networkExposeRequest, error) {
	request := networkExposeRequest{
		resourceType:     input.ResourceType,
		name:             input.Name,
		externalIPPoolID: input.ExternalIPPoolID,
		externalIPID:     input.ExternalIPID,
		virtualNetworkID: input.VirtualNetworkID,
	}
	if input.Name == "" {
		return networkExposeRequest{}, fmt.Errorf("name is required")
	}
	targetCount := 0
	if input.ComputeInstanceID != "" {
		targetCount++
		request.attachmentTargetKind = "ComputeInstance"
		request.attachmentTargetID = input.ComputeInstanceID
	}
	if input.ClusterID != "" {
		targetCount++
		request.attachmentTargetKind = "Cluster"
		request.attachmentTargetID = input.ClusterID
	}
	if input.BareMetalInstanceID != "" {
		targetCount++
		request.attachmentTargetKind = "BareMetalInstance"
		request.attachmentTargetID = input.BareMetalInstanceID
	}
	switch input.ResourceType {
	case NetworkResourceTypeExternalIP:
		if input.ExternalIPPoolID == "" {
			return networkExposeRequest{}, fmt.Errorf("external_ip_pool_id is required for external_ip")
		}
		if input.ExternalIPID != "" || input.VirtualNetworkID != "" || targetCount != 0 || input.ClusterEndpoint != "" {
			return networkExposeRequest{}, fmt.Errorf("only external_ip_pool_id is valid for external_ip")
		}
	case NetworkResourceTypeExternalIPAttachment:
		if input.ExternalIPID == "" {
			return networkExposeRequest{}, fmt.Errorf("external_ip_id is required for external_ip_attachment")
		}
		if targetCount != 1 {
			return networkExposeRequest{}, fmt.Errorf(
				"external_ip_attachment requires exactly one target: compute_instance_id, cluster_id, or baremetal_instance_id",
			)
		}
		if input.ExternalIPPoolID != "" || input.VirtualNetworkID != "" {
			return networkExposeRequest{}, fmt.Errorf(
				"external_ip_pool_id and virtual_network_id are not valid for external_ip_attachment",
			)
		}
		if input.ClusterEndpoint == "" && request.attachmentTargetKind == "Cluster" {
			return networkExposeRequest{}, fmt.Errorf("cluster_endpoint is required for cluster targets")
		}
		if input.ClusterEndpoint != "" && request.attachmentTargetKind != "Cluster" {
			return networkExposeRequest{}, fmt.Errorf("cluster_endpoint is valid only for cluster targets")
		}
		switch input.ClusterEndpoint {
		case "":
		case NetworkClusterEndpointAPI:
			request.attachmentTargetEndpoint = publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
		case NetworkClusterEndpointIngress:
			request.attachmentTargetEndpoint = publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS
		default:
			return networkExposeRequest{}, fmt.Errorf(
				"cluster_endpoint must be one of %s or %s",
				NetworkClusterEndpointAPI,
				NetworkClusterEndpointIngress,
			)
		}
	case NetworkResourceTypeNATGateway:
		if input.VirtualNetworkID == "" {
			return networkExposeRequest{}, fmt.Errorf("virtual_network_id is required for nat_gateway")
		}
		if input.ExternalIPID == "" {
			return networkExposeRequest{}, fmt.Errorf("external_ip_id is required for nat_gateway")
		}
		if input.ExternalIPPoolID != "" || targetCount != 0 || input.ClusterEndpoint != "" {
			return networkExposeRequest{}, fmt.Errorf(
				"external_ip_pool_id, attachment targets, and cluster_endpoint are not valid for nat_gateway",
			)
		}
	default:
		return networkExposeRequest{}, unsupportedExposeNetworkResourceTypeError(input.ResourceType)
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

func networkExposeOutput(
	resourceType NetworkResourceType, id string, metadata *publicv1.Metadata, state string,
) ExposeNetworkResourceOutput {
	return ExposeNetworkResourceOutput{
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

func unsupportedDeleteNetworkResourceTypeError(resourceType NetworkResourceType) error {
	return fmt.Errorf(
		"resource_type must be one of %s, %s, %s, %s, %s, or %s",
		NetworkResourceTypeVirtualNetwork,
		NetworkResourceTypeSubnet,
		NetworkResourceTypeSecurityGroup,
		NetworkResourceTypeExternalIP,
		NetworkResourceTypeExternalIPAttachment,
		NetworkResourceTypeNATGateway,
	)
}

func unsupportedExposeNetworkResourceTypeError(resourceType NetworkResourceType) error {
	return fmt.Errorf(
		"resource_type must be one of %s, %s, or %s",
		NetworkResourceTypeExternalIP,
		NetworkResourceTypeExternalIPAttachment,
		NetworkResourceTypeNATGateway,
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
			"FailedPrecondition",
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

func safeNetworkExposeCreateError(request networkExposeRequest, err error) error {
	if status.Code(err) == codes.InvalidArgument {
		message := status.Convert(err).Message()
		for _, dependency := range networkExposeDependencies(request) {
			if networkDependencyMissing(dependency.kind, dependency.id, message) {
				return safeMissingPrerequisiteError(dependency.kind, dependency.id)
			}
		}
	}
	return safeNetworkCreateError(err)
}

type networkDependency struct {
	kind string
	id   string
}

func networkExposeDependencies(request networkExposeRequest) []networkDependency {
	switch request.resourceType {
	case NetworkResourceTypeExternalIP:
		return []networkDependency{{kind: "ExternalIPPool", id: request.externalIPPoolID}}
	case NetworkResourceTypeExternalIPAttachment:
		return []networkDependency{
			{kind: "ExternalIP", id: request.externalIPID},
			{kind: request.attachmentTargetKind, id: request.attachmentTargetID},
		}
	case NetworkResourceTypeNATGateway:
		return []networkDependency{
			{kind: "VirtualNetwork", id: request.virtualNetworkID},
			{kind: "ExternalIP", id: request.externalIPID},
		}
	default:
		return nil
	}
}

func networkDependencyMissing(kind, id, message string) bool {
	switch kind {
	case "ExternalIPPool":
		return strings.Contains(message, fmt.Sprintf("pool '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("ExternalIPPool %q not found", id))
	case "VirtualNetwork":
		return strings.Contains(message, fmt.Sprintf("parent VirtualNetwork '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("VirtualNetwork '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("VirtualNetwork %q not found", id))
	case "ExternalIP":
		return strings.Contains(message, fmt.Sprintf("ExternalIP '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("ExternalIP %q not found", id))
	case "ComputeInstance":
		return strings.Contains(message, fmt.Sprintf("ComputeInstance '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("ComputeInstance %q not found", id))
	case "Cluster":
		return strings.Contains(message, fmt.Sprintf("Cluster '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("Cluster %q not found", id))
	case "BareMetalInstance":
		return strings.Contains(message, fmt.Sprintf("BareMetalInstance '%s' does not exist", id)) ||
			strings.Contains(message, fmt.Sprintf("BareMetalInstance %q not found", id))
	default:
		return strings.Contains(message, fmt.Sprintf("%s '%s' does not exist", kind, id))
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
			"FailedPrecondition",
			"The network resource has active dependencies. Remove them before deleting it.",
			false,
		)
	}
	return safePublicAPIError(err)
}
