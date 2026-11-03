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

	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// computeInstanceServiceName is the OSAC service name VMaaS compute instances are billed under. It matches the
// design's own rate-card example (§4.3) and the service name osac-metering already sends for this dimension.
const computeInstanceServiceName = "compute_instance"

// catalogDimensionIndex groups OSAC-6889's aggregated (service, dimension, value) catalog pricing inventory so a
// rate-card row's conditions can be matched against it without re-deriving the field-policy walk
// aggregateCatalogPricingInputs already performs over the VMaaS/CaaS catalog item DAOs.
type catalogDimensionIndex struct {
	// byValue maps (service, dimension, value) to the catalog items locked to, or defaulted to, exactly that
	// value.
	byValue map[[3]string]map[string]*privatev1.CatalogPricingInputCatalogItem
	// anyByDimension maps (service, dimension) to every catalog item that has any value for that dimension, open
	// or fixed: the union of catalog items across all of that dimension's rows.
	anyByDimension map[[2]string]map[string]*privatev1.CatalogPricingInputCatalogItem
}

// buildCatalogDimensionIndex indexes the full (service, dimension, value) -> catalog-items inventory OSAC-6889's
// aggregateCatalogPricingInputs computes, for AC5/AC6 catalog cross-reference across all five documented dimensions
// (instance_type, additional_disk_size_gib, additional_storage_tier, baremetal_instance_type, node_count).
func buildCatalogDimensionIndex(inputs []*privatev1.CatalogPricingInput) catalogDimensionIndex {
	idx := catalogDimensionIndex{
		byValue:        map[[3]string]map[string]*privatev1.CatalogPricingInputCatalogItem{},
		anyByDimension: map[[2]string]map[string]*privatev1.CatalogPricingInputCatalogItem{},
	}
	for _, input := range inputs {
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

// matchCatalogItems evaluates every one of a rate-card row's conditions together (AC6: "all conditions on a row
// apply together") against idx, returning the names of the catalog items satisfying all of them. An EQUALS
// condition matches catalog items locked or defaulted to that exact value; an ANY condition matches catalog items
// that have any value for that dimension, open or fixed. A row matching no catalog item returns an empty list,
// which is itself a valid "not in catalog" result (AC6), never a guessed one.
func matchCatalogItems(item pricing.RateCardItem, idx catalogDimensionIndex) []string {
	if len(item.Conditions) == 0 {
		return nil
	}
	var matches map[string]*privatev1.CatalogPricingInputCatalogItem
	for i, condition := range item.Conditions {
		var candidates map[string]*privatev1.CatalogPricingInputCatalogItem
		if condition.Operator == pricing.OperatorEquals {
			candidates = idx.byValue[[3]string{item.Service, condition.Dimension, condition.Value}]
		} else {
			candidates = idx.anyByDimension[[2]string{item.Service, condition.Dimension}]
		}
		if i == 0 {
			matches = candidates
		} else {
			matches = intersectCatalogItems(matches, candidates)
		}
		if len(matches) == 0 {
			return nil
		}
	}
	names := make([]string, 0, len(matches))
	for _, candidate := range matches {
		names = append(names, candidate.GetName())
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
