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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// catalogItem is implemented by ClusterCatalogItem, ComputeInstanceCatalogItem,
// and BareMetalInstanceCatalogItem.
type catalogItem interface {
	GetPublished() bool
	GetMetadata() *privatev1.Metadata
}

// validateCatalogItemForCreation allows a new resource to use only a published, active Catalog
// Item. The DAO lookup has already limited the item to what this caller may see.
func validateCatalogItemForCreation(item catalogItem, ref string) error {
	if item.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog item '%s' has been deleted", ref)
	}
	if !item.GetPublished() {
		return grpcstatus.Errorf(grpccodes.NotFound,
			"catalog item '%s' is not published", ref)
	}
	return nil
}

// preserveCatalogItemProvenance keeps an object's original spec.catalog_item reference. Updates
// compare any supplied ID, name, and scope with the stored reference without fetching the Catalog
// Item, which may have been deleted. An ID-only match is accepted; changing or clearing the
// reference is rejected.
func preserveCatalogItemProvenance[T interface {
	fullResourceReference
	proto.Message
}](current, candidate T, mask *fieldmaskpb.FieldMask) (T, error) {
	if proto.Equal(current, candidate) || (mask == nil && !candidate.ProtoReflect().IsValid()) {
		return cloneMessage(current), nil
	}
	if !current.ProtoReflect().IsValid() || !candidate.ProtoReflect().IsValid() ||
		candidate.GetId() != current.GetId() ||
		(candidate.GetName() != "" && candidate.GetName() != current.GetName()) ||
		(candidate.GetProject() != "" && candidate.GetProject() != current.GetProject()) ||
		(candidate.GetShared() && !current.GetShared()) {
		return candidate, grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable", refKey(current), refKey(candidate))
	}
	// A false shared flag has no protobuf presence; a mask targeting that flag makes it explicit.
	for _, path := range mask.GetPaths() {
		if (path == "spec.catalog_item.shared" && candidate.GetShared() != current.GetShared()) ||
			(path == "spec.catalog_item.project" && candidate.GetProject() != current.GetProject()) ||
			(path == "spec.catalog_item.name" && candidate.GetName() != current.GetName()) {
			return candidate, grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable", refKey(current), refKey(candidate))
		}
	}
	return cloneMessage(current), nil
}
