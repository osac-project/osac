/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"cmp"
	"maps"
	"slices"
	"strconv"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// catalogPricingInputRow accumulates the catalog items attributed to one distinct (service, dimension, value) tuple
// while aggregating. Catalog items are kept in a map, keyed by id, so the same item attributed to a row from more
// than one field policy is counted once.
type catalogPricingInputRow struct {
	service         string
	dimension       string
	value           string
	acceptsAnyValue bool
	items           map[string]*privatev1.CatalogPricingInputCatalogItem
}

// aggregateCatalogPricingInputs builds the deterministic, distinct (service, dimension, value) inventory described
// by OSAC-6889 AC1-AC4, from catalog items already fetched by the caller. It is a pure function: it takes no DAO,
// billing, or other dependency, so it structurally cannot make a provider call.
func aggregateCatalogPricingInputs(
	computeItems []*privatev1.ComputeInstanceCatalogItem,
	clusterItems []*privatev1.ClusterCatalogItem,
) []*privatev1.CatalogPricingInput {
	rows := map[[3]string]*catalogPricingInputRow{}

	for _, item := range computeItems {
		info := catalogItemInfo(item.GetId(), item.GetMetadata().GetName(), item.GetTitle(), item.GetPublished())
		fields := item.GetFields()
		if fields == nil {
			continue
		}
		// Include provider-condition dimensions even when a dimension is not currently listed as a metered
		// billable dimension (notably boot_disk_storage_tier). The inventory is also the admin's name-alignment
		// reference for provider rules; this preserves that comparison without claiming each condition is metered.
		emitDiskImage(rows, fields.GetDiskImage(), info)
		emitBootDisk(rows, fields.GetBootDisk(), info)
		emitInstanceType(rows, fields.GetInstanceType(), info)
		emitAdditionalDisks(rows, fields.GetAdditionalDisks(), info)
	}

	for _, item := range clusterItems {
		info := catalogItemInfo(item.GetId(), item.GetMetadata().GetName(), item.GetTitle(), item.GetPublished())
		fields := item.GetFields()
		if fields == nil {
			continue
		}
		emitNodeSets(rows, fields.GetNodeSets(), info)
	}

	return sortedCatalogPricingInputs(rows)
}

func catalogItemInfo(id, name, title string, published bool) *privatev1.CatalogPricingInputCatalogItem {
	return privatev1.CatalogPricingInputCatalogItem_builder{
		Id:        id,
		Name:      name,
		Title:     title,
		Published: published,
	}.Build()
}

// emit records that item uses or offers value for (service, dimension), merging into an existing row when present.
func emit(rows map[[3]string]*catalogPricingInputRow, service, dimension, value string, acceptsAnyValue bool,
	item *privatev1.CatalogPricingInputCatalogItem) {
	key := [3]string{service, dimension, value}
	row, ok := rows[key]
	if !ok {
		row = &catalogPricingInputRow{
			service:   service,
			dimension: dimension,
			value:     value,
			items:     map[string]*privatev1.CatalogPricingInputCatalogItem{},
		}
		rows[key] = row
	}
	row.acceptsAnyValue = row.acceptsAnyValue || acceptsAnyValue
	row.items[item.GetId()] = item
}

const (
	serviceComputeInstance = "compute_instance"
	serviceCluster         = "cluster"

	dimensionInstanceType          = "instance_type"
	dimensionAdditionalDiskSize    = "additional_disk_size_gib"
	dimensionAdditionalStorageTier = "additional_storage_tier"
	dimensionNodeCount             = "node_count"
	dimensionBaremetalInstanceType = "baremetal_instance_type"
	dimensionImage                 = "image"
	dimensionBootDiskSize          = "boot_disk_size_gib"
	dimensionBootDiskStorageTier   = "boot_disk_storage_tier"
)

func emitDiskImage(rows map[[3]string]*catalogPricingInputRow,
	policy *privatev1.DiskImageReferenceFieldPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	switch {
	case policy.HasLocked():
		emitReference(rows, dimensionImage, policy.GetLocked().GetId(), policy.GetLocked().GetName(), item)
	case policy.HasEditable():
		emit(rows, serviceComputeInstance, dimensionImage, "", true, item)
		if editable := policy.GetEditable(); editable != nil {
			if def := editable.GetDefaultValue(); def != nil {
				emitReference(rows, dimensionImage, def.GetId(), def.GetName(), item)
			}
		}
	}
}

func emitBootDisk(rows map[[3]string]*catalogPricingInputRow,
	policies *privatev1.ComputeInstanceBootDiskFieldPolicies,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policies == nil {
		return
	}
	emitBootDiskSize(rows, policies.GetSizeGib(), item)
	emitBootDiskStorageTier(rows, policies.GetStorageTier(), item)
}

func emitBootDiskSize(rows map[[3]string]*catalogPricingInputRow,
	policy *privatev1.Int32FieldPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	switch {
	case policy.HasLocked():
		if size := policy.GetLocked(); size != 0 {
			emit(rows, serviceComputeInstance, dimensionBootDiskSize, strconv.Itoa(int(size)), false, item)
		}
	case policy.HasEditable():
		emit(rows, serviceComputeInstance, dimensionBootDiskSize, "", true, item)
		if editable := policy.GetEditable(); editable != nil && editable.HasDefaultValue() {
			if size := editable.GetDefaultValue(); size != 0 {
				emit(rows, serviceComputeInstance, dimensionBootDiskSize, strconv.Itoa(int(size)), false, item)
			}
		}
	}
}

func emitBootDiskStorageTier(rows map[[3]string]*catalogPricingInputRow,
	policy *privatev1.StorageTierReferenceFieldPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	switch {
	case policy.HasLocked():
		emitReference(rows, dimensionBootDiskStorageTier, policy.GetLocked().GetId(), policy.GetLocked().GetName(), item)
	case policy.HasEditable():
		emit(rows, serviceComputeInstance, dimensionBootDiskStorageTier, "", true, item)
		if editable := policy.GetEditable(); editable != nil {
			if def := editable.GetDefaultValue(); def != nil {
				emitReference(rows, dimensionBootDiskStorageTier, def.GetId(), def.GetName(), item)
			}
		}
	}
}

// emitReference uses the canonical name when available, falling back to the stable ID as the metering
// reference formatter does. Catalog policies are canonicalized to carry both values before persistence.
func emitReference(rows map[[3]string]*catalogPricingInputRow, dimension, id, name string,
	item *privatev1.CatalogPricingInputCatalogItem) {
	value := name
	if value == "" {
		value = id
	}
	if value != "" {
		emit(rows, serviceComputeInstance, dimension, value, false, item)
	}
}

func emitInstanceType(rows map[[3]string]*catalogPricingInputRow, policy *privatev1.InstanceTypeReferenceFieldPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	switch {
	case policy.HasLocked():
		emit(rows, serviceComputeInstance, dimensionInstanceType, policy.GetLocked().GetName(), false, item)
	case policy.HasEditable():
		emit(rows, serviceComputeInstance, dimensionInstanceType, "", true, item)
		if def := policy.GetEditable().GetDefaultValue(); def != nil && def.GetName() != "" {
			emit(rows, serviceComputeInstance, dimensionInstanceType, def.GetName(), false, item)
		}
	}
}

func emitAdditionalDisks(rows map[[3]string]*catalogPricingInputRow, policy *privatev1.ComputeInstanceDiskListFieldPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	var disks []*privatev1.ComputeInstanceDisk
	anyValue := false
	switch {
	case policy.HasLocked():
		disks = policy.GetLocked().GetItems()
	case policy.HasEditable():
		disks = policy.GetEditable().GetDefaultValue().GetItems()
		anyValue = true
	}
	for _, disk := range disks {
		if disk.GetSizeGib() != 0 {
			emit(rows, serviceComputeInstance, dimensionAdditionalDiskSize, strconv.Itoa(int(disk.GetSizeGib())), false, item)
		}
		if tier := disk.GetStorageTier(); tier != nil && tier.GetName() != "" {
			emit(rows, serviceComputeInstance, dimensionAdditionalStorageTier, tier.GetName(), false, item)
		}
	}
	if anyValue {
		emit(rows, serviceComputeInstance, dimensionAdditionalDiskSize, "", true, item)
		emit(rows, serviceComputeInstance, dimensionAdditionalStorageTier, "", true, item)
	}
}

func emitNodeSets(rows map[[3]string]*catalogPricingInputRow, policy *privatev1.ClusterNodeSetMapPolicy,
	item *privatev1.CatalogPricingInputCatalogItem) {
	if policy == nil {
		return
	}
	var nodeSets map[string]*privatev1.ClusterCatalogNodeSet
	anyValue := false
	switch {
	case policy.HasLocked():
		nodeSets = policy.GetLocked().GetItems()
	case policy.HasEditable():
		nodeSets = policy.GetEditable().GetDefaultValue().GetItems()
		anyValue = true
	}
	for _, key := range sortedNodeSetKeys(nodeSets) {
		nodeSet := nodeSets[key]
		if nodeSet.GetSize() != 0 {
			emit(rows, serviceCluster, dimensionNodeCount, strconv.Itoa(int(nodeSet.GetSize())), false, item)
		}
		if ref := nodeSet.GetBaremetalInstanceType(); ref != nil && ref.GetName() != "" {
			emit(rows, serviceCluster, dimensionBaremetalInstanceType, ref.GetName(), false, item)
		}
	}
	if anyValue {
		emit(rows, serviceCluster, dimensionNodeCount, "", true, item)
		emit(rows, serviceCluster, dimensionBaremetalInstanceType, "", true, item)
	}
}

// sortedNodeSetKeys returns the node-set map's keys in ascending order, for deterministic iteration.
func sortedNodeSetKeys(m map[string]*privatev1.ClusterCatalogNodeSet) []string {
	return slices.Sorted(maps.Keys(m))
}

func sortedCatalogPricingInputs(rows map[[3]string]*catalogPricingInputRow) []*privatev1.CatalogPricingInput {
	result := make([]*privatev1.CatalogPricingInput, 0, len(rows))
	for _, row := range rows {
		items := make([]*privatev1.CatalogPricingInputCatalogItem, 0, len(row.items))
		for _, item := range row.items {
			items = append(items, item)
		}
		slices.SortFunc(items, func(a, b *privatev1.CatalogPricingInputCatalogItem) int {
			return cmp.Compare(a.GetId(), b.GetId())
		})
		result = append(result, privatev1.CatalogPricingInput_builder{
			Service:         row.service,
			Dimension:       row.dimension,
			Value:           row.value,
			AcceptsAnyValue: row.acceptsAnyValue,
			CatalogItems:    items,
		}.Build())
	}
	slices.SortFunc(result, func(a, b *privatev1.CatalogPricingInput) int {
		if c := cmp.Compare(a.GetService(), b.GetService()); c != 0 {
			return c
		}
		if c := cmp.Compare(a.GetDimension(), b.GetDimension()); c != 0 {
			return c
		}
		return cmp.Compare(a.GetValue(), b.GetValue())
	})
	return result
}
