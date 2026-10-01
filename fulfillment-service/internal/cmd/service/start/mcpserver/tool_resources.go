/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type ResourceType string

const (
	ResourceTypeComputeInstanceCatalogItem ResourceType = "compute_instance_catalog_item"
	ResourceTypeComputeInstanceTemplate    ResourceType = "compute_instance_template"
	ResourceTypeInstanceType               ResourceType = "instance_type"
	ResourceTypeDiskImage                  ResourceType = "disk_image"
	ResourceTypeStorageTier                ResourceType = "storage_tier"
	ResourceTypeVirtualNetwork             ResourceType = "virtual_network"
	ResourceTypeSubnet                     ResourceType = "subnet"
	ResourceTypeSecurityGroup              ResourceType = "security_group"
	ResourceTypeComputeInstance            ResourceType = "compute_instance"
)

type ListResourcesInput struct {
	ResourceType ResourceType `json:"resource_type" jsonschema:"supported OSAC deployment resource type"`
	Filter       string       `json:"filter,omitempty" jsonschema:"optional CEL filter expression to narrow results"`
	Offset       int32        `json:"offset,omitempty" jsonschema:"zero-based result offset"`
	PageSize     int32        `json:"page_size,omitempty" jsonschema:"maximum result count from 1 through 100"`
}

type ResourceSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	State       string `json:"state,omitempty"`
}

type ListResourcesOutput struct {
	Offset int32             `json:"offset"`
	Size   int32             `json:"size"`
	Total  int32             `json:"total"`
	Items  []ResourceSummary `json:"items"`
}

type GetResourceInput struct {
	ResourceType ResourceType `json:"resource_type" jsonschema:"supported OSAC deployment resource type"`
	ID           string       `json:"id" jsonschema:"resource ID"`
}

type GetResourceOutput struct {
	Resource map[string]any `json:"resource"`
}

func handleListResources(resources resourceRegistry) mcp.ToolHandlerFor[ListResourcesInput, ListResourcesOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input ListResourcesInput,
	) (*mcp.CallToolResult, ListResourcesOutput, error) {
		page, err := resourcePage(input)
		if err != nil {
			return nil, ListResourcesOutput{}, err
		}
		operations, ok := resources[input.ResourceType]
		if !ok {
			return nil, ListResourcesOutput{}, unsupportedResourceTypeError(input.ResourceType)
		}
		output, err := operations.list(forwardToken(ctx, req), input.Filter, page)
		if err != nil {
			return nil, ListResourcesOutput{}, err
		}
		return nil, output, nil
	}
}

func handleGetResource(resources resourceRegistry) mcp.ToolHandlerFor[GetResourceInput, GetResourceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input GetResourceInput,
	) (*mcp.CallToolResult, GetResourceOutput, error) {
		operations, ok := resources[input.ResourceType]
		if !ok {
			return nil, GetResourceOutput{}, unsupportedResourceTypeError(input.ResourceType)
		}
		message, err := operations.get(forwardToken(ctx, req), input.ID)
		if err != nil {
			return nil, GetResourceOutput{}, err
		}
		resource, err := messageToMap(message)
		if err != nil {
			return nil, GetResourceOutput{}, fmt.Errorf("failed to encode %s: %w", operations.label, err)
		}
		return nil, GetResourceOutput{Resource: resource}, nil
	}
}

const (
	defaultResourcePageSize int32 = 50
	maxResourcePageSize     int32 = 100
)

type resourcePageRequest struct {
	offset int32
	limit  int32
}

func resourcePage(input ListResourcesInput) (resourcePageRequest, error) {
	if input.Offset < 0 {
		return resourcePageRequest{}, fmt.Errorf("offset must not be negative")
	}
	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = defaultResourcePageSize
	}
	if pageSize < 0 || pageSize > maxResourcePageSize {
		return resourcePageRequest{}, fmt.Errorf("page_size must be between 1 and %d", maxResourcePageSize)
	}
	return resourcePageRequest{offset: input.Offset, limit: pageSize}, nil
}

func resourceListOutput(
	page resourcePageRequest, size, total int32, items []ResourceSummary,
) ListResourcesOutput {
	return ListResourcesOutput{
		Offset: page.offset,
		Size:   size,
		Total:  total,
		Items:  items,
	}
}

func unsupportedResourceTypeError(resourceType ResourceType) error {
	return fmt.Errorf("unsupported resource type %q", resourceType)
}

func supportedResourceTypesDescription(supportedResourceTypes []ResourceType) string {
	resourceTypes := make([]string, len(supportedResourceTypes))
	for i, resourceType := range supportedResourceTypes {
		resourceTypes[i] = string(resourceType)
	}
	return strings.Join(resourceTypes, ", ")
}

func messageToMap(message proto.Message) (map[string]any, error) {
	if message == nil {
		return map[string]any{}, nil
	}
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}
