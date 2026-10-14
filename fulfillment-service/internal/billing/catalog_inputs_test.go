/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billing

import (
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCatalogPricingInputsGroupsEqualValuesAndRetainsSources(t *testing.T) {
	image := publicv1.DiskImageReferenceFieldPolicy_builder{
		Locked: publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
	}.Build()
	instanceType := publicv1.InstanceTypeReferenceFieldPolicy_builder{
		Locked: publicv1.InstanceTypeReference_builder{Name: "small"}.Build(),
	}.Build()
	first := publicv1.ComputeInstanceCatalogItem_builder{
		Id:        "item-1",
		Title:     "Small Fedora",
		Published: true,
		Metadata:  publicv1.Metadata_builder{Name: "vm-small"}.Build(),
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage:    image,
			InstanceType: instanceType,
		}.Build(),
	}.Build()
	second := publicv1.ComputeInstanceCatalogItem_builder{
		Id:       "item-2",
		Title:    "Large Fedora",
		Metadata: publicv1.Metadata_builder{Name: "vm-large"}.Build(),
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: image,
		}.Build(),
	}.Build()

	inputs := CatalogPricingInputsFromCatalog([]*publicv1.ComputeInstanceCatalogItem{first, second}, nil, nil)

	var imageInput *publicv1.CatalogPricingInput
	for _, input := range inputs {
		if input.GetService() == "compute_instance" && input.GetDimension() == "image_ref" && input.GetValue() == "fedora-16" {
			imageInput = input
			break
		}
	}
	if imageInput == nil {
		t.Fatal("missing image_ref inventory row for fedora-16")
	}
	if len(imageInput.GetCatalogItems()) != 2 {
		t.Fatalf("got %d catalog sources, want 2", len(imageInput.GetCatalogItems()))
	}
	if imageInput.GetCatalogItems()[0].GetName() != "vm-large" || imageInput.GetCatalogItems()[1].GetName() != "vm-small" {
		t.Fatalf("catalog sources are not deterministically sorted: %v", imageInput.GetCatalogItems())
	}

	if got := findCatalogPricingInput(inputs, "compute_instance", "instance_type", "small"); got == nil {
		t.Fatal("missing instance_type inventory row for small")
	} else if len(got.GetCatalogItems()) != 1 || got.GetCatalogItems()[0].GetId() != "item-1" {
		t.Fatalf("unexpected instance_type sources: %v", got.GetCatalogItems())
	} else if got.GetAcceptsAnyValue() {
		t.Fatal("locked instance type unexpectedly accepts any value")
	}
}

func TestCatalogPricingInputsMarksEditableValuesAsOpenEnded(t *testing.T) {
	item := publicv1.ComputeInstanceCatalogItem_builder{
		Id:       "item-editable",
		Metadata: publicv1.Metadata_builder{Name: "vm-editable"}.Build(),
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
				Editable: publicv1.EditableDiskImageReferenceField_builder{
					DefaultValue: publicv1.DiskImageReference_builder{Name: "fedora-16"}.Build(),
				}.Build(),
			}.Build(),
			InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
				Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	inputs := CatalogPricingInputsFromCatalog([]*publicv1.ComputeInstanceCatalogItem{item}, nil, nil)

	if input := findCatalogPricingInput(inputs, "compute_instance", "image_ref", "fedora-16"); input == nil {
		t.Fatal("missing editable image default value")
	}
	var anyImage *publicv1.CatalogPricingInput
	var anyInstanceType *publicv1.CatalogPricingInput
	for _, input := range inputs {
		if input.GetService() != "compute_instance" || !input.GetAcceptsAnyValue() {
			continue
		}
		switch input.GetDimension() {
		case "image_ref":
			anyImage = input
		case "instance_type":
			anyInstanceType = input
		}
	}
	if anyImage == nil || len(anyImage.GetCatalogItems()) != 1 {
		t.Fatalf("missing open-ended image row: %v", anyImage)
	}
	if anyInstanceType == nil || anyInstanceType.GetValue() != "" || len(anyInstanceType.GetCatalogItems()) != 1 {
		t.Fatalf("missing open-ended instance type row: %v", anyInstanceType)
	}
}

func TestCatalogPricingInputsIncludesClusterAndBareMetalValues(t *testing.T) {
	cluster := publicv1.ClusterCatalogItem_builder{
		Id:       "cluster-1",
		Metadata: publicv1.Metadata_builder{Name: "openshift-cluster"}.Build(),
		Template: publicv1.ClusterTemplateReference_builder{Name: "standard-cluster"}.Build(),
		Fields: publicv1.ClusterCatalogItemFields_builder{
			Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
				Locked: publicv1.ClusterVersionReference_builder{Name: "openshift-4-18"}.Build(),
			}.Build(),
			NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{
				Locked: publicv1.ClusterNodeSetMap_builder{Items: map[string]*publicv1.ClusterCatalogNodeSet{
					"workers": publicv1.ClusterCatalogNodeSet_builder{
						Size:                  3,
						BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Name: "bm-standard"}.Build(),
					}.Build(),
				}}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	bareMetal := publicv1.BareMetalInstanceCatalogItem_builder{
		Id:       "baremetal-1",
		Metadata: publicv1.Metadata_builder{Name: "bm-standard-item"}.Build(),
		Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
			InstanceType: publicv1.BareMetalInstanceTypeReferenceFieldPolicy_builder{
				Locked: publicv1.BareMetalInstanceTypeReference_builder{Name: "bm-standard"}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	inputs := CatalogPricingInputsFromCatalog(nil, []*publicv1.ClusterCatalogItem{cluster}, []*publicv1.BareMetalInstanceCatalogItem{bareMetal})
	for _, testCase := range []struct {
		service   string
		dimension string
		value     string
	}{
		{service: "cluster_order", dimension: "cluster_template", value: "standard-cluster"},
		{service: "cluster_order", dimension: "release_image", value: "openshift-4-18"},
		{service: "cluster_order", dimension: "baremetal_instance_type", value: "bm-standard"},
		{service: "cluster_order", dimension: "node_count", value: "3"},
		{service: "bare_metal_instance", dimension: "bm_instance_type", value: "bm-standard"},
		{service: "bare_metal_instance", dimension: "catalog_item", value: "bm-standard-item"},
	} {
		if input := findCatalogPricingInput(inputs, testCase.service, testCase.dimension, testCase.value); input == nil {
			t.Errorf("missing %s/%s=%s inventory row", testCase.service, testCase.dimension, testCase.value)
		}
	}
}

func findCatalogPricingInput(inputs []*publicv1.CatalogPricingInput, service, dimension, value string) *publicv1.CatalogPricingInput {
	for _, input := range inputs {
		if input.GetService() == service && input.GetDimension() == dimension && input.GetValue() == value {
			return input
		}
	}
	return nil
}
