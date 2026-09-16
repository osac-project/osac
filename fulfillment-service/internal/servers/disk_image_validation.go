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
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateDiskImageState checks an image named in Template spec_defaults. The caller-visible
// lookup prefers preferredTenant when a name exists in several tenants, then falls back to
// shared; an ID identifies one image directly. It returns a warning for a deprecated image,
// an error for an obsolete or missing image, and no result for an empty key. source names the
// referencing field in errors, such as " in spec_defaults".
func validateDiskImageState(
	ctx context.Context,
	diskImagesDao *dao.GenericDAO[*privatev1.DiskImage],
	key string,
	preferredTenant string,
	source string,
) (*privatev1.DiskImage, []string, error) {
	if key == "" {
		return nil, nil, nil
	}
	diskImage, err := resolveDiskImage(ctx, diskImagesDao, key, preferredTenant, source)
	if err != nil {
		return nil, nil, err
	}
	warnings, err := validateResolvedDiskImage(diskImage, key, source)
	return diskImage, warnings, err
}

// resolveDiskImage preserves the established name precedence used by Compute
// resources: the preferred tenant wins, followed by shared, otherwise duplicate
// visible names are ambiguous. Globally unique IDs resolve directly.
func resolveDiskImage(
	ctx context.Context,
	diskImagesDao *dao.GenericDAO[*privatev1.DiskImage],
	key string,
	preferredTenant string,
	source string,
) (*privatev1.DiskImage, error) {
	response, err := diskImagesDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return nil, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return nil, grpcstatus.Errorf(grpccodes.Internal,
			"failed to retrieve disk image '%s'", key)
	}

	var diskImage *privatev1.DiskImage
	switch response.GetTotal() {
	case 0:
		return nil, grpcstatus.Errorf(grpccodes.NotFound,
			"disk image '%s'%s not found", key, source)
	case 1:
		diskImage = response.GetItems()[0]
	default:
		// The name resolved to multiple disk images; break the tie by tenant precedence.
		diskImage, err = resolvePreferredDiskImage(ctx, diskImagesDao, key, preferredTenant, source)
		if err != nil {
			return nil, err
		}
	}
	return diskImage, nil
}

// resolveDiskImageReference finds the image named by a Catalog policy or resource.
// If acme/apps and shared/apps both have "fedora", {name: "fedora"} prefers acme, while
// {name: "fedora", shared: true} selects shared. Use project when the shared image is in a
// different project. An ID selects a caller-visible image directly.
// The image must belong to the owner tenant or shared tenant; callers check its lifecycle and
// fill the stored reference after this function returns.
func resolveDiskImageReference(ctx context.Context, resourceDao *dao.GenericDAO[*privatev1.DiskImage], scope referenceScope, ref *privatev1.DiskImageReference, source string) (*privatev1.DiskImage, error) {
	var err error
	var resolved *privatev1.DiskImage
	if ref.GetId() == "" && ref.GetName() != "" && !ref.GetShared() && ref.GetProject() == "" {
		resolved, err = resolveDiskImage(
			ctx, resourceDao, ref.GetName(), scope.tenant, source,
		)
		if err != nil {
			return nil, err
		}
	} else {
		resolved, err = resolveFullResourceReference(
			ctx, resourceDao, scope, ref, "disk image", source, grpccodes.NotFound,
		)
	}
	if err != nil {
		return nil, err
	}
	if err := validateDependencyOwnerScope(scope, resolved.GetMetadata(), "disk image", source); err != nil {
		return nil, err
	}
	return resolved, nil
}

// resolveLockedDiskImageReference applies the same name and scope rules while holding
// an exclusive lock on the selected image until the request transaction ends.
func resolveLockedDiskImageReference(ctx context.Context, resourceDao *dao.GenericDAO[*privatev1.DiskImage], scope referenceScope, ref *privatev1.DiskImageReference, source string) (*privatev1.DiskImage, error) {
	var err error
	var resolved *privatev1.DiskImage
	if ref.GetId() == "" && ref.GetName() != "" && !ref.GetShared() && ref.GetProject() == "" {
		resolved, err = resolveDiskImage(ctx, resourceDao, ref.GetName(), scope.tenant, source)
		if err != nil {
			return nil, err
		}
		resolved, err = getLockedReferenceResource(ctx, resourceDao, resolved.GetId())
		if err != nil {
			return nil, resourceLookupError(err, "disk image", ref.GetName(), source, grpccodes.NotFound)
		}
	} else {
		resolved, err = resolveLockedFullResourceReference(
			ctx, resourceDao, scope, ref, "disk image", source, grpccodes.NotFound,
		)
		if err != nil {
			return nil, err
		}
	}
	if err := validateDependencyOwnerScope(scope, resolved.GetMetadata(), "disk image", source); err != nil {
		return nil, err
	}
	return resolved, nil
}

// validateResolvedDiskImage validates lifecycle state and returns any deprecation warning for an existing image.
func validateResolvedDiskImage(diskImage *privatev1.DiskImage, key, source string) ([]string, error) {
	if err := validateResourceNotDeleted("disk image", key, source, diskImage.GetMetadata()); err != nil {
		return nil, err
	}
	lifecycle := diskImage.GetSpec().GetLifecycle()
	var warnings []string

	switch lifecycle {
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE:
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"disk image '%s'%s is obsolete and cannot be used", key, source)
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED:
		warning := fmt.Sprintf("Disk image '%s'%s is deprecated", key, source)
		dep := diskImage.GetSpec().GetDeprecation()
		if dep != nil && dep.GetObsolescenceTimestamp() != nil {
			warning += fmt.Sprintf(" and will become obsolete on %s",
				dep.GetObsolescenceTimestamp().AsTime().Format(time.RFC3339))
		}
		warnings = append(warnings, warning)
	}

	return warnings, nil
}

// resolvePreferredDiskImage chooses between visible images with the same name: first the
// preferred tenant, then shared. If neither owns one, the name remains ambiguous. For example,
// an admin who can see "fedora" in two unrelated tenants must use an ID rather than that name alone.
func resolvePreferredDiskImage(
	ctx context.Context,
	diskImagesDao *dao.GenericDAO[*privatev1.DiskImage],
	name string,
	preferredTenant string,
	source string,
) (*privatev1.DiskImage, error) {
	tenants := make([]string, 0, 2)
	for _, tenant := range []string{preferredTenant, auth.SharedTenant} {
		if tenant != "" && !slices.Contains(tenants, tenant) {
			tenants = append(tenants, tenant)
		}
	}

	for _, tenant := range tenants {
		response, err := diskImagesDao.List().
			SetFilter(fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s",
				strconv.Quote(name), strconv.Quote(tenant))).
			SetLimit(1).
			Do(ctx)
		if err != nil {
			var deniedErr *dao.ErrDenied
			if errors.As(err, &deniedErr) {
				return nil, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
			}
			return nil, grpcstatus.Errorf(grpccodes.Internal,
				"failed to retrieve disk image '%s'", name)
		}
		if response.GetTotal() >= 1 {
			return response.GetItems()[0], nil
		}
	}

	return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
		"there are multiple disk images with identifier or name '%s'%s", name, source)
}
