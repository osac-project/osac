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
	want := map[string]string{"instance_type": "m7.large"}
	if !reflect.DeepEqual(requests[0].Dimensions, want) {
		t.Fatalf("dimensions = %#v, want %#v", requests[0].Dimensions, want)
	}
}

func TestComputeInstanceCoverageRequestsCheckEachAdditionalDisk(t *testing.T) {
	instance := privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Name: "m7.large"}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		BootDisk: privatev1.ComputeInstanceDisk_builder{
			SizeGib: testInt32Pointer(40), StorageTier: privatev1.StorageTierReference_builder{Name: "premium"}.Build(),
		}.Build(),
		AdditionalDisks: []*privatev1.ComputeInstanceDisk{
			privatev1.ComputeInstanceDisk_builder{SizeGib: testInt32Pointer(50), StorageTier: privatev1.StorageTierReference_builder{Name: "premium"}.Build()}.Build(),
			privatev1.ComputeInstanceDisk_builder{SizeGib: testInt32Pointer(100), StorageTier: privatev1.StorageTierReference_builder{Name: "archive"}.Build()}.Build(),
		},
	}.Build()}.Build()

	requests := computeInstanceCoverageRequests(instance)
	if len(requests) != 3 {
		t.Fatalf("computeInstanceCoverageRequests() = %+v, want base and two disk requests", requests)
	}
	if !reflect.DeepEqual(requests[0].Dimensions, map[string]string{"instance_type": "m7.large"}) {
		t.Fatalf("base coverage request = %+v, want only instance_type", requests[0])
	}
	for index, want := range []map[string]string{
		{"additional_disk_size_gib": "50", "additional_storage_tier": "premium"},
		{"additional_disk_size_gib": "100", "additional_storage_tier": "archive"},
	} {
		request := requests[index+1]
		if !request.Supported || !reflect.DeepEqual(request.Dimensions, want) {
			t.Fatalf("additional disk coverage request %d = %+v, want dimensions %#v", index, request, want)
		}
	}
}

func TestClusterCoverageRequestsUseOnlyNodeSetDimensions(t *testing.T) {
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
	if len(requests) != 2 {
		t.Fatalf("clusterCoverageRequests() = %+v, want only two node-set requests", requests)
	}
	for index, want := range []map[string]string{
		{"baremetal_instance_type": "cpu", "node_count": "3"},
		{"baremetal_instance_type": "gpu", "node_count": "2"},
	} {
		request := requests[index]
		if !request.Supported || request.Service != billing.ServiceCluster || !reflect.DeepEqual(request.Dimensions, want) {
			t.Fatalf("cluster coverage request %d = %+v, want only node-set dimensions %#v", index, request, want)
		}
	}
}

func testInt32Pointer(value int32) *int32 {
	return &value
}
