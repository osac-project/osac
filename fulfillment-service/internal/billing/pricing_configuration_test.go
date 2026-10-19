/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"reflect"
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestApplyComputeInstancePricingConfigurationUsesSelectedValues(t *testing.T) {
	t.Parallel()

	image := publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build()
	defaultType := publicv1.InstanceTypeReference_builder{Name: "small"}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{Locked: image}.Build(),
		InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{Editable: publicv1.EditableInstanceTypeReferenceField_builder{
			DefaultValue: defaultType,
		}.Build()}.Build(),
		BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
			SizeGib:     publicv1.Int32FieldPolicy_builder{Locked: pricingTestInt32(40)}.Build(),
			StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{Locked: publicv1.StorageTierReference_builder{Name: "premium"}.Build()}.Build(),
		}.Build(),
	}.Build()
	request := CatalogPricingRequest{
		Service:      ServiceComputeInstance,
		Dimensions:   map[string]string{"image_ref": "fedora-16", "instance_type": "small", "boot_disk_size_gib": "40", "storage_tier": "premium"},
		Description:  "Small Fedora VM",
		PriceMayVary: true,
		Supported:    true,
	}
	selectedType := publicv1.InstanceTypeReference_builder{Name: "large"}.Build()
	spec := publicv1.ComputeInstanceSpec_builder{
		DiskImage:    publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
		InstanceType: selectedType,
		BootDisk: publicv1.ComputeInstanceDisk_builder{
			SizeGib: pricingTestInt32(40), StorageTier: publicv1.StorageTierReference_builder{Name: "premium"}.Build(),
		}.Build(),
	}.Build()

	got, err := ApplyComputeInstancePricingConfiguration(request, spec, fields)
	if err != nil {
		t.Fatalf("ApplyComputeInstancePricingConfiguration() error = %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Dimensions, map[string]string{"instance_type": "large"}) {
		t.Fatalf("pricing requests = %v, want only selected instance type", got)
	}
}

func pricingTestInt32(value int32) *int32 {
	return &value
}

func TestApplyComputeInstancePricingConfigurationSeparatesAdditionalDisks(t *testing.T) {
	t.Parallel()
	firstSize, secondSize := int32(50), int32(100)
	first := publicv1.ComputeInstanceDisk_builder{SizeGib: &firstSize}.Build()
	second := publicv1.ComputeInstanceDisk_builder{SizeGib: &secondSize}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{Locked: publicv1.InstanceTypeReference_builder{Name: "large"}.Build()}.Build(),
		AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{Locked: publicv1.ComputeInstanceDiskList_builder{
			Items: []*publicv1.ComputeInstanceDisk{first, second},
		}.Build()}.Build(),
	}.Build()
	request := CatalogPricingRequest{Service: ServiceComputeInstance, Dimensions: map[string]string{"instance_type": "large"}, Supported: true}
	spec := publicv1.ComputeInstanceSpec_builder{
		InstanceType:    publicv1.InstanceTypeReference_builder{Name: "large"}.Build(),
		AdditionalDisks: []*publicv1.ComputeInstanceDisk{first, second},
	}.Build()
	got, err := ApplyComputeInstancePricingConfiguration(request, spec, fields)
	if err != nil {
		t.Fatalf("ApplyComputeInstancePricingConfiguration() error = %v", err)
	}
	if len(got) != 3 || !reflect.DeepEqual(got[0].Dimensions, map[string]string{"instance_type": "large"}) ||
		!reflect.DeepEqual(got[1].Dimensions, map[string]string{"additional_disk_size_gib": "50"}) ||
		!reflect.DeepEqual(got[2].Dimensions, map[string]string{"additional_disk_size_gib": "100"}) {
		t.Fatalf("pricing requests = %+v, want base and separate disks", got)
	}
}

func TestApplyComputeInstancePricingConfigurationRejectsLockedValueOverride(t *testing.T) {
	t.Parallel()

	lockedImage := publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build()
	fields := publicv1.ComputeInstanceCatalogItemFields_builder{
		DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{Locked: lockedImage}.Build(),
	}.Build()
	request := CatalogPricingRequest{Dimensions: map[string]string{"image_ref": "fedora-16"}, Supported: true}
	spec := publicv1.ComputeInstanceSpec_builder{
		DiskImage: publicv1.DiskImageReference_builder{Name: "rhel-9.6"}.Build(),
	}.Build()

	if _, err := ApplyComputeInstancePricingConfiguration(request, spec, fields); err == nil {
		t.Fatal("ApplyComputeInstancePricingConfiguration() error = nil, want locked-field error")
	}
}

func TestApplyClusterPricingConfigurationUsesSelectedNodeSet(t *testing.T) {
	t.Parallel()

	request := CatalogPricingRequest{
		Service:    ServiceCluster,
		Dimensions: map[string]string{"release_image": "openshift-4.18", "baremetal_instance_type": "small", "node_count": "3"},
		Supported:  true,
	}
	fields := publicv1.ClusterCatalogItemFields_builder{
		Version:  publicv1.ClusterVersionReferenceFieldPolicy_builder{Editable: publicv1.EditableClusterVersionReferenceField_builder{}.Build()}.Build(),
		NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Editable: publicv1.EditableClusterNodeSetMap_builder{}.Build()}.Build(),
	}.Build()
	version := publicv1.ClusterVersionReference_builder{Name: "openshift-4.19"}.Build()
	instanceType := publicv1.BareMetalInstanceTypeReference_builder{Name: "large"}.Build()
	size := int32(5)
	nodeSet := publicv1.ClusterNodeSet_builder{Size: &size, BaremetalInstanceType: instanceType}.Build()
	spec := publicv1.ClusterSpec_builder{
		Version:  version,
		NodeSets: map[string]*publicv1.ClusterNodeSet{"workers": nodeSet},
	}.Build()

	got, err := ApplyClusterPricingConfiguration(request, spec, fields)
	if err != nil {
		t.Fatalf("ApplyClusterPricingConfiguration() error = %v", err)
	}
	if len(got) != 2 || got[0].Dimensions["release_image"] != "openshift-4.19" || got[0].Dimensions["node_count"] != "" || got[1].Dimensions["baremetal_instance_type"] != "large" || got[1].Dimensions["node_count"] != "5" {
		t.Fatalf("pricing requests = %v, want base version and selected node set", got)
	}
}
