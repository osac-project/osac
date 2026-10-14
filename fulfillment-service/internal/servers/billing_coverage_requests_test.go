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
)

func TestComputeInstanceCoverageRequestsUseResolvedResourceValues(t *testing.T) {
	instance := privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Name: "m7.large"}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		BootDisk: privatev1.ComputeInstanceDisk_builder{
			SizeGib:     testInt32Pointer(40),
			StorageTier: privatev1.StorageTierReference_builder{Name: "premium"}.Build(),
		}.Build(),
	}.Build()}.Build()

	requests := computeInstanceCoverageRequests(instance)
	if len(requests) != 1 || !requests[0].Supported || requests[0].Service != billing.ServiceComputeInstance {
		t.Fatalf("computeInstanceCoverageRequests() = %+v, want one supported compute request", requests)
	}
	want := map[string]string{
		"instance_type": "m7.large", "image_ref": "fedora-16", "boot_disk_size_gib": "40", "storage_tier": "premium",
	}
	if !reflect.DeepEqual(requests[0].Dimensions, want) {
		t.Fatalf("dimensions = %#v, want %#v", requests[0].Dimensions, want)
	}
}

func TestComputeInstanceCoverageRequestsCheckEachAdditionalDisk(t *testing.T) {
	instance := privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Name: "m7.large"}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		AdditionalDisks: []*privatev1.ComputeInstanceDisk{
			privatev1.ComputeInstanceDisk_builder{SizeGib: testInt32Pointer(50), StorageTier: privatev1.StorageTierReference_builder{Name: "premium"}.Build()}.Build(),
			privatev1.ComputeInstanceDisk_builder{SizeGib: testInt32Pointer(100), StorageTier: privatev1.StorageTierReference_builder{Name: "archive"}.Build()}.Build(),
		},
	}.Build()}.Build()

	requests := computeInstanceCoverageRequests(instance)
	if len(requests) != 3 {
		t.Fatalf("computeInstanceCoverageRequests() = %+v, want base and two disk requests", requests)
	}
	if requests[0].Dimensions["instance_type"] != "m7.large" || requests[0].Dimensions["image_ref"] != "fedora-16" {
		t.Fatalf("base coverage request = %+v, want compute dimensions", requests[0])
	}
	for _, request := range requests[1:] {
		if !request.Supported || request.Dimensions["instance_type"] != "" || request.Dimensions["image_ref"] != "" {
			t.Fatalf("additional disk coverage request = %+v, want disk dimensions only", request)
		}
	}
	if requests[1].Dimensions["additional_disk_size_gib"] == requests[2].Dimensions["additional_disk_size_gib"] || requests[1].Dimensions["additional_storage_tier"] == requests[2].Dimensions["additional_storage_tier"] {
		t.Fatalf("additional disk requests = %+v, want both distinct disk configurations", requests)
	}
}

func TestClusterCoverageRequestsCheckEachNodeSet(t *testing.T) {
	cluster := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{
		Template: privatev1.ClusterTemplateReference_builder{Name: "standard"}.Build(),
		Version:  privatev1.ClusterVersionReference_builder{Name: "4.18"}.Build(),
		NodeSets: map[string]*privatev1.ClusterNodeSet{
			"compute": privatev1.ClusterNodeSet_builder{
				Size: testInt32Pointer(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "cpu"}.Build(),
			}.Build(),
			"gpu": privatev1.ClusterNodeSet_builder{
				Size: testInt32Pointer(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "gpu"}.Build(),
			}.Build(),
		},
	}.Build()}.Build()

	requests := clusterCoverageRequests(cluster)
	if len(requests) != 3 {
		t.Fatalf("clusterCoverageRequests() = %+v, want base and two node-set requests", requests)
	}
	for _, request := range requests {
		if !request.Supported || request.Service != billing.ServiceCluster || request.Dimensions["cluster_template"] != "standard" || request.Dimensions["release_image"] != "4.18" {
			t.Fatalf("cluster coverage request = %+v, want resolved shared dimensions", request)
		}
	}
	if requests[0].Dimensions["baremetal_instance_type"] != "" || requests[1].Dimensions["baremetal_instance_type"] == requests[2].Dimensions["baremetal_instance_type"] {
		t.Fatalf("cluster requests = %+v, want distinct node-set instance types", requests)
	}
}

func TestBareMetalCoverageRequestUsesResolvedResourceValues(t *testing.T) {
	instance := privatev1.BareMetalInstance_builder{Spec: privatev1.BareMetalInstanceSpec_builder{
		CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Name: "gpu-node"}.Build(),
		InstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "h100"}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Name: "rhel-9.6"}.Build(),
	}.Build()}.Build()

	requests := bareMetalInstanceCoverageRequests(instance)
	if len(requests) != 1 || !requests[0].Supported || requests[0].Service != billing.ServiceBareMetalInstance {
		t.Fatalf("bareMetalInstanceCoverageRequests() = %+v, want one supported BM request", requests)
	}
	want := map[string]string{"catalog_item": "gpu-node", "bm_instance_type": "h100", "image_ref": "rhel-9.6"}
	if !reflect.DeepEqual(requests[0].Dimensions, want) {
		t.Fatalf("dimensions = %#v, want %#v", requests[0].Dimensions, want)
	}
}

func testInt32Pointer(value int32) *int32 {
	return &value
}
