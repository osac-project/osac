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
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ResourceType string

const (
	ResourceTypeComputeInstanceCatalogItem ResourceType = "compute_instance_catalog_item"
	ResourceTypeComputeInstanceTemplate    ResourceType = "compute_instance_template"
	ResourceTypeInstanceType               ResourceType = "instance_type"
	ResourceTypeDiskImage                  ResourceType = "disk_image"
	ResourceTypeStorageTier                ResourceType = "storage_tier"
	ResourceTypeProject                    ResourceType = "project"
	ResourceTypeVirtualNetwork             ResourceType = "virtual_network"
	ResourceTypeSubnet                     ResourceType = "subnet"
	ResourceTypeSecurityGroup              ResourceType = "security_group"
	ResourceTypeComputeInstance            ResourceType = "compute_instance"
	ResourceTypeSecretReference            ResourceType = "secret_reference"
)

type ListResourcesInput struct {
	ResourceType ResourceType `json:"resource_type" jsonschema:"supported OSAC deployment resource type"`
	Filter       string       `json:"filter,omitempty" jsonschema:"optional CEL filter expression to narrow results"`
	Offset       int32        `json:"offset,omitempty" jsonschema:"zero-based result offset"`
	PageSize     int32        `json:"page_size,omitempty" jsonschema:"maximum result count from 1 through 100"`
}

type ResourceSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version int32  `json:"version"`
	State   string `json:"state,omitempty"`
	Type    string `json:"type,omitempty"`
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
			return nil, ListResourcesOutput{}, safeInvalidRequestError(err)
		}
		operations, ok := resources[input.ResourceType]
		if !ok {
			return nil, ListResourcesOutput{}, safeInvalidRequestError(unsupportedResourceTypeError(input.ResourceType))
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, ListResourcesOutput{}, err
		}
		output, err := operations.list(ctx, input.Filter, page)
		if err != nil {
			return nil, ListResourcesOutput{}, safePublicAPIError(err)
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
			return nil, GetResourceOutput{}, safeInvalidRequestError(unsupportedResourceTypeError(input.ResourceType))
		}
		if strings.TrimSpace(input.ID) == "" {
			return nil, GetResourceOutput{}, safeInvalidRequestError(errors.New("id must not be empty"))
		}
		if operations.get == nil {
			return nil, GetResourceOutput{}, safeInvalidRequestError(fmt.Errorf(
				"get_resource does not support %s", input.ResourceType,
			))
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, GetResourceOutput{}, err
		}
		summary, err := operations.get(ctx, input.ID)
		if err != nil {
			return nil, GetResourceOutput{}, safePublicAPIError(err)
		}
		resource, err := summaryToMap(summary)
		if err != nil {
			return nil, GetResourceOutput{}, safePublicAPIError(fmt.Errorf("failed to encode %s: %w", operations.label, err))
		}
		return nil, GetResourceOutput{Resource: resource}, nil
	}
}

func handleGetResourceTool(resources resourceRegistry) mcp.ToolHandler {
	typedHandler := handleGetResource(resources)
	inputSchema := resolveResourceToolSchema[GetResourceInput](resources.resourceTypes())
	outputSchema := resolveToolSchema[GetResourceOutput]()

	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		arguments := req.Params.Arguments
		if len(arguments) == 0 {
			arguments = []byte("{}")
		}
		var document any
		if err := json.Unmarshal(arguments, &document); err != nil {
			return invalidArgumentsResult(), nil
		}
		if err := inputSchema.Validate(document); err != nil {
			return invalidArgumentsResult(), nil
		}
		var input GetResourceInput
		if err := json.Unmarshal(arguments, &input); err != nil {
			return invalidArgumentsResult(), nil
		}

		result, output, err := typedHandler(ctx, req, input)
		if err != nil {
			if result == nil {
				result = &mcp.CallToolResult{}
			}
			result.SetError(err)
			return result, nil
		}
		if result == nil {
			result = &mcp.CallToolResult{}
		}
		encodedOutput, err := json.Marshal(output)
		if err != nil {
			return internalOutputResult(err), nil
		}
		var outputDocument any
		if err := json.Unmarshal(encodedOutput, &outputDocument); err != nil {
			return internalOutputResult(err), nil
		}
		if err := outputSchema.Validate(outputDocument); err != nil {
			return internalOutputResult(err), nil
		}
		result.StructuredContent = outputDocument
		if result.Content == nil {
			result.Content = []mcp.Content{&mcp.TextContent{Text: string(encodedOutput)}}
		}
		return result, nil
	}
}

func invalidArgumentsResult() *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(safeInvalidRequestError(errors.New("arguments do not match the get_resource schema")))
	return result
}

func internalOutputResult(err error) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(safePublicAPIError(fmt.Errorf("failed to encode get_resource response: %w", err)))
	return result
}

func resolveResourceToolSchema[T any](resourceTypes []ResourceType) *jsonschema.Resolved {
	var schema jsonschema.Schema
	if err := json.Unmarshal(resourceToolSchema[T](resourceTypes), &schema); err != nil {
		panic(fmt.Errorf("decode resource tool schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("resolve resource tool schema: %w", err))
	}
	return resolved
}

func resolveToolSchema[T any]() *jsonschema.Resolved {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Errorf("infer tool schema: %w", err))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(fmt.Errorf("resolve tool schema: %w", err))
	}
	return resolved
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

func summaryToMap(summary ResourceSummary) (map[string]any, error) {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}
