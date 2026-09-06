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
	"fmt"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

// tenantMetadataObject is satisfied by any protobuf resource that exposes a Metadata message
// containing a tenant field.
type tenantMetadataObject interface {
	GetMetadata() *privatev1.Metadata
}

// resolveObjectTenant returns the tenant that should be used for cross-tenant validation.
// It prefers the explicit metadata.tenant field on the object. When that is empty it
// falls back to TenancyLogic.DetermineDefaultTenant — which, for the private API, returns
// the system tenant. Callers on the private API side should always set metadata.tenant
// before calling this.
func resolveObjectTenant(ctx context.Context, obj tenantMetadataObject, tl auth.TenancyLogic) (string, error) {
	if obj != nil && obj.GetMetadata() != nil && obj.GetMetadata().GetTenant() != "" {
		return obj.GetMetadata().GetTenant(), nil
	}
	tenant, err := tl.DetermineDefaultTenant(ctx)
	if err != nil {
		return "", grpcstatus.Errorf(grpccodes.Internal, "failed to determine target tenant")
	}
	return tenant, nil
}

// validateTenantMatch checks that referencedTenant matches parentTenant exactly.
// It returns an InvalidArgument gRPC error on mismatch.
//
// Use this when the referenced resource MUST belong to the same tenant as the
// parent resource. For resources that may also come from the shared tenant, use
// validateTenantOrShared instead.
func validateTenantMatch(parentTenant, referencedTenant, kind, id string) error {
	if referencedTenant != parentTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' belongs to tenant '%s', but the parent resource belongs to tenant '%s': "+
				"cross-tenant references are not allowed",
			kind, id, referencedTenant, parentTenant)
	}
	return nil
}

// validateTenantOrShared checks that referencedTenant matches either parentTenant
// or the shared tenant. This is appropriate for references to resources like
// ExternalIPPools which may be shared across tenants.
func validateTenantOrShared(parentTenant, referencedTenant, kind, id string) error {
	if referencedTenant != parentTenant && referencedTenant != auth.SharedTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' belongs to tenant '%s', but the parent resource belongs to tenant '%s': "+
				"cross-tenant references are not allowed (shared tenant resources are permitted)",
			kind, id, referencedTenant, parentTenant)
	}
	return nil
}

// fetchAndValidateTenantMatch is a convenience wrapper that fetches a referenced
// resource's tenant from its metadata and validates it matches the expected tenant.
// The referencedObj must have already been fetched from the DAO.
func fetchAndValidateTenantMatch(parentTenant string, referencedObj tenantMetadataObject, kind, id string) error {
	if referencedObj == nil || referencedObj.GetMetadata() == nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to validate tenant for %s '%s': object has no metadata", kind, id)
	}
	referencedTenant := referencedObj.GetMetadata().GetTenant()
	return validateTenantMatch(parentTenant, referencedTenant, kind, id)
}

// fetchAndValidateTenantOrShared is like fetchAndValidateTenantMatch but also
// allows the referenced resource to be in the shared tenant.
func fetchAndValidateTenantOrShared(parentTenant string, referencedObj tenantMetadataObject, kind, id string) error {
	if referencedObj == nil || referencedObj.GetMetadata() == nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to validate tenant for %s '%s': object has no metadata", kind, id)
	}
	referencedTenant := referencedObj.GetMetadata().GetTenant()
	return validateTenantOrShared(parentTenant, referencedTenant, kind, id)
}

// crossTenantError formats a descriptive error for cross-tenant reference violations.
// It is a lower-level helper used by the validate* functions above.
func crossTenantError(kind, id, referencedTenant, parentTenant string) error {
	return fmt.Errorf(
		"%s '%s' belongs to tenant '%s', but the parent resource belongs to tenant '%s'",
		kind, id, referencedTenant, parentTenant)
}
