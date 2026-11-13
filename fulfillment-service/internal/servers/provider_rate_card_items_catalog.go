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
	"slices"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// computeInstanceServiceName is the OSAC service name VMaaS compute instances are billed under. It matches the
// design's own rate-card example (§4.3) and the service name osac-metering already sends for this dimension.
const computeInstanceServiceName = "compute_instance"

// catalogDimensionIndex retains scalar values and repeated-component boundaries so matching does not need to
// repeat the field-policy walk over the VMaaS/CaaS catalog item DAOs.
type catalogDimensionIndex struct {
	// byService includes every catalog item, including items that emit no pricing dimensions. A row with no
	// conditions matches every item in its service.
	byService map[string]map[string]*privatev1.CatalogPricingInputCatalogItem
	// byValue maps (service, dimension, value) to the catalog items locked to, or defaulted to, exactly that
	// value. Only scalar conditions use this flattened index; component conditions instead use one component
	// satisfying the whole group, or an editable policy for that kind that accepts any requested combination.
	byValue map[[3]string]map[string]*privatev1.CatalogPricingInputCatalogItem
	// anyByDimension maps (service, dimension) to every catalog item that has any value for that dimension, open
	// or fixed: the union of catalog items across all of that dimension's rows. This serves scalar ANY conditions;
	// component ANY conditions require the dimension on the same component, unless the kind's policy is editable.
	anyByDimension   map[[2]string]map[string]*privatev1.CatalogPricingInputCatalogItem
	componentsByItem map[[2]string]map[catalogComponentKind]catalogComponents
}

type catalogComponentKind string

const (
	catalogComponentAdditionalDisk catalogComponentKind = "additional_disk"
	catalogComponentNodeSet        catalogComponentKind = "node_set"
)

type catalogComponents struct {
	values   []map[string]string
	editable bool
}

// catalogPricingInventory caches the unchanged public comparison rows, all items for conditionless rows, and
// components keyed by (service, item ID) and kind. Each component keeps one disk's or node set's dimension/value
// pairs together. An editable list/map policy accepts any requested combination for that kind, even without defaults.
type catalogPricingInventory struct {
	inputs           []*privatev1.CatalogPricingInput
	itemsByService   map[string]map[string]*privatev1.CatalogPricingInputCatalogItem
	componentsByItem map[[2]string]map[catalogComponentKind]catalogComponents
}

// newCatalogPricingInventoryCache builds the rate-card server's inventory cache for the same configurable inventory
// TTL as the catalog pricing inputs server. The fill budget is the shared 15s cap that newTTLCache clamps to at most
// half the configured TTL. Keeping both servers on one TTL means the freshness bound an administrator reads in the
// chart applies to every cached catalog sweep.
func newCatalogPricingInventoryCache(ttl time.Duration) *ttlCache[catalogInventoryKey, catalogPricingInventory] {
	return newTTLCache[catalogInventoryKey, catalogPricingInventory](ttl, catalogInventoryCacheFillBudget, 1,
		func(catalogInventoryKey) string { return "catalog-inventory" })
}

// catalogItemsByService returns all catalog items grouped by their mapped pricing service, even when an item has
// no field policy that contributes a row to CatalogPricingInputs.List.
func catalogItemsByService(computeItems []*privatev1.ComputeInstanceCatalogItem,
	clusterItems []*privatev1.ClusterCatalogItem) map[string]map[string]*privatev1.CatalogPricingInputCatalogItem {
	result := map[string]map[string]*privatev1.CatalogPricingInputCatalogItem{}
	add := func(service string, item *privatev1.CatalogPricingInputCatalogItem) {
		if result[service] == nil {
			result[service] = map[string]*privatev1.CatalogPricingInputCatalogItem{}
		}
		result[service][item.GetId()] = item
	}
	for _, item := range computeItems {
		if item != nil {
			add(serviceComputeInstance, catalogItemInfo(item.GetId(), item.GetMetadata().GetName(), item.GetTitle(), item.GetPublished()))
		}
	}
	for _, item := range clusterItems {
		if item != nil {
			add(serviceCluster, catalogItemInfo(item.GetId(), item.GetMetadata().GetName(), item.GetTitle(), item.GetPublished()))
		}
	}
	return result
}

// buildCatalogDimensionIndex indexes comparison rows for scalar conditions while retaining component boundaries
// and editable policies for component groups, plus the complete per-service catalog item set for conditionless rows.
func buildCatalogDimensionIndex(inventory catalogPricingInventory) catalogDimensionIndex {
	idx := catalogDimensionIndex{
		byService:        inventory.itemsByService,
		byValue:          map[[3]string]map[string]*privatev1.CatalogPricingInputCatalogItem{},
		anyByDimension:   map[[2]string]map[string]*privatev1.CatalogPricingInputCatalogItem{},
		componentsByItem: inventory.componentsByItem,
	}
	for _, input := range inventory.inputs {
		dimKey := [2]string{input.GetService(), input.GetDimension()}
		valKey := [3]string{input.GetService(), input.GetDimension(), input.GetValue()}
		if idx.anyByDimension[dimKey] == nil {
			idx.anyByDimension[dimKey] = map[string]*privatev1.CatalogPricingInputCatalogItem{}
		}
		if idx.byValue[valKey] == nil {
			idx.byValue[valKey] = map[string]*privatev1.CatalogPricingInputCatalogItem{}
		}
		for _, item := range input.GetCatalogItems() {
			idx.anyByDimension[dimKey][item.GetId()] = item
			idx.byValue[valKey][item.GetId()] = item
		}
	}
	return idx
}

// matchCatalogItems evaluates every one of a rate-card row's conditions together against idx, returning the names
// of the catalog items satisfying all of them. A row with no conditions matches every catalog item of its service.
// Scalar EQUALS conditions match locked/default values; scalar ANY matches any value, open or fixed. Conditions
// of one component kind must all hold on one disk or node set, unless that item's policy for the kind is editable,
// which accepts any requested combination that is not self-contradictory. Scalar and component groups intersect.
// Production rows currently have only EQUALS conditions (or none), but ANY remains supported for documented
// semantics and tests. A row matching no catalog item returns an empty list, a valid "not in catalog" result,
// never a guessed one.
func matchCatalogItems(item pricing.RateCardItem, idx catalogDimensionIndex) []string {
	if len(item.Conditions) == 0 {
		return catalogItemNames(idx.byService[item.Service])
	}
	matches := idx.byService[item.Service]
	groups := map[catalogComponentKind][]pricing.RateCondition{}
	for _, condition := range item.Conditions {
		if kind := catalogDimensionComponentKind(condition.Dimension); kind != "" {
			groups[kind] = append(groups[kind], condition)
			continue
		}
		var candidates map[string]*privatev1.CatalogPricingInputCatalogItem
		if condition.Operator == pricing.OperatorEquals {
			candidates = idx.byValue[[3]string{item.Service, condition.Dimension, condition.Value}]
		} else {
			candidates = idx.anyByDimension[[2]string{item.Service, condition.Dimension}]
		}
		matches = intersectCatalogItems(matches, candidates)
		if len(matches) == 0 {
			return nil
		}
	}
	for kind, conditions := range groups {
		candidates := map[string]*privatev1.CatalogPricingInputCatalogItem{}
		for id, info := range matches {
			components := idx.componentsByItem[[2]string{item.Service, id}][kind]
			if matchCatalogComponents(conditions, components) {
				candidates[id] = info
			}
		}
		matches = candidates
		if len(matches) == 0 {
			return nil
		}
	}
	return catalogItemNames(matches)
}

func catalogDimensionComponentKind(dimension string) catalogComponentKind {
	switch dimension {
	case dimensionAdditionalDiskSize, dimensionAdditionalStorageTier:
		return catalogComponentAdditionalDisk
	case dimensionNodeCount, dimensionBaremetalInstanceType:
		return catalogComponentNodeSet
	default:
		return ""
	}
}

// matchCatalogComponents reports whether one disk or node set satisfies every condition of its kind. A component
// stores at most one scalar value per dimension, so EQUALS conditions on the same dimension must agree; otherwise
// the conditions are contradictory and no requested combination can satisfy them. That check also guards the
// editable shortcut, keeping this matcher consistent with the scalar matcher and the pricing resolver.
func matchCatalogComponents(conditions []pricing.RateCondition, components catalogComponents) bool {
	equalsValues := map[string]string{}
	for _, condition := range conditions {
		if condition.Operator != pricing.OperatorEquals {
			continue
		}
		value, seen := equalsValues[condition.Dimension]
		if seen && value != condition.Value {
			return false
		}
		equalsValues[condition.Dimension] = condition.Value
	}
	if components.editable {
		return true
	}
	for _, component := range components.values {
		matches := true
		for _, condition := range conditions {
			value, present := component[condition.Dimension]
			if !present || (condition.Operator == pricing.OperatorEquals && value != condition.Value) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func catalogItemNames(items map[string]*privatev1.CatalogPricingInputCatalogItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.GetName())
	}
	slices.Sort(names)
	return names
}

// intersectCatalogItems returns the catalog items present in both sets, keyed by id.
func intersectCatalogItems(a, b map[string]*privatev1.CatalogPricingInputCatalogItem,
) map[string]*privatev1.CatalogPricingInputCatalogItem {
	result := map[string]*privatev1.CatalogPricingInputCatalogItem{}
	for id, item := range a {
		if _, ok := b[id]; ok {
			result[id] = item
		}
	}
	return result
}
