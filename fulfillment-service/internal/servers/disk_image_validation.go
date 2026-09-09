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

// validateDiskImageState looks up a DiskImage by id or name through the tenant-filtered DAO and
// validates its lifecycle. It returns the resolved DiskImage (callers such as the ComputeInstance
// handler backfill id/name onto the stored reference), a warning for DEPRECATED images, an error
// for OBSOLETE or not-found images, and (nil, nil, nil) when key is empty. source adds context to
// error messages (e.g. " in field_definitions"); pass "" when validating directly on a
// ComputeInstance. Shared by the ComputeInstance, ComputeInstanceTemplate, and CatalogItem servers,
// mirroring validateInstanceTypeState.
//
// Names are unique only per tenant, so a lookup by name may match several rows (a shared image plus
// same-name tenant images). preferredTenant breaks the tie: the preferred-tenant image wins, then
// the shared image, otherwise the ambiguity is an InvalidArgument. Callers pass their default tenant
// (own tenant for a tenant-scoped caller, shared for an admin; see
// auth.TenancyLogic.DetermineDefaultTenant). A key that is an id matches at most one row, so callers
// that always pass an id (ComputeInstance, ComputeInstanceTemplate) can pass an empty preferredTenant.
//
// Error codes follow the instance_type / ComputeInstance handlers: NotFound for missing,
// FailedPrecondition for OBSOLETE, a warning for DEPRECATED. A cross-tenant reference resolves to
// zero rows under the DAO's tenancy filter and collapses into the not-found case, avoiding any leak
// of cross-tenant existence.
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

	response, err := diskImagesDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return nil, nil, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return nil, nil, grpcstatus.Errorf(grpccodes.Internal,
			"failed to retrieve disk image '%s'", key)
	}

	var diskImage *privatev1.DiskImage
	switch response.GetTotal() {
	case 0:
		return nil, nil, grpcstatus.Errorf(grpccodes.NotFound,
			"disk image '%s'%s not found", key, source)
	case 1:
		diskImage = response.GetItems()[0]
	default:
		// The name resolved to multiple disk images; break the tie by tenant precedence.
		diskImage, err = resolvePreferredDiskImage(ctx, diskImagesDao, key, preferredTenant, source)
		if err != nil {
			return nil, nil, err
		}
	}

	lifecycle := diskImage.GetSpec().GetLifecycle()
	var warnings []string

	switch lifecycle {
	case privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE:
		return nil, nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
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

	return diskImage, warnings, nil
}

// resolvePreferredDiskImage breaks a disk-image name collision deterministically. Names are unique
// only per tenant, so a shared image and one or more same-name tenant images can coexist. The image
// owned by preferredTenant wins; failing that, the shared image; failing that, the collision is a
// genuine ambiguity (e.g. a provider admin naming a name held by several tenants but by no shared
// image) and is reported as InvalidArgument. Each candidate is fetched with an explicit tenant
// filter (on top of the DAO's own tenancy filter) so the choice is exact regardless of how many
// tenants share the name. An empty preferredTenant yields no candidate tenants and falls straight
// through to the ambiguity error.
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
