// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"fmt"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ClusterSpec selects the prepared catalog and requests workers from one inventory group.
// Requested capacity must be positive and fit within the group's original hardware.
func ClusterSpec(group InventoryGroup, workers int32) (*publicv1.ClusterSpec, error) {
	if workers <= 0 || int(workers) > len(group.Inventory.Hosts) {
		return nil, fmt.Errorf("inventory group %s has %d hosts for %d requested workers",
			group.Name, len(group.Inventory.Hosts), workers)
	}

	return publicv1.ClusterSpec_builder{
		Template: publicv1.ClusterTemplateReference_builder{Id: clusterTemplateID}.Build(),
		Version:  publicv1.ClusterVersionReference_builder{Name: clusterVersionName, Tenant: "shared"}.Build(),
		NodeSets: map[string]*publicv1.ClusterNodeSet{
			"compute": publicv1.ClusterNodeSet_builder{
				Size: new(workers),
				BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Name: group.InstanceTypeName, Tenant: "shared",
				}.Build(),
			}.Build(),
		},
	}.Build(), nil
}
