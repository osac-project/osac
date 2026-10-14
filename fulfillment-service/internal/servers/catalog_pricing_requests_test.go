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
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestComputeCatalogPricingRequestUsesLockedAndDefaultValues(t *testing.T) {
	image := publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build()
	instanceType := publicv1.InstanceTypeReference_builder{Name: "small"}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{Locked: image}.Build(),
		InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{Editable: publicv1.EditableInstanceTypeReferenceField_builder{
			DefaultValue: instanceType,
		}.Build()}.Build(),
	}.Build()
	item := publicv1.ComputeInstanceCatalogItem_builder{Title: "Small Fedora VM", Fields: fields}.Build()

	got := computeCatalogPricingRequests([]*publicv1.ComputeInstanceCatalogItem{item})
	if len(got) != 1 || got[0].Service != billing.ServiceComputeInstance || !got[0].Supported {
		t.Fatalf("computeCatalogPricingRequests() = %v, want one supported compute request", got)
	}
	if got[0].Dimensions["image_ref"] != "fedora-16" || got[0].Dimensions["instance_type"] != "small" {
		t.Fatalf("pricing dimensions = %v, want locked image and default instance type", got[0].Dimensions)
	}
	if got[0].Description != "Small Fedora VM" || !got[0].PriceMayVary {
		t.Fatalf("pricing request = %+v, want catalog title and variable price", got[0])
	}
}

func TestComputeCatalogPricingRequestSeparatesSingleAdditionalDisk(t *testing.T) {
	size := int32(50)
	disk := publicv1.ComputeInstanceDisk_builder{SizeGib: &size,
		StorageTier: publicv1.StorageTierReference_builder{Name: "premium"}.Build()}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{Locked: publicv1.InstanceTypeReference_builder{Name: "large"}.Build()}.Build(),
		AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{Locked: publicv1.ComputeInstanceDiskList_builder{
			Items: []*publicv1.ComputeInstanceDisk{disk},
		}.Build()}.Build(),
	}.Build()
	item := publicv1.ComputeInstanceCatalogItem_builder{Fields: fields}.Build()
	requests := computeCatalogPricingRequests([]*publicv1.ComputeInstanceCatalogItem{item})
	components := catalogPricingComponents(requests[0])
	if len(components) != 2 || components[0].Dimensions["instance_type"] != "large" || components[0].Dimensions["additional_disk_size_gib"] != "" ||
		components[1].Dimensions["instance_type"] != "" || components[1].Dimensions["additional_disk_size_gib"] != "50" || components[1].Dimensions["additional_storage_tier"] != "premium" {
		t.Fatalf("catalog components = %+v, want separate VM and disk", components)
	}
}

func TestBareMetalCatalogPricingRequestUsesCatalogAndResourceValues(t *testing.T) {
	instanceType := publicv1.BareMetalInstanceTypeReference_builder{Name: "gpu-node"}.Build()
	image := publicv1.DiskImageReference_builder{Name: "rhel-9.6"}.Build()
	fields := publicv1.BareMetalInstanceCatalogItemFields_builder{
		InstanceType: publicv1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Locked: instanceType}.Build(),
		DiskImage:    publicv1.DiskImageReferenceFieldPolicy_builder{Locked: image}.Build(),
	}.Build()
	metadata := publicv1.Metadata_builder{Name: "gpu"}.Build()
	item := publicv1.BareMetalInstanceCatalogItem_builder{Metadata: metadata, Title: "GPU node", Fields: fields}.Build()

	got := bareMetalCatalogPricingRequests([]*publicv1.BareMetalInstanceCatalogItem{item})
	if len(got) != 1 || got[0].Service != billing.ServiceBareMetalInstance || !got[0].Supported {
		t.Fatalf("bareMetalCatalogPricingRequests() = %v, want one supported bare-metal request", got)
	}
	if got[0].Dimensions["catalog_item"] != "gpu" || got[0].Dimensions["bm_instance_type"] != "gpu-node" || got[0].Dimensions["image_ref"] != "rhel-9.6" {
		t.Fatalf("pricing dimensions = %v, want catalog item, instance type, and image", got[0].Dimensions)
	}
}

func TestClusterCatalogPricingRequestUsesVersionAndSingleNodeSet(t *testing.T) {
	version := publicv1.ClusterVersionReference_builder{Name: "openshift-4.18"}.Build()
	instanceType := publicv1.BareMetalInstanceTypeReference_builder{Name: "gpu-node"}.Build()
	nodeSet := publicv1.ClusterCatalogNodeSet_builder{Size: 3, BaremetalInstanceType: instanceType}.Build()
	fields := publicv1.ClusterCatalogItemFields_builder{
		Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
		NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{
			Items: map[string]*publicv1.ClusterCatalogNodeSet{"workers": nodeSet},
		}.Build()}.Build(),
	}.Build()
	item := publicv1.ClusterCatalogItem_builder{Title: "GPU cluster", Fields: fields}.Build()

	got := clusterCatalogPricingRequests([]*publicv1.ClusterCatalogItem{item})
	if len(got) != 1 || got[0].Service != billing.ServiceCluster || !got[0].Supported {
		t.Fatalf("clusterCatalogPricingRequests() = %v, want one supported cluster request", got)
	}
	components := catalogPricingComponents(got[0])
	if len(components) != 2 || got[0].Dimensions["release_image"] != "openshift-4.18" || got[0].Dimensions["node_count"] != "" || components[1].Dimensions["baremetal_instance_type"] != "gpu-node" || components[1].Dimensions["node_count"] != "3" {
		t.Fatalf("pricing components = %v, want base version and one node set", components)
	}
}
