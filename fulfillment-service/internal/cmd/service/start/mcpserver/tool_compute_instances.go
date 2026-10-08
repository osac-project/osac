/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// CreateComputeInstanceInput creates a compute instance from a published catalog item.
// The catalog item is intentionally required in this MCP phase so the server can
// enforce its policy-controlled defaults and permitted overrides.
type CreateComputeInstanceInput struct {
	Name               string                                  `json:"name" jsonschema:"name for the new compute instance"`
	CatalogItem        string                                  `json:"catalog_item" jsonschema:"ID of the compute instance catalog item"`
	InstanceTypeID     string                                  `json:"instance_type_id,omitempty" jsonschema:"optional ID from list_resources(instance_type); the catalog item must permit the override"`
	BootDisk           *ComputeInstanceBootDiskInput           `json:"boot_disk,omitempty" jsonschema:"optional object with size_gib and/or storage_tier_id; example: {\"size_gib\":20,\"storage_tier_id\":\"<storage-tier-id>\"}; copy the ID from list_resources(storage_tier), not its name or a nested storageTier object; omit to use catalog defaults; the catalog item must permit each supplied field"`
	NetworkAttachments []ComputeInstanceNetworkAttachmentInput `json:"network_attachments,omitempty" jsonschema:"optional array of objects, maximum one attachment; example: [{\"subnet_id\":\"subnet-id\",\"security_group_ids\":[\"security-group-id\"]}]; omit to use the catalog or tenant default network"`
}

// ComputeInstanceBootDiskInput selects values already visible through
// list_resources. Omitted fields stay absent so the catalog item's defaults
// and locked values remain authoritative.
type ComputeInstanceBootDiskInput struct {
	SizeGiB       *int32 `json:"size_gib,omitempty" jsonschema:"optional boot disk size in GiB; must be greater than zero"`
	StorageTierID string `json:"storage_tier_id,omitempty" jsonschema:"optional ID from list_resources(storage_tier)"`
}

// ComputeInstanceNetworkAttachmentInput is one NIC. Subnet and security group
// IDs must come from the caller's tenant-visible list_resources results.
type ComputeInstanceNetworkAttachmentInput struct {
	SubnetID         string   `json:"subnet_id" jsonschema:"ID from list_resources(subnet)"`
	SecurityGroupIDs []string `json:"security_group_ids,omitempty" jsonschema:"optional IDs from list_resources(security_group)"`
}

type CreateComputeInstanceOutput struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type DeleteComputeInstanceInput struct {
	ID string `json:"id" jsonschema:"compute instance ID"`
}

type DeleteComputeInstanceOutput struct {
	ID string `json:"id"`
}

func handleCreateComputeInstance(
	client publicv1.ComputeInstancesClient,
) mcp.ToolHandlerFor[CreateComputeInstanceInput, CreateComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input CreateComputeInstanceInput,
	) (*mcp.CallToolResult, CreateComputeInstanceOutput, error) {
		spec, err := computeInstanceSpecFromInput(input)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		response, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: input.Name}.Build(),
				Spec:     spec,
			}.Build(),
		}.Build())
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safePublicAPIError(err)
		}
		created := response.GetObject()
		return nil, CreateComputeInstanceOutput{
			ID:    created.GetId(),
			State: created.GetStatus().GetState().String(),
		}, nil
	}
}

func computeInstanceSpecFromInput(input CreateComputeInstanceInput) (*publicv1.ComputeInstanceSpec, error) {
	builder := publicv1.ComputeInstanceSpec_builder{
		CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: input.CatalogItem}.Build(),
	}
	if instanceTypeID := strings.TrimSpace(input.InstanceTypeID); instanceTypeID != "" {
		builder.InstanceType = publicv1.InstanceTypeReference_builder{Id: instanceTypeID}.Build()
	}
	if input.BootDisk != nil {
		bootDisk, err := computeInstanceBootDiskFromInput(*input.BootDisk)
		if err != nil {
			return nil, err
		}
		builder.BootDisk = bootDisk
	}
	if input.NetworkAttachments != nil {
		attachments, err := computeInstanceNetworkAttachmentsFromInput(input.NetworkAttachments)
		if err != nil {
			return nil, err
		}
		builder.NetworkAttachments = attachments
	}
	return builder.Build(), nil
}

func computeInstanceBootDiskFromInput(input ComputeInstanceBootDiskInput) (*publicv1.ComputeInstanceDisk, error) {
	storageTierID := strings.TrimSpace(input.StorageTierID)
	if input.SizeGiB == nil && storageTierID == "" {
		return nil, fmt.Errorf("boot_disk must include size_gib and/or storage_tier_id")
	}
	if input.SizeGiB != nil && *input.SizeGiB <= 0 {
		return nil, fmt.Errorf("boot_disk.size_gib must be greater than zero")
	}
	builder := publicv1.ComputeInstanceDisk_builder{SizeGib: input.SizeGiB}
	if storageTierID != "" {
		builder.StorageTier = publicv1.StorageTierReference_builder{Id: storageTierID}.Build()
	}
	return builder.Build(), nil
}

func computeInstanceNetworkAttachmentsFromInput(input []ComputeInstanceNetworkAttachmentInput) ([]*publicv1.ComputeNetworkAttachment, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("network_attachments must contain at least one attachment when supplied")
	}
	if len(input) > 1 {
		return nil, fmt.Errorf("network_attachments must contain at most one attachment")
	}
	attachments := make([]*publicv1.ComputeNetworkAttachment, len(input))
	for i, attachment := range input {
		subnetID := strings.TrimSpace(attachment.SubnetID)
		if subnetID == "" {
			return nil, fmt.Errorf("network_attachments[%d].subnet_id is required", i)
		}
		securityGroups := make([]*publicv1.SecurityGroupLocalReference, len(attachment.SecurityGroupIDs))
		for j, securityGroupID := range attachment.SecurityGroupIDs {
			securityGroupID = strings.TrimSpace(securityGroupID)
			if securityGroupID == "" {
				return nil, fmt.Errorf("network_attachments[%d].security_group_ids[%d] is required", i, j)
			}
			securityGroups[j] = publicv1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build()
		}
		attachments[i] = publicv1.ComputeNetworkAttachment_builder{
			Subnet:         publicv1.SubnetLocalReference_builder{Id: subnetID}.Build(),
			SecurityGroups: securityGroups,
		}.Build()
	}
	return attachments, nil
}

func handleDeleteComputeInstance(
	client publicv1.ComputeInstancesClient,
) mcp.ToolHandlerFor[DeleteComputeInstanceInput, DeleteComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input DeleteComputeInstanceInput,
	) (*mcp.CallToolResult, DeleteComputeInstanceOutput, error) {
		var err error
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, DeleteComputeInstanceOutput{}, err
		}
		_, err = client.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: input.ID}.Build())
		if err != nil {
			return nil, DeleteComputeInstanceOutput{}, safePublicAPIError(err)
		}
		return nil, DeleteComputeInstanceOutput(input), nil
	}
}
