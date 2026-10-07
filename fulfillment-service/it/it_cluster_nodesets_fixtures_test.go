/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// testClusterNodeSets is an explicit request fixture for tests unrelated to hardware selection.
// The caller must create the referenced BareMetalInstanceType in the shared tenant.
func testClusterNodeSets(instanceTypeID string, size int32) map[string]*publicv1.ClusterNodeSet {
	return map[string]*publicv1.ClusterNodeSet{
		"my-node-set": publicv1.ClusterNodeSet_builder{
			Size: new(size), BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeID, Shared: true}.Build(),
		}.Build(),
	}
}

func TestClusterNodeSetsSharedHardware(t *testing.T) {
	nodeSets := testClusterNodeSets("shared-instance-type", 3)
	if len(nodeSets) != 1 || nodeSets["my-node-set"] == nil {
		t.Fatalf("unexpected node sets: %v", nodeSets)
	}
	nodeSet := nodeSets["my-node-set"]
	ref := nodeSet.GetBaremetalInstanceType()
	if !ref.GetShared() {
		t.Fatal("bare metal instance type fixture must set shared=true")
	}
	if ref.GetId() != "shared-instance-type" || ref.GetProject() != "" || nodeSet.GetSize() != 3 {
		t.Fatalf("fixture changed hardware identity, project, or size: %v", nodeSet)
	}
}
