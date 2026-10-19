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
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// resourceOperations contains the concrete API operations that back one MCP
// deployment resource type. The registry keeps list_resources and get_resource
// generic while retaining a narrow, explicit allowlist of public APIs.
type resourceOperations struct {
	label string
	list  func(context.Context, string, resourcePageRequest) (ListResourcesOutput, error)
	get   func(context.Context, string) (proto.Message, error)
}

type resourceRegistry map[ResourceType]resourceOperations

func (r resourceRegistry) resourceTypes() []ResourceType {
	result := make([]ResourceType, 0, len(r))
	for resourceType := range r {
		result = append(result, resourceType)
	}
	slices.Sort(result)
	return result
}

func newResourceRegistry(deps ServerDeps) resourceRegistry {
	return resourceRegistry{
		ResourceTypeComputeInstanceCatalogItem: {
			label: "compute instance catalog item",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.ComputeInstanceCatalogItemsListRequest_builder{
					Offset: &page.offset,
					Limit:  &page.limit,
				}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.ComputeInstanceCatalogItemsClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list compute instance catalog items: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.ComputeInstanceCatalogItem) ResourceSummary {
					return ResourceSummary{
						ID:          item.GetId(),
						Name:        item.GetMetadata().GetName(),
						Title:       item.GetTitle(),
						Description: item.GetDescription(),
					}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.ComputeInstanceCatalogItemsClient.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get compute instance catalog item %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeComputeInstanceTemplate: {
			label: "compute instance template",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.ComputeInstanceTemplatesListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.ComputeInstanceTemplatesClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list compute instance templates: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.ComputeInstanceTemplate) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), Title: item.GetTitle(), Description: item.GetDescription()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.ComputeInstanceTemplatesClient.Get(ctx, publicv1.ComputeInstanceTemplatesGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get compute instance template %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeInstanceType: {
			label: "instance type",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.InstanceTypesListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.InstanceTypesClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list instance types: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.InstanceType) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), Description: item.GetSpec().GetDescription(), State: item.GetSpec().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.InstanceTypesClient.Get(ctx, publicv1.InstanceTypesGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get instance type %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeDiskImage: {
			label: "disk image",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.DiskImagesListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.DiskImagesClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list disk images: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.DiskImage) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), State: item.GetSpec().GetLifecycle().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.DiskImagesClient.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get disk image %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeStorageTier: {
			label: "storage tier",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.StorageTiersListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.StorageTiersClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list storage tiers: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.StorageTier) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), Description: item.GetSpec().GetDescription(), State: item.GetStatus().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.StorageTiersClient.Get(ctx, publicv1.StorageTiersGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get storage tier %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeVirtualNetwork: {
			label: "virtual network",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.VirtualNetworksListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.VirtualNetworksClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list virtual networks: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.VirtualNetwork) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), State: item.GetStatus().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.VirtualNetworksClient.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get virtual network %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeSubnet: {
			label: "subnet",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.SubnetsListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.SubnetsClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list subnets: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.Subnet) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), State: item.GetStatus().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.SubnetsClient.Get(ctx, publicv1.SubnetsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get subnet %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeSecurityGroup: {
			label: "security group",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.SecurityGroupsListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.SecurityGroupsClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list security groups: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.SecurityGroup) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), State: item.GetStatus().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.SecurityGroupsClient.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get security group %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
		ResourceTypeComputeInstance: {
			label: "compute instance",
			list: func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
				request := publicv1.ComputeInstancesListRequest_builder{Offset: &page.offset, Limit: &page.limit}
				if filter != "" {
					request.Filter = &filter
				}
				response, err := deps.ComputeInstancesClient.List(ctx, request.Build())
				if err != nil {
					return ListResourcesOutput{}, fmt.Errorf("failed to list compute instances: %w", err)
				}
				items := summaries(response.GetItems(), func(item *publicv1.ComputeInstance) ResourceSummary {
					return ResourceSummary{ID: item.GetId(), Name: item.GetMetadata().GetName(), State: item.GetStatus().GetState().String()}
				})
				return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
			},
			get: func(ctx context.Context, id string) (proto.Message, error) {
				response, err := deps.ComputeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				if err != nil {
					return nil, fmt.Errorf("failed to get compute instance %q: %w", id, err)
				}
				return response.GetObject(), nil
			},
		},
	}
}

func summaries[T any](items []T, summarize func(T) ResourceSummary) []ResourceSummary {
	result := make([]ResourceSummary, len(items))
	for i, item := range items {
		result[i] = summarize(item)
	}
	return result
}
