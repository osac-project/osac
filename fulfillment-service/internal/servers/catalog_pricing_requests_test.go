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
	"reflect"
	"testing"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
		BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
			SizeGib:     publicv1.Int32FieldPolicy_builder{Locked: testInt32Pointer(40)}.Build(),
			StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{Locked: publicv1.StorageTierReference_builder{Name: "premium"}.Build()}.Build(),
		}.Build(),
	}.Build()
	item := publicv1.ComputeInstanceCatalogItem_builder{Title: "Small Fedora VM", Fields: fields}.Build()

	got := computeCatalogPricingRequests([]*publicv1.ComputeInstanceCatalogItem{item})
	if len(got) != 1 || got[0].Service != billing.ServiceComputeInstance || !got[0].Supported {
		t.Fatalf("computeCatalogPricingRequests() = %v, want one supported compute request", got)
	}
	if want := map[string]string{"instance_type": "small"}; !reflect.DeepEqual(got[0].Dimensions, want) {
		t.Fatalf("pricing dimensions = %v, want %v", got[0].Dimensions, want)
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
	if len(components) != 2 || !reflect.DeepEqual(components[0].Dimensions, map[string]string{"instance_type": "large"}) ||
		!reflect.DeepEqual(components[1].Dimensions, map[string]string{"additional_disk_size_gib": "50", "additional_storage_tier": "premium"}) {
		t.Fatalf("catalog components = %+v, want separate VM and disk", components)
	}
}

func TestComputeCatalogPricingRequestUsesOnlyInstanceTypeFromTemplateDefaults(t *testing.T) {
	item := publicv1.ComputeInstanceCatalogItem_builder{Id: "vm-small"}.Build()
	defaults := map[string]map[string]string{"vm-small": {
		"instance_type": "small", "image_ref": "fedora-16", "boot_disk_size_gib": "40", "storage_tier": "premium",
	}}
	got := computeCatalogPricingRequestsWithTemplateDefaults([]*publicv1.ComputeInstanceCatalogItem{item}, defaults)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Dimensions, map[string]string{"instance_type": "small"}) {
		t.Fatalf("compute catalog pricing requests = %+v, want only template instance_type", got)
	}
}

func TestComputeInstanceTemplatePricingDimensionsUseOnlyInstanceType(t *testing.T) {
	template := privatev1.ComputeInstanceTemplate_builder{SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Name: "small"}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		BootDisk: privatev1.ComputeInstanceDisk_builder{
			SizeGib: testInt32Pointer(40), StorageTier: privatev1.StorageTierReference_builder{Name: "premium"}.Build(),
		}.Build(),
	}.Build()}.Build()
	if got, want := computeInstanceTemplatePricingDimensions(template), map[string]string{"instance_type": "small"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("template pricing dimensions = %v, want %v", got, want)
	}
}

func TestClusterCatalogPricingRequestUsesOnlyNodeSetBillableDimensions(t *testing.T) {
	version := publicv1.ClusterVersionReference_builder{Name: "openshift-4.18"}.Build()
	instanceType := publicv1.BareMetalInstanceTypeReference_builder{Name: "cpu-node"}.Build()
	gpuInstanceType := publicv1.BareMetalInstanceTypeReference_builder{Name: "gpu-node"}.Build()
	fields := publicv1.ClusterCatalogItemFields_builder{
		Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
		NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{
			Items: map[string]*publicv1.ClusterCatalogNodeSet{
				"compute": publicv1.ClusterCatalogNodeSet_builder{Size: 3, BaremetalInstanceType: instanceType}.Build(),
				"gpu":     publicv1.ClusterCatalogNodeSet_builder{Size: 2, BaremetalInstanceType: gpuInstanceType}.Build(),
			},
		}.Build()}.Build(),
	}.Build()
	template := publicv1.ClusterTemplateReference_builder{Name: "standard"}.Build()
	item := publicv1.ClusterCatalogItem_builder{Title: "GPU cluster", Template: template, Fields: fields}.Build()

	got := clusterCatalogPricingRequests([]*publicv1.ClusterCatalogItem{item})
	if len(got) != 1 || got[0].Service != billing.ServiceCluster || !got[0].Supported {
		t.Fatalf("clusterCatalogPricingRequests() = %v, want one supported cluster request", got)
	}
	request := applyTemplatePricingDefaults(got[0], map[string]string{
		"cluster_template":        "template-default",
		"release_image":           "template-version",
		"baremetal_instance_type": "template-node",
		"node_count":              "10",
	})
	components := catalogPricingComponents(request)
	if len(request.Dimensions) != 0 || len(components) != 2 {
		t.Fatalf("cluster pricing request = %+v with components %v, want only two node-set components", request, components)
	}
	for index, want := range []map[string]string{
		{"baremetal_instance_type": "cpu-node", "node_count": "3"},
		{"baremetal_instance_type": "gpu-node", "node_count": "2"},
	} {
		if !reflect.DeepEqual(components[index].Dimensions, want) {
			t.Fatalf("node-set component %d dimensions = %v, want %#v", index, components[index].Dimensions, want)
		}
	}
}
