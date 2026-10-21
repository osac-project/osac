/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func (s *PrivateClustersServer) prepareUpdatedNodeSetHardware(ctx context.Context, name string,
	existing, nodeSet *privatev1.ClusterNodeSet, mask *fieldmaskpb.FieldMask, sizeOnlyNodeSets bool) error {
	if sizeOnlyNodeSets && existing == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"node_sets.%s.baremetal_instance_type is required when adding a node set with a size-only update", name)
	}
	if existing != nil {
		existingKey := refKey(existing.GetBaremetalInstanceType())
		updatedKey := refKey(nodeSet.GetBaremetalInstanceType())
		if updatedKey != "" && updatedKey != existingKey {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change baremetal_instance_type for node set '%s' from '%s' to '%s': baremetal_instance_type is immutable",
				name, existingKey, updatedKey)
		}
	}
	if existing != nil &&
		(refKey(existing.GetBaremetalInstanceType()) == refKey(nodeSet.GetBaremetalInstanceType()) ||
			(s.isUpdatingOnlySizes(mask) && refKey(nodeSet.GetBaremetalInstanceType()) == "")) {
		// Keep the stored hardware reference when unchanged. Size-only masks
		// also need the stored fabric interface.
		nodeSet.SetBaremetalInstanceType(cloneMessage(existing.GetBaremetalInstanceType()))
		if sizeOnlyNodeSets {
			nodeSet.SetFabricInterface(existing.GetFabricInterface())
		}
		return nil
	}
	ref := nodeSet.GetBaremetalInstanceType()
	if refKey(ref) == "" {
		return nil
	}
	kind := "node_sets." + name + ".baremetal_instance_type"
	resolved, err := getReferenceResource(ctx, s.bareMetalInstanceTypesDao, ref.GetId())
	if err != nil {
		return resourceLookupError(err, kind, ref.GetId(), "", grpccodes.InvalidArgument)
	}
	return validateResourceNotDeleted(kind, refKey(ref), "", resolved.GetMetadata())
}

// restoreMaskedNodeSetFields retains stored fields that a nested map mask did
// not select. The masked map entry in the candidate is only a partial message.
func restoreMaskedNodeSetFields(name string, existing, selected *privatev1.ClusterNodeSet,
	mask *fieldmaskpb.FieldMask) (*privatev1.ClusterNodeSet, error) {
	if existing == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "unknown node set '%s'", name)
	}
	selectedVersion := selected.GetVersion()
	selectedSize, hasSelectedSize := selected.GetSize(), selected.HasSize()
	nodeSet := cloneMessage(existing)
	if updateIncludesField(mask, "spec.node_sets."+name+".version") {
		nodeSet.SetVersion(selectedVersion)
	}
	if updateIncludesField(mask, "spec.node_sets."+name+".size") {
		if hasSelectedSize {
			nodeSet.SetSize(selectedSize)
		} else {
			nodeSet.ClearSize()
		}
	}
	return nodeSet, nil
}

func isUpdatingOnlyNodeSizeOrVersionFields(mask *fieldmaskpb.FieldMask) bool {
	if len(mask.GetPaths()) == 0 {
		return false
	}
	for _, path := range mask.GetPaths() {
		if !strings.HasPrefix(path, "spec.node_sets.") ||
			(!strings.HasSuffix(path, ".size") && !strings.HasSuffix(path, ".version")) {
			return false
		}
	}
	return true
}
