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
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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
	if err := validateImmutableReferenceIdentity(current, candidate, "spec.catalog_item", "catalog item", false); err != nil {
		return candidate, err
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

// catalogItemResource combines the catalogItem interface with the constraints needed for
// DAO operations and reference resolution. All three concrete catalog item types
// (ComputeInstanceCatalogItem, ClusterCatalogItem, BareMetalInstanceCatalogItem)
// satisfy this interface.
type catalogItemResource interface {
	catalogItem
	dao.Object
}

// resolveCatalogItemByName preserves the established name precedence used by resource
// creation: the preferred tenant wins, followed by shared, otherwise duplicate visible
// names are ambiguous. Globally unique IDs resolve directly.
//
// This follows the same visibility-based List approach used by resolveDiskImage in
// disk_image_validation.go: the DAO list filter does not constrain the tenant, so
// items in any caller-visible tenant (including shared) can match. When multiple
// items share the same name, resolvePreferredCatalogItem applies tenant precedence.
func resolveCatalogItemByName[O catalogItemResource](
	ctx context.Context,
	catalogItemDao *dao.GenericDAO[O],
	key string,
	preferredTenant string,
	source string,
) (O, error) {
	var zero O
	response, err := catalogItemDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return zero, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return zero, grpcstatus.Errorf(grpccodes.Internal,
			"failed to retrieve catalog item '%s'", key)
	}

	switch response.GetTotal() {
	case 0:
		return zero, grpcstatus.Errorf(grpccodes.NotFound,
			"catalog item '%s'%s not found", key, source)
	case 1:
		item := response.GetItems()[0]
		// The DAO list uses caller visibility, which for an admin includes
		// every tenant. Restrict the result to the preferred tenant or
		// shared so that a name (or ID) from an unrelated tenant is never
		// returned.
		itemTenant := item.GetMetadata().GetTenant()
		if itemTenant != preferredTenant && itemTenant != auth.SharedTenant {
			return zero, grpcstatus.Errorf(grpccodes.NotFound,
				"catalog item '%s'%s not found", key, source)
		}
		return item, nil
	default:
		// The name resolved to multiple catalog items; break the tie by tenant precedence.
		return resolvePreferredCatalogItem(ctx, catalogItemDao, key, preferredTenant, source)
	}
}

// resolvePreferredCatalogItem chooses between visible catalog items with the same name:
// first the preferred tenant, then shared. If neither owns one, the name remains ambiguous.
// This mirrors resolvePreferredDiskImage from disk_image_validation.go.
func resolvePreferredCatalogItem[O catalogItemResource](
	ctx context.Context,
	catalogItemDao *dao.GenericDAO[O],
	name string,
	preferredTenant string,
	source string,
) (O, error) {
	var zero O
	tenants := make([]string, 0, 2)
	for _, tenant := range []string{preferredTenant, auth.SharedTenant} {
		if tenant != "" && !slices.Contains(tenants, tenant) {
			tenants = append(tenants, tenant)
		}
	}

	for _, tenant := range tenants {
		response, err := catalogItemDao.List().
			SetFilter(fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s",
				strconv.Quote(name), strconv.Quote(tenant))).
			SetLimit(1).
			Do(ctx)
		if err != nil {
			var deniedErr *dao.ErrDenied
			if errors.As(err, &deniedErr) {
				return zero, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
			}
			return zero, grpcstatus.Errorf(grpccodes.Internal,
				"failed to retrieve catalog item '%s'", name)
		}
		if response.GetTotal() >= 1 {
			return response.GetItems()[0], nil
		}
	}

	return zero, grpcstatus.Errorf(grpccodes.InvalidArgument,
		"there are multiple catalog items with identifier or name '%s'%s", name, source)
}

// resolveAndLockCatalogItemReference detects whether the reference uses only a name
// (no ID, no explicit shared flag, no explicit project) and, when it does, resolves
// via the visibility-based resolveCatalogItemByName helper so that shared-tenant
// catalog items are found from any tenant. For all other reference shapes (ID,
// explicit shared, explicit project) it falls back to the existing scoped
// resolveAndCanonicalizeLockedReference path.
//
// After resolution the reference is canonicalized (id, name, shared, project filled)
// and the returned object is held under an exclusive row lock until the request
// transaction ends.
func resolveAndLockCatalogItemReference[O catalogItemResource](
	ctx context.Context,
	catalogItemDao *dao.GenericDAO[O],
	ownerMetadata *privatev1.Metadata,
	ref fullResourceReference,
) (O, error) {
	if ref.GetId() == "" && ref.GetName() != "" && !ref.GetShared() && ref.GetProject() == "" {
		resolved, err := resolveCatalogItemByName(ctx, catalogItemDao, ref.GetName(), ownerMetadata.GetTenant(), "")
		if err != nil {
			var zero O
			return zero, err
		}
		locked, lockErr := getLockedReferenceResource(ctx, catalogItemDao, resolved.GetId())
		if lockErr != nil {
			var zero O
			return zero, resourceLookupError(lockErr, "catalog item", ref.GetName(), "", grpccodes.NotFound)
		}
		ref.SetId(locked.GetId())
		ref.SetName(locked.GetMetadata().GetName())
		ref.SetShared(locked.GetMetadata().GetTenant() == auth.SharedTenant)
		ref.SetProject(locked.GetMetadata().GetProject())
		return locked, nil
	}
	return resolveAndCanonicalizeLockedReference(ctx, catalogItemDao, ownerMetadata, ref, "catalog item", grpccodes.NotFound)
}
