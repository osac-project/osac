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
	"errors"
	"sort"
	"strconv"
	"strings"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// CatalogPricingRequest describes one billable resource component to resolve.
type CatalogPricingRequest struct {
	Service      string
	Dimensions   map[string]string
	Components   []CatalogPricingComponentRequest
	Description  string
	PriceMayVary bool
	Supported    bool
}

// CatalogPricingComponentRequest describes one component of a catalog item's
// default configuration when the item contains multiple independently rated
// components, such as additional disks or cluster node sets.
type CatalogPricingComponentRequest struct {
	Dimensions  map[string]string
	Description string
}

var ErrInvalidPricingConfiguration = errors.New("pricing configuration is invalid for the catalog item")

// ApplyComputeInstancePricingConfiguration overlays selected values and returns
// one request for the instance and one for each additional disk.
func ApplyComputeInstancePricingConfiguration(
	request CatalogPricingRequest,
	spec *publicv1.ComputeInstanceSpec,
	fields *publicv1.ComputeInstanceCatalogItemFields,
) ([]CatalogPricingRequest, error) {
	if spec == nil || fields == nil {
		return nil, ErrInvalidPricingConfiguration
	}
	if request.Dimensions == nil {
		request.Dimensions = make(map[string]string)
	}
	request.Components = nil
	request.Supported = true
	delete(request.Dimensions, "additional_disk_size_gib")
	delete(request.Dimensions, "additional_storage_tier")
	if err := applyComputeInstancePricingSelections(request.Dimensions, spec, fields); err != nil {
		return nil, err
	}
	additionalDisks := computeInstanceAdditionalDisks(spec, fields.GetAdditionalDisks())
	if err := validateComputeInstanceAdditionalDisks(additionalDisks, fields.GetAdditionalDisks()); err != nil {
		return nil, err
	}
	requests, err := computeInstancePricingRequests(request, additionalDisks)
	if err != nil {
		return nil, err
	}
	return requests, nil
}

func applyComputeInstancePricingSelections(
	dimensions map[string]string,
	spec *publicv1.ComputeInstanceSpec,
	fields *publicv1.ComputeInstanceCatalogItemFields,
) error {
	if err := applyReferencePricingSelection(dimensions, "image_ref", spec.GetDiskImage().GetName(), fields.GetDiskImage().GetLocked().GetName(), fields.GetDiskImage().GetEditable() != nil); err != nil {
		return err
	}
	if err := applyReferencePricingSelection(dimensions, "instance_type", spec.GetInstanceType().GetName(), fields.GetInstanceType().GetLocked().GetName(), fields.GetInstanceType().GetEditable() != nil); err != nil {
		return err
	}
	return applyComputeInstanceBootDiskPricingSelection(dimensions, spec.GetBootDisk(), fields.GetBootDisk())
}

func applyComputeInstanceBootDiskPricingSelection(
	dimensions map[string]string,
	disk *publicv1.ComputeInstanceDisk,
	policy *publicv1.ComputeInstanceBootDiskFieldPolicies,
) error {
	if disk == nil {
		return nil
	}
	if disk.HasSizeGib() {
		lockedSize := policy.GetSizeGib().GetLocked()
		selectedSize := disk.GetSizeGib()
		if policy.GetSizeGib().HasLocked() && lockedSize != selectedSize {
			return ErrInvalidPricingConfiguration
		}
		if !policy.GetSizeGib().HasLocked() && policy.GetSizeGib().GetEditable() == nil {
			return ErrInvalidPricingConfiguration
		}
		dimensions["boot_disk_size_gib"] = strconv.FormatInt(int64(selectedSize), 10)
	}
	if tier := disk.GetStorageTier().GetName(); tier != "" {
		if err := applyReferencePricingSelection(dimensions, "storage_tier", tier, policy.GetStorageTier().GetLocked().GetName(), policy.GetStorageTier().GetEditable() != nil); err != nil {
			return err
		}
	}
	return nil
}

func computeInstanceAdditionalDisks(
	spec *publicv1.ComputeInstanceSpec,
	policy *publicv1.ComputeInstanceDiskListFieldPolicy,
) []*publicv1.ComputeInstanceDisk {
	additionalDisks := spec.GetAdditionalDisks()
	if len(additionalDisks) == 0 && policy != nil {
		if locked := policy.GetLocked(); locked != nil {
			additionalDisks = locked.GetItems()
		} else if editable := policy.GetEditable(); editable != nil {
			additionalDisks = editable.GetDefaultValue().GetItems()
		}
	}
	return additionalDisks
}

func validateComputeInstanceAdditionalDisks(
	additionalDisks []*publicv1.ComputeInstanceDisk,
	policy *publicv1.ComputeInstanceDiskListFieldPolicy,
) error {
	if len(additionalDisks) == 0 {
		return nil
	}
	if policy == nil {
		return ErrInvalidPricingConfiguration
	}
	if locked := policy.GetLocked(); locked != nil {
		items := locked.GetItems()
		if len(items) != len(additionalDisks) {
			return ErrInvalidPricingConfiguration
		}
		for index, disk := range additionalDisks {
			if !computeInstanceAdditionalDiskMatches(disk, items[index]) {
				return ErrInvalidPricingConfiguration
			}
		}
	} else if policy.GetEditable() == nil {
		return ErrInvalidPricingConfiguration
	}
	return nil
}

func computeInstanceAdditionalDiskMatches(disk, expected *publicv1.ComputeInstanceDisk) bool {
	return disk != nil && expected != nil && expected.GetSizeGib() == disk.GetSizeGib() && expected.GetStorageTier().GetName() == disk.GetStorageTier().GetName()
}

func computeInstancePricingRequests(
	request CatalogPricingRequest,
	additionalDisks []*publicv1.ComputeInstanceDisk,
) ([]CatalogPricingRequest, error) {
	requests := []CatalogPricingRequest{request}
	for index, disk := range additionalDisks {
		if disk == nil {
			return nil, ErrInvalidPricingConfiguration
		}
		component := request
		component.Dimensions = make(map[string]string, 2)
		component.Description = strings.TrimSpace(request.Description + " additional disk " + strconv.Itoa(index+1))
		if disk.HasSizeGib() {
			component.Dimensions["additional_disk_size_gib"] = strconv.FormatInt(int64(disk.GetSizeGib()), 10)
		}
		if tier := disk.GetStorageTier().GetName(); tier != "" {
			component.Dimensions["additional_storage_tier"] = tier
		}
		requests = append(requests, component)
	}
	return requests, nil
}

// ApplyClusterPricingConfiguration overlays selected values and returns one
// request for the cluster template and one for each node set.
func ApplyClusterPricingConfiguration(
	request CatalogPricingRequest,
	spec *publicv1.ClusterSpec,
	fields *publicv1.ClusterCatalogItemFields,
) ([]CatalogPricingRequest, error) {
	if spec == nil || fields == nil {
		return nil, ErrInvalidPricingConfiguration
	}
	if request.Dimensions == nil {
		request.Dimensions = make(map[string]string)
	}
	request.Components = nil
	request.Supported = true
	if err := applyReferencePricingSelection(request.Dimensions, "release_image", spec.GetVersion().GetName(), fields.GetVersion().GetLocked().GetName(), fields.GetVersion().GetEditable() != nil); err != nil {
		return nil, err
	}
	nodeSets := spec.GetNodeSets()
	policy := fields.GetNodeSets()
	if len(nodeSets) == 0 && policy != nil {
		defaults := policy.GetLocked()
		if defaults == nil && policy.GetEditable() != nil {
			defaults = policy.GetEditable().GetDefaultValue()
		}
		if defaults != nil {
			nodeSets = make(map[string]*publicv1.ClusterNodeSet, len(defaults.GetItems()))
			for name, nodeSet := range defaults.GetItems() {
				if nodeSet == nil {
					return nil, ErrInvalidPricingConfiguration
				}
				size := nodeSet.GetSize()
				nodeSets[name] = publicv1.ClusterNodeSet_builder{
					Size:                  &size,
					BaremetalInstanceType: nodeSet.GetBaremetalInstanceType(),
				}.Build()
			}
		}
	}
	if len(nodeSets) == 0 {
		request.Supported = false
		return []CatalogPricingRequest{request}, nil
	}
	if err := validateClusterNodeSetsSelection(policy, nodeSets); err != nil {
		return nil, err
	}
	delete(request.Dimensions, "baremetal_instance_type")
	delete(request.Dimensions, "node_count")
	names := make([]string, 0, len(nodeSets))
	for name := range nodeSets {
		names = append(names, name)
	}
	sort.Strings(names)
	requests := []CatalogPricingRequest{request}
	for _, name := range names {
		nodeSet := nodeSets[name]
		component := request
		component.Dimensions = clonePricingDimensions(request.Dimensions)
		component.Description = strings.TrimSpace(request.Description + " " + name)
		if instanceType := nodeSet.GetBaremetalInstanceType().GetName(); instanceType != "" {
			component.Dimensions["baremetal_instance_type"] = instanceType
		}
		if nodeSet.HasSize() {
			component.Dimensions["node_count"] = strconv.FormatInt(int64(nodeSet.GetSize()), 10)
		}
		requests = append(requests, component)
	}
	return requests, nil
}

// ApplyBareMetalPricingConfiguration overlays the selected bare-metal type and
// image on the catalog item's locked and default values.
func ApplyBareMetalPricingConfiguration(
	request CatalogPricingRequest,
	spec *publicv1.BareMetalInstanceSpec,
	fields *publicv1.BareMetalInstanceCatalogItemFields,
) (CatalogPricingRequest, error) {
	if spec == nil || fields == nil {
		return request, ErrInvalidPricingConfiguration
	}
	if request.Dimensions == nil {
		request.Dimensions = make(map[string]string)
	}
	if err := applyReferencePricingSelection(request.Dimensions, "bm_instance_type", spec.GetInstanceType().GetName(), fields.GetInstanceType().GetLocked().GetName(), fields.GetInstanceType().GetEditable() != nil); err != nil {
		return request, err
	}
	if err := applyReferencePricingSelection(request.Dimensions, "image_ref", spec.GetDiskImage().GetName(), fields.GetDiskImage().GetLocked().GetName(), fields.GetDiskImage().GetEditable() != nil); err != nil {
		return request, err
	}
	return request, nil
}

func applyReferencePricingSelection(dimensions map[string]string, dimension, selected, locked string, editable bool) error {
	selected = strings.TrimSpace(selected)
	locked = strings.TrimSpace(locked)
	if selected == "" {
		return nil
	}
	if locked != "" {
		if selected != locked {
			return ErrInvalidPricingConfiguration
		}
		return nil
	}
	if !editable {
		return ErrInvalidPricingConfiguration
	}
	dimensions[dimension] = selected
	return nil
}

func validateClusterNodeSetsSelection(policy *publicv1.ClusterNodeSetMapPolicy, selected map[string]*publicv1.ClusterNodeSet) error {
	if policy == nil {
		return ErrInvalidPricingConfiguration
	}
	for _, nodeSet := range selected {
		if nodeSet == nil {
			return ErrInvalidPricingConfiguration
		}
	}
	if locked := policy.GetLocked(); locked != nil {
		if len(locked.GetItems()) != len(selected) {
			return ErrInvalidPricingConfiguration
		}
		for name, expected := range locked.GetItems() {
			actual := selected[name]
			if expected == nil || actual == nil || expected.GetSize() != actual.GetSize() || expected.GetBaremetalInstanceType().GetName() != actual.GetBaremetalInstanceType().GetName() {
				return ErrInvalidPricingConfiguration
			}
		}
		return nil
	}
	if policy.GetEditable() == nil {
		return ErrInvalidPricingConfiguration
	}
	return nil
}

func clonePricingDimensions(dimensions map[string]string) map[string]string {
	cloned := make(map[string]string, len(dimensions))
	for key, value := range dimensions {
		cloned[key] = value
	}
	return cloned
}
