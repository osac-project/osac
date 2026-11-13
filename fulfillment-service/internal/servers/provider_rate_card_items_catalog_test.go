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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Provider rate card catalog component matching", func() {
	equals := func(dimension, value string) pricing.RateCondition {
		return pricing.RateCondition{Dimension: dimension, Operator: pricing.OperatorEquals, Value: value}
	}
	any := func(dimension string) pricing.RateCondition {
		return pricing.RateCondition{Dimension: dimension, Operator: pricing.OperatorAny}
	}
	disk := func(size, tier string) map[string]string {
		return map[string]string{dimensionAdditionalDiskSize: size, dimensionAdditionalStorageTier: tier}
	}
	nodeSet := func(count, instanceType string) map[string]string {
		return map[string]string{dimensionNodeCount: count, dimensionBaremetalInstanceType: instanceType}
	}
	buildInventory := func() catalogPricingInventory {
		inventory := catalogPricingInventory{
			itemsByService:   map[string]map[string]*privatev1.CatalogPricingInputCatalogItem{},
			componentsByItem: map[[2]string]map[catalogComponentKind]catalogComponents{},
		}
		add := func(service, name, instanceType string, kind catalogComponentKind, components catalogComponents) {
			info := catalogItemInfo(name, name, "", true)
			if inventory.itemsByService[service] == nil {
				inventory.itemsByService[service] = map[string]*privatev1.CatalogPricingInputCatalogItem{}
			}
			inventory.itemsByService[service][name] = info
			inventory.componentsByItem[[2]string{service, name}] = map[catalogComponentKind]catalogComponents{kind: components}
			if instanceType != "" {
				inventory.inputs = append(inventory.inputs, privatev1.CatalogPricingInput_builder{
					Service: service, Dimension: dimensionInstanceType, Value: instanceType,
					CatalogItems: []*privatev1.CatalogPricingInputCatalogItem{info},
				}.Build())
			}
		}
		add(serviceComputeInstance, "disks-split", "small", catalogComponentAdditionalDisk,
			catalogComponents{values: []map[string]string{disk("50", "slow"), disk("100", "fast")}})
		add(serviceComputeInstance, "disk-paired", "small", catalogComponentAdditionalDisk,
			catalogComponents{values: []map[string]string{disk("50", "fast")}})
		add(serviceComputeInstance, "disk-large", "large", catalogComponentAdditionalDisk,
			catalogComponents{values: []map[string]string{disk("50", "fast")}})
		add(serviceComputeInstance, "disks-editable", "small", catalogComponentAdditionalDisk,
			catalogComponents{editable: true})
		add(serviceComputeInstance, "disks-empty", "small", catalogComponentAdditionalDisk, catalogComponents{})
		add(serviceComputeInstance, "disks-partial", "small", catalogComponentAdditionalDisk,
			catalogComponents{values: []map[string]string{
				{dimensionAdditionalDiskSize: "50"}, {dimensionAdditionalStorageTier: "fast"},
			}})
		add(serviceComputeInstance, "no-dimensions", "", "", catalogComponents{})
		add(serviceCluster, "nodes-split", "", catalogComponentNodeSet,
			catalogComponents{values: []map[string]string{nodeSet("3", "type-a"), nodeSet("5", "type-b")}})
		add(serviceCluster, "nodes-paired", "", catalogComponentNodeSet,
			catalogComponents{values: []map[string]string{nodeSet("3", "type-b")}})
		add(serviceCluster, "nodes-editable", "", catalogComponentNodeSet, catalogComponents{editable: true})
		add(serviceCluster, "no-dimensions", "", "", catalogComponents{})
		return inventory
	}

	DescribeTable("matches every condition within its scalar or component group",
		func(service string, conditions []pricing.RateCondition, expected []string) {
			index := buildCatalogDimensionIndex(buildInventory())
			matches := matchCatalogItems(pricing.RateCardItem{Service: service, Conditions: conditions}, index)
			if len(expected) == 0 {
				Expect(matches).To(BeEmpty())
			} else {
				Expect(matches).To(Equal(expected))
			}
		},
		Entry("does not combine size and tier from different disks", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "50"), equals(dimensionAdditionalStorageTier, "fast")},
			[]string{"disk-large", "disk-paired", "disks-editable"}),
		Entry("a single size condition matches either disk", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "50")},
			[]string{"disk-large", "disk-paired", "disks-editable", "disks-partial", "disks-split"}),
		Entry("editable disks accept values absent from defaults", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "999"), equals(dimensionAdditionalStorageTier, "custom")},
			[]string{"disks-editable"}),
		Entry("does not combine count and type from different node sets", serviceCluster,
			[]pricing.RateCondition{equals(dimensionNodeCount, "3"), equals(dimensionBaremetalInstanceType, "type-b")},
			[]string{"nodes-editable", "nodes-paired"}),
		Entry("a single count condition matches either node set", serviceCluster,
			[]pricing.RateCondition{equals(dimensionNodeCount, "3")},
			[]string{"nodes-editable", "nodes-paired", "nodes-split"}),
		Entry("editable node sets accept values absent from defaults", serviceCluster,
			[]pricing.RateCondition{equals(dimensionNodeCount, "99"), equals(dimensionBaremetalInstanceType, "custom")},
			[]string{"nodes-editable"}),
		Entry("scalar and disk groups intersect", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "50"), equals(dimensionInstanceType, "small"),
				equals(dimensionAdditionalStorageTier, "fast")},
			[]string{"disk-paired", "disks-editable"}),
		Entry("an editable component does not override scalar restrictions", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionInstanceType, "missing"), equals(dimensionAdditionalDiskSize, "50")},
			[]string{}),
		Entry("scalar ANY intersects the component group", serviceComputeInstance,
			[]pricing.RateCondition{any(dimensionInstanceType), equals(dimensionAdditionalDiskSize, "50"),
				equals(dimensionAdditionalStorageTier, "fast")},
			[]string{"disk-large", "disk-paired", "disks-editable"}),
		Entry("component ANY requires a value on the same disk", serviceComputeInstance,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "50"), any(dimensionAdditionalStorageTier)},
			[]string{"disk-large", "disk-paired", "disks-editable", "disks-split"}),
		Entry("component ANY requires a value on the same node set", serviceCluster,
			[]pricing.RateCondition{any(dimensionNodeCount), equals(dimensionBaremetalInstanceType, "type-b")},
			[]string{"nodes-editable", "nodes-paired", "nodes-split"}),
		Entry("conditionless compute rows include dimensionless items", serviceComputeInstance, []pricing.RateCondition{},
			[]string{"disk-large", "disk-paired", "disks-editable", "disks-empty", "disks-partial", "disks-split", "no-dimensions"}),
		Entry("conditionless cluster rows stay within their service", serviceCluster, []pricing.RateCondition{},
			[]string{"no-dimensions", "nodes-editable", "nodes-paired", "nodes-split"}),
		Entry("components do not cross service boundaries", serviceCluster,
			[]pricing.RateCondition{equals(dimensionAdditionalDiskSize, "50")}, []string{}),
		Entry("unknown services have no conditionless matches", "unknown", []pricing.RateCondition{}, []string{}),
	)
})
