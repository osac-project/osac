/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"sort"
	"strconv"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func computeCatalogPricingRequests(items []*publicv1.ComputeInstanceCatalogItem) []CatalogPricingRequest {
	return computeCatalogPricingRequestsWithTemplateDefaults(items, nil)
}

func computeCatalogPricingRequestsWithTemplateDefaults(
	items []*publicv1.ComputeInstanceCatalogItem,
	templateDefaults map[string]map[string]string,
) []CatalogPricingRequest {
	requests := make([]CatalogPricingRequest, len(items))
	for i, item := range items {
		request := CatalogPricingRequest{
			Service:     billing.ServiceComputeInstance,
			Dimensions:  make(map[string]string),
			Description: catalogPriceDescription(item.GetTitle(), "Compute instance"),
			Supported:   true,
		}
		if fields := item.GetFields(); fields != nil {
			request.PriceMayVary = addDiskImagePricingDimension(request.Dimensions, "image_ref", fields.GetDiskImage()) || request.PriceMayVary
			request.PriceMayVary = addInstanceTypePricingDimension(request.Dimensions, "instance_type", fields.GetInstanceType()) || request.PriceMayVary
			if bootDisk := fields.GetBootDisk(); bootDisk != nil {
				request.PriceMayVary = addInt32PricingDimension(request.Dimensions, "boot_disk_size_gib", bootDisk.GetSizeGib()) || request.PriceMayVary
				request.PriceMayVary = addStorageTierPricingDimension(request.Dimensions, "storage_tier", bootDisk.GetStorageTier()) || request.PriceMayVary
			}
			request.PriceMayVary = addComputeAdditionalDiskPricingDimensions(&request, fields.GetAdditionalDisks()) || request.PriceMayVary
		}
		requests[i] = applyTemplatePricingDefaults(request, templateDefaults[item.GetId()])
	}
	return requests
}

func clusterCatalogPricingRequests(items []*publicv1.ClusterCatalogItem) []CatalogPricingRequest {
	return clusterCatalogPricingRequestsWithTemplateDefaults(items, nil)
}

func clusterCatalogPricingRequestsWithTemplateDefaults(
	items []*publicv1.ClusterCatalogItem,
	templateDefaults map[string]map[string]string,
) []CatalogPricingRequest {
	requests := make([]CatalogPricingRequest, len(items))
	for i, item := range items {
		request := CatalogPricingRequest{
			Service:     billing.ServiceCluster,
			Dimensions:  make(map[string]string),
			Description: catalogPriceDescription(item.GetTitle(), "Cluster"),
			Supported:   true,
		}
		if template := item.GetTemplate(); template != nil && template.GetName() != "" {
			request.Dimensions["cluster_template"] = template.GetName()
		}
		if fields := item.GetFields(); fields != nil {
			request.PriceMayVary = addClusterVersionPricingDimension(request.Dimensions, "release_image", fields.GetVersion())
			request.PriceMayVary = addClusterNodeSetPricingDimensions(&request, fields.GetNodeSets()) || request.PriceMayVary
		}
		requests[i] = applyTemplatePricingDefaults(request, templateDefaults[item.GetId()])
	}
	return requests
}

func bareMetalCatalogPricingRequests(items []*publicv1.BareMetalInstanceCatalogItem) []CatalogPricingRequest {
	return bareMetalCatalogPricingRequestsWithTemplateDefaults(items, nil)
}

func bareMetalCatalogPricingRequestsWithTemplateDefaults(
	items []*publicv1.BareMetalInstanceCatalogItem,
	templateDefaults map[string]map[string]string,
) []CatalogPricingRequest {
	requests := make([]CatalogPricingRequest, len(items))
	for i, item := range items {
		request := CatalogPricingRequest{
			Service:     billing.ServiceBareMetalInstance,
			Dimensions:  make(map[string]string),
			Description: catalogPriceDescription(item.GetTitle(), "Bare metal instance"),
			Supported:   true,
		}
		if metadata := item.GetMetadata(); metadata != nil && metadata.GetName() != "" {
			request.Dimensions["catalog_item"] = metadata.GetName()
		}
		if fields := item.GetFields(); fields != nil {
			request.PriceMayVary = addBareMetalInstanceTypePricingDimension(request.Dimensions, "bm_instance_type", fields.GetInstanceType())
			request.PriceMayVary = addDiskImagePricingDimension(request.Dimensions, "image_ref", fields.GetDiskImage()) || request.PriceMayVary
		}
		requests[i] = applyTemplatePricingDefaults(request, templateDefaults[item.GetId()])
	}
	return requests
}

// applyTemplatePricingDefaults fills billable dimensions omitted by the
// catalog item's field policies from its referenced template. Catalog policy
// values take precedence because provisioning applies them before template
// defaults.
func applyTemplatePricingDefaults(request CatalogPricingRequest, defaults map[string]string) CatalogPricingRequest {
	if request.Dimensions == nil {
		request.Dimensions = make(map[string]string)
	}
	for dimension, value := range defaults {
		if value == "" {
			continue
		}
		if request.Service == billing.ServiceComputeInstance && (dimension == "additional_disk_size_gib" || dimension == "additional_storage_tier") {
			continue
		}
		if request.Service == billing.ServiceCluster && (dimension == "baremetal_instance_type" || dimension == "node_count") {
			continue
		}
		if _, present := request.Dimensions[dimension]; !present {
			request.Dimensions[dimension] = value
		}
	}
	for i := range request.Components {
		if request.Components[i].Dimensions == nil {
			request.Components[i].Dimensions = make(map[string]string)
		}
		for dimension, value := range defaults {
			if value == "" {
				continue
			}
			if request.Service == billing.ServiceComputeInstance && dimension != "additional_disk_size_gib" && dimension != "additional_storage_tier" {
				continue
			}
			if request.Service == billing.ServiceCluster && dimension != "cluster_template" && dimension != "release_image" && dimension != "baremetal_instance_type" && dimension != "node_count" {
				continue
			}
			if _, present := request.Components[i].Dimensions[dimension]; !present {
				request.Components[i].Dimensions[dimension] = value
			}
		}
	}
	return request
}

func addDiskImagePricingDimension(dimensions map[string]string, dimension string, policy *publicv1.DiskImageReferenceFieldPolicy) bool {
	if policy == nil {
		return false
	}
	locked := policy.GetLocked()
	editable := policy.GetEditable()
	var defaultValue *publicv1.DiskImageReference
	if editable != nil {
		defaultValue = editable.GetDefaultValue()
	}
	return addReferencePricingDimension(dimensions, dimension, locked.GetName(), defaultValue.GetName(), editable != nil, defaultValue != nil)
}

func addInstanceTypePricingDimension(dimensions map[string]string, dimension string, policy *publicv1.InstanceTypeReferenceFieldPolicy) bool {
	if policy == nil {
		return false
	}
	locked := policy.GetLocked()
	editable := policy.GetEditable()
	var defaultValue *publicv1.InstanceTypeReference
	if editable != nil {
		defaultValue = editable.GetDefaultValue()
	}
	return addReferencePricingDimension(dimensions, dimension, locked.GetName(), defaultValue.GetName(), editable != nil, defaultValue != nil)
}

func addStorageTierPricingDimension(dimensions map[string]string, dimension string, policy *publicv1.StorageTierReferenceFieldPolicy) bool {
	if policy == nil {
		return false
	}
	locked := policy.GetLocked()
	editable := policy.GetEditable()
	var defaultValue *publicv1.StorageTierReference
	if editable != nil {
		defaultValue = editable.GetDefaultValue()
	}
	return addReferencePricingDimension(dimensions, dimension, locked.GetName(), defaultValue.GetName(), editable != nil, defaultValue != nil)
}

func addBareMetalInstanceTypePricingDimension(dimensions map[string]string, dimension string, policy *publicv1.BareMetalInstanceTypeReferenceFieldPolicy) bool {
	if policy == nil {
		return false
	}
	locked := policy.GetLocked()
	editable := policy.GetEditable()
	var defaultValue *publicv1.BareMetalInstanceTypeReference
	if editable != nil {
		defaultValue = editable.GetDefaultValue()
	}
	return addReferencePricingDimension(dimensions, dimension, locked.GetName(), defaultValue.GetName(), editable != nil, defaultValue != nil)
}

func addClusterVersionPricingDimension(dimensions map[string]string, dimension string, policy *publicv1.ClusterVersionReferenceFieldPolicy) bool {
	if policy == nil {
		return false
	}
	locked := policy.GetLocked()
	editable := policy.GetEditable()
	var defaultValue *publicv1.ClusterVersionReference
	if editable != nil {
		defaultValue = editable.GetDefaultValue()
	}
	return addReferencePricingDimension(dimensions, dimension, locked.GetName(), defaultValue.GetName(), editable != nil, defaultValue != nil)
}

func addReferencePricingDimension(dimensions map[string]string, dimension, locked, defaultValue string, editable, hasDefault bool) bool {
	if locked != "" {
		dimensions[dimension] = locked
		return false
	}
	if editable && hasDefault && defaultValue != "" {
		dimensions[dimension] = defaultValue
	}
	return editable
}

func addInt32PricingDimension(dimensions map[string]string, dimension string, policy *publicv1.Int32FieldPolicy) bool {
	if policy == nil {
		return false
	}
	if policy.HasLocked() {
		if policy.GetLocked() > 0 {
			dimensions[dimension] = strconv.FormatInt(int64(policy.GetLocked()), 10)
		}
		return false
	}
	if editable := policy.GetEditable(); editable != nil {
		if editable.HasDefaultValue() && editable.GetDefaultValue() > 0 {
			dimensions[dimension] = strconv.FormatInt(int64(editable.GetDefaultValue()), 10)
		}
		return true
	}
	return false
}

func addComputeAdditionalDiskPricingDimensions(request *CatalogPricingRequest, policy *publicv1.ComputeInstanceDiskListFieldPolicy) bool {
	if policy == nil {
		return false
	}
	var disks *publicv1.ComputeInstanceDiskList
	editable := policy.GetEditable()
	if locked := policy.GetLocked(); locked != nil {
		disks = locked
	} else if editable != nil {
		disks = editable.GetDefaultValue()
	}
	if disks == nil {
		return editable != nil
	}
	items := disks.GetItems()
	if len(items) == 0 {
		return editable != nil
	}
	request.Components = make([]billing.CatalogPricingComponentRequest, 0, len(items))
	for index, disk := range items {
		if disk == nil {
			request.Supported = false
			request.Components = nil
			return editable != nil
		}
		dimensions := make(map[string]string, 2)
		addComputeDiskDimensions(dimensions, disk)
		request.Components = append(request.Components, billing.CatalogPricingComponentRequest{
			Dimensions:  dimensions,
			Description: request.Description + " additional disk " + strconv.Itoa(index+1),
		})
	}
	return editable != nil
}

func addComputeDiskDimensions(dimensions map[string]string, disk *publicv1.ComputeInstanceDisk) {
	if disk.HasSizeGib() {
		dimensions["additional_disk_size_gib"] = strconv.FormatInt(int64(disk.GetSizeGib()), 10)
	}
	if tier := disk.GetStorageTier(); tier != nil && tier.GetName() != "" {
		dimensions["additional_storage_tier"] = tier.GetName()
	}
}

func addClusterNodeSetPricingDimensions(request *CatalogPricingRequest, policy *publicv1.ClusterNodeSetMapPolicy) bool {
	if policy == nil {
		return false
	}
	var nodeSets *publicv1.ClusterNodeSetMap
	editable := policy.GetEditable()
	if locked := policy.GetLocked(); locked != nil {
		nodeSets = locked
	} else if editable != nil {
		nodeSets = editable.GetDefaultValue()
	}
	if nodeSets == nil {
		request.Supported = false
		return editable != nil
	}
	items := nodeSets.GetItems()
	if len(items) == 0 {
		request.Supported = false
		return editable != nil
	}
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	request.Components = make([]billing.CatalogPricingComponentRequest, 0, len(names))
	for _, name := range names {
		nodeSet := items[name]
		if nodeSet == nil {
			request.Supported = false
			request.Components = nil
			return editable != nil
		}
		dimensions := cloneCatalogPricingDimensions(request.Dimensions)
		addClusterNodeSetDimensions(dimensions, nodeSet)
		request.Components = append(request.Components, billing.CatalogPricingComponentRequest{
			Dimensions:  dimensions,
			Description: request.Description + " " + name,
		})
	}
	return editable != nil
}

func addClusterNodeSetDimensions(dimensions map[string]string, nodeSet *publicv1.ClusterCatalogNodeSet) {
	if instanceType := nodeSet.GetBaremetalInstanceType(); instanceType != nil && instanceType.GetName() != "" {
		dimensions["baremetal_instance_type"] = instanceType.GetName()
	}
	if nodeSet.GetSize() > 0 {
		dimensions["node_count"] = strconv.FormatInt(int64(nodeSet.GetSize()), 10)
	}
}

func cloneCatalogPricingDimensions(dimensions map[string]string) map[string]string {
	cloned := make(map[string]string, len(dimensions))
	for key, value := range dimensions {
		cloned[key] = value
	}
	return cloned
}

func catalogPriceDescription(title, fallback string) string {
	if title != "" {
		return title
	}
	return fallback
}
