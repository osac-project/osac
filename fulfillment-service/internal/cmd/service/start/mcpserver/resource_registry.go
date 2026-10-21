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
	"strings"

	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// resourceOperations contains the concrete API operations that back one MCP
// deployment resource type. Each entry is a typed adapter; it must never
// accept an arbitrary public method or serialize a resource directly when a
// narrower projection is required. A nil get marks a list-only resource.
type resourceOperations struct {
	label string
	list  func(context.Context, string, resourcePageRequest) (ListResourcesOutput, error)
	get   func(context.Context, string) (ResourceSummary, error)
}

type resourceRegistry map[ResourceType]resourceOperations

type resourceListResponse[Item any] interface {
	GetSize() int32
	GetTotal() int32
	GetItems() []Item
}

type resourceGetResponse[Object proto.Message] interface {
	GetObject() Object
}

func (r resourceRegistry) resourceTypes() []ResourceType {
	result := make([]ResourceType, 0, len(r))
	for resourceType := range r {
		result = append(result, resourceType)
	}
	slices.Sort(result)
	return result
}

func (r resourceRegistry) listResourceTypes() []ResourceType {
	result := make([]ResourceType, 0, len(r))
	for resourceType, operations := range r {
		if operations.list != nil {
			result = append(result, resourceType)
		}
	}
	slices.Sort(result)
	return result
}

func (r resourceRegistry) getResourceTypes() []ResourceType {
	result := make([]ResourceType, 0, len(r))
	for resourceType, operations := range r {
		if operations.get != nil {
			result = append(result, resourceType)
		}
	}
	slices.Sort(result)
	return result
}

func projectResourceOperations(deps ServerDeps) resourceOperations {
	list := func(ctx context.Context, request *publicv1.ProjectsListRequest) (*publicv1.ProjectsListResponse, error) {
		return deps.ProjectsClient.List(ctx, request)
	}
	get := func(ctx context.Context, request *publicv1.ProjectsGetRequest) (*publicv1.ProjectsGetResponse, error) {
		return deps.ProjectsClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "project",
		list:  typedResourceList("projects", list, projectListRequest, projectSummary),
		get:   typedResourceGet("project", get, projectGetRequest, projectSummary),
	}
}

func secretReferenceResourceOperations(deps ServerDeps) resourceOperations {
	list := func(ctx context.Context, request *publicv1.SecretsListRequest) (*publicv1.SecretsListResponse, error) {
		return deps.SecretsClient.List(ctx, request)
	}
	return resourceOperations{
		label: "secret reference",
		list:  typedResourceList("secret references", list, secretListRequest, secretSummary),
	}
}

func typedResourceList[
	Request any,
	Response resourceListResponse[Item],
	Item any,
](
	label string,
	list func(context.Context, *Request) (Response, error),
	buildRequest func(string, resourcePageRequest) *Request,
	summary func(Item) ResourceSummary,
) func(context.Context, string, resourcePageRequest) (ListResourcesOutput, error) {
	return func(ctx context.Context, filter string, page resourcePageRequest) (ListResourcesOutput, error) {
		response, err := list(ctx, buildRequest(filter, page))
		if err != nil {
			return ListResourcesOutput{}, fmt.Errorf("failed to list %s: %w", label, err)
		}
		items := summaries(response.GetItems(), summary)
		return resourceListOutput(page, response.GetSize(), response.GetTotal(), items), nil
	}
}

func typedResourceGet[
	Request any,
	Response resourceGetResponse[Object],
	Object proto.Message,
](
	label string,
	get func(context.Context, *Request) (Response, error),
	buildRequest func(string) *Request,
	summarize func(Object) ResourceSummary,
) func(context.Context, string) (ResourceSummary, error) {
	return func(ctx context.Context, id string) (ResourceSummary, error) {
		response, err := get(ctx, buildRequest(id))
		if err != nil {
			return ResourceSummary{}, fmt.Errorf("failed to get %s %q: %w", label, id, err)
		}
		return summarize(response.GetObject()), nil
	}
}

func projectListRequest(filter string, page resourcePageRequest) *publicv1.ProjectsListRequest {
	request := publicv1.ProjectsListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func projectGetRequest(id string) *publicv1.ProjectsGetRequest {
	return publicv1.ProjectsGetRequest_builder{Id: id}.Build()
}

func projectSummary(item *publicv1.Project) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func secretListRequest(filter string, page resourcePageRequest) *publicv1.SecretsListRequest {
	request := publicv1.SecretsListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func secretSummary(item *publicv1.Secret) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		Type:    secretReferenceType(item.GetType()),
	}
}

func computeInstanceCatalogItemResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest,
	) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
		return deps.ComputeInstanceCatalogItemsClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsGetRequest,
	) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
		return deps.ComputeInstanceCatalogItemsClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "compute instance catalog item",
		list: typedResourceList(
			"compute instance catalog items", list, computeInstanceCatalogItemListRequest, computeInstanceCatalogItemSummary,
		),
		get: typedResourceGet(
			"compute instance catalog item", get, computeInstanceCatalogItemGetRequest,
			computeInstanceCatalogItemSummary,
		),
	}
}

func computeInstanceCatalogItemListRequest(
	filter string, page resourcePageRequest,
) *publicv1.ComputeInstanceCatalogItemsListRequest {
	request := publicv1.ComputeInstanceCatalogItemsListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func computeInstanceCatalogItemGetRequest(id string) *publicv1.ComputeInstanceCatalogItemsGetRequest {
	return publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: id}.Build()
}

func computeInstanceCatalogItemSummary(item *publicv1.ComputeInstanceCatalogItem) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   catalogItemState(item.GetPublished()),
	}
}

func computeInstanceTemplateResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.ComputeInstanceTemplatesListRequest,
	) (*publicv1.ComputeInstanceTemplatesListResponse, error) {
		return deps.ComputeInstanceTemplatesClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.ComputeInstanceTemplatesGetRequest,
	) (*publicv1.ComputeInstanceTemplatesGetResponse, error) {
		return deps.ComputeInstanceTemplatesClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "compute instance template",
		list: typedResourceList(
			"compute instance templates", list, computeInstanceTemplateListRequest, computeInstanceTemplateSummary,
		),
		get: typedResourceGet(
			"compute instance template", get, computeInstanceTemplateGetRequest,
			computeInstanceTemplateSummary,
		),
	}
}

func computeInstanceTemplateListRequest(
	filter string, page resourcePageRequest,
) *publicv1.ComputeInstanceTemplatesListRequest {
	request := publicv1.ComputeInstanceTemplatesListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func computeInstanceTemplateGetRequest(id string) *publicv1.ComputeInstanceTemplatesGetRequest {
	return publicv1.ComputeInstanceTemplatesGetRequest_builder{Id: id}.Build()
}

func computeInstanceTemplateSummary(item *publicv1.ComputeInstanceTemplate) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
	}
}

func instanceTypeResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.InstanceTypesListRequest,
	) (*publicv1.InstanceTypesListResponse, error) {
		return deps.InstanceTypesClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.InstanceTypesGetRequest,
	) (*publicv1.InstanceTypesGetResponse, error) {
		return deps.InstanceTypesClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "instance type",
		list:  typedResourceList("instance types", list, instanceTypeListRequest, instanceTypeSummary),
		get:   typedResourceGet("instance type", get, instanceTypeGetRequest, instanceTypeSummary),
	}
}

func instanceTypeListRequest(filter string, page resourcePageRequest) *publicv1.InstanceTypesListRequest {
	request := publicv1.InstanceTypesListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func instanceTypeGetRequest(id string) *publicv1.InstanceTypesGetRequest {
	return publicv1.InstanceTypesGetRequest_builder{Id: id}.Build()
}

func instanceTypeSummary(item *publicv1.InstanceType) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetSpec().GetState().String(),
	}
}

func diskImageResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.DiskImagesListRequest,
	) (*publicv1.DiskImagesListResponse, error) {
		return deps.DiskImagesClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.DiskImagesGetRequest,
	) (*publicv1.DiskImagesGetResponse, error) {
		return deps.DiskImagesClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "disk image",
		list:  typedResourceList("disk images", list, diskImageListRequest, diskImageSummary),
		get:   typedResourceGet("disk image", get, diskImageGetRequest, diskImageSummary),
	}
}

func diskImageListRequest(filter string, page resourcePageRequest) *publicv1.DiskImagesListRequest {
	request := publicv1.DiskImagesListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func diskImageGetRequest(id string) *publicv1.DiskImagesGetRequest {
	return publicv1.DiskImagesGetRequest_builder{Id: id}.Build()
}

func diskImageSummary(item *publicv1.DiskImage) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetSpec().GetLifecycle().String(),
	}
}

func storageTierResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.StorageTiersListRequest,
	) (*publicv1.StorageTiersListResponse, error) {
		return deps.StorageTiersClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.StorageTiersGetRequest,
	) (*publicv1.StorageTiersGetResponse, error) {
		return deps.StorageTiersClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "storage tier",
		list:  typedResourceList("storage tiers", list, storageTierListRequest, storageTierSummary),
		get:   typedResourceGet("storage tier", get, storageTierGetRequest, storageTierSummary),
	}
}

func storageTierListRequest(filter string, page resourcePageRequest) *publicv1.StorageTiersListRequest {
	request := publicv1.StorageTiersListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func storageTierGetRequest(id string) *publicv1.StorageTiersGetRequest {
	return publicv1.StorageTiersGetRequest_builder{Id: id}.Build()
}

func storageTierSummary(item *publicv1.StorageTier) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func virtualNetworkResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.VirtualNetworksListRequest,
	) (*publicv1.VirtualNetworksListResponse, error) {
		return deps.VirtualNetworksClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.VirtualNetworksGetRequest,
	) (*publicv1.VirtualNetworksGetResponse, error) {
		return deps.VirtualNetworksClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "virtual network",
		list:  typedResourceList("virtual networks", list, virtualNetworkListRequest, virtualNetworkSummary),
		get:   typedResourceGet("virtual network", get, virtualNetworkGetRequest, virtualNetworkSummary),
	}
}

func virtualNetworkListRequest(filter string, page resourcePageRequest) *publicv1.VirtualNetworksListRequest {
	request := publicv1.VirtualNetworksListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func virtualNetworkGetRequest(id string) *publicv1.VirtualNetworksGetRequest {
	return publicv1.VirtualNetworksGetRequest_builder{Id: id}.Build()
}

func virtualNetworkSummary(item *publicv1.VirtualNetwork) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func subnetResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.SubnetsListRequest,
	) (*publicv1.SubnetsListResponse, error) {
		return deps.SubnetsClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.SubnetsGetRequest,
	) (*publicv1.SubnetsGetResponse, error) {
		return deps.SubnetsClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "subnet",
		list:  typedResourceList("subnets", list, subnetListRequest, subnetSummary),
		get:   typedResourceGet("subnet", get, subnetGetRequest, subnetSummary),
	}
}

func subnetListRequest(filter string, page resourcePageRequest) *publicv1.SubnetsListRequest {
	request := publicv1.SubnetsListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func subnetGetRequest(id string) *publicv1.SubnetsGetRequest {
	return publicv1.SubnetsGetRequest_builder{Id: id}.Build()
}

func subnetSummary(item *publicv1.Subnet) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func securityGroupResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.SecurityGroupsListRequest,
	) (*publicv1.SecurityGroupsListResponse, error) {
		return deps.SecurityGroupsClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.SecurityGroupsGetRequest,
	) (*publicv1.SecurityGroupsGetResponse, error) {
		return deps.SecurityGroupsClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "security group",
		list:  typedResourceList("security groups", list, securityGroupListRequest, securityGroupSummary),
		get:   typedResourceGet("security group", get, securityGroupGetRequest, securityGroupSummary),
	}
}

func securityGroupListRequest(filter string, page resourcePageRequest) *publicv1.SecurityGroupsListRequest {
	request := publicv1.SecurityGroupsListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func securityGroupGetRequest(id string) *publicv1.SecurityGroupsGetRequest {
	return publicv1.SecurityGroupsGetRequest_builder{Id: id}.Build()
}

func securityGroupSummary(item *publicv1.SecurityGroup) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func computeInstanceResourceOperations(deps ServerDeps) resourceOperations {
	list := func(
		ctx context.Context, request *publicv1.ComputeInstancesListRequest,
	) (*publicv1.ComputeInstancesListResponse, error) {
		return deps.ComputeInstancesClient.List(ctx, request)
	}
	get := func(
		ctx context.Context, request *publicv1.ComputeInstancesGetRequest,
	) (*publicv1.ComputeInstancesGetResponse, error) {
		return deps.ComputeInstancesClient.Get(ctx, request)
	}
	return resourceOperations{
		label: "compute instance",
		list:  typedResourceList("compute instances", list, computeInstanceListRequest, computeInstanceSummary),
		get:   typedResourceGet("compute instance", get, computeInstanceGetRequest, computeInstanceSummary),
	}
}

func computeInstanceListRequest(filter string, page resourcePageRequest) *publicv1.ComputeInstancesListRequest {
	request := publicv1.ComputeInstancesListRequest_builder{
		Offset: &page.offset,
		Limit:  &page.limit,
	}
	if filter != "" {
		request.Filter = &filter
	}
	return request.Build()
}

func computeInstanceGetRequest(id string) *publicv1.ComputeInstancesGetRequest {
	return publicv1.ComputeInstancesGetRequest_builder{Id: id}.Build()
}

func computeInstanceSummary(item *publicv1.ComputeInstance) ResourceSummary {
	return ResourceSummary{
		ID:      item.GetId(),
		Name:    item.GetMetadata().GetName(),
		Version: item.GetMetadata().GetVersion(),
		State:   item.GetStatus().GetState().String(),
	}
}

func catalogItemState(published bool) string {
	if published {
		return "published"
	}
	return "unpublished"
}

func newResourceRegistry(deps ServerDeps) resourceRegistry {
	return resourceRegistry{
		ResourceTypeProject:                    projectResourceOperations(deps),
		ResourceTypeSecretReference:            secretReferenceResourceOperations(deps),
		ResourceTypeComputeInstanceCatalogItem: computeInstanceCatalogItemResourceOperations(deps),
		ResourceTypeComputeInstanceTemplate:    computeInstanceTemplateResourceOperations(deps),
		ResourceTypeInstanceType:               instanceTypeResourceOperations(deps),
		ResourceTypeDiskImage:                  diskImageResourceOperations(deps),
		ResourceTypeStorageTier:                storageTierResourceOperations(deps),
		ResourceTypeVirtualNetwork:             virtualNetworkResourceOperations(deps),
		ResourceTypeSubnet:                     subnetResourceOperations(deps),
		ResourceTypeSecurityGroup:              securityGroupResourceOperations(deps),
		ResourceTypeComputeInstance:            computeInstanceResourceOperations(deps),
	}
}

func secretReferenceType(secretType publicv1.SecretType) string {
	return strings.ToLower(strings.TrimPrefix(secretType.String(), "SECRET_TYPE_"))
}

func summaries[T any](items []T, summarize func(T) ResourceSummary) []ResourceSummary {
	result := make([]ResourceSummary, len(items))
	for i, item := range items {
		result[i] = summarize(item)
	}
	return result
}
