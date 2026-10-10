/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
)

// TenancyLogic defines the logic for determining object tenancy and access control.
//
//go:generate mockgen -destination=tenancy_logic_mock.go -package=auth . TenancyLogic
type TenancyLogic interface {
	// DetermineAssignableTenants calculates and returns the list of tenant names that can be assigned to an object
	// that is being created or updated. This should be a superset of the default tenants.
	DetermineAssignableTenants(ctx context.Context) (collections.Set[string], error)

	// DetermineDefaultTenant returns the tenant name that is assigned by default when an object is created
	// without an explicit tenant in the request.
	DetermineDefaultTenant(ctx context.Context) (string, error)

	// DetermineVisibility calculates and returns the visibility of the current user.
	DetermineVisibility(ctx context.Context) (*Visibility, error)
}

// SystemTenant is the tenant that is assigned to objects that are only visible to the system.
const SystemTenant = "system"

// SystemTenants is the set of tenants that are assigned to objects that are only visible to the system.
var SystemTenants = collections.NewSet(SystemTenant)

// SharedTenant is the tenant that is always visible to all users.
const SharedTenant = "shared"

// SharedTenants is the set of tenants that are always visible to all users.
var SharedTenants = collections.NewSet(SharedTenant)

// AllTenants is the set of all tenants that are possible.
var AllTenants = collections.NewUniversalSet[string]()

// DefaultAllowedTenants is the set of tenants where objects can normally be created. It excludes
// the system and shared tenants, which are reserved for platform-level concerns. Servers that
// manage platform-scoped resources should opt in to the shared tenant explicitly.
var DefaultAllowedTenants = AllTenants.Difference(collections.NewSet(SystemTenant, SharedTenant))

// DetermineTenantForOperation determines which tenant will be assigned to a resource based on:
//   - requestedTenant: the tenant explicitly requested (from metadata)
//   - currentTenant: the tenant currently assigned (for updates, empty for creates)
//
// Logic (mirrors GenericServer.determineAssignedTenant):
//  1. If requestedTenant is specified → validate and return it
//  2. Else if currentTenant exists → return it (preserves tenant on update)
//  3. Else → determine and return the default tenant
//
// This shared logic is used by:
//   - GenericServer.determineAssignedTenant() for actual resource operations
//   - PrivateSelfSubjectAccessReviewsServer.Create() for permission checks
//
// Returns the determined tenant or an error.
func DetermineTenantForOperation(ctx context.Context, tenancyLogic TenancyLogic, requestedTenant, currentTenant string) (string, error) {
	// If a tenant was explicitly requested, validate and use it
	if requestedTenant != "" {
		if err := ValidateTenantAssignment(ctx, tenancyLogic, requestedTenant); err != nil {
			return "", err
		}
		return requestedTenant, nil
	}

	// For updates, preserve the current tenant if no new one was requested
	if currentTenant != "" {
		return currentTenant, nil
	}

	// For creates with no tenant specified, use the default tenant
	defaultTenant, err := tenancyLogic.DetermineDefaultTenant(ctx)
	if err != nil {
		return "", err
	}
	return defaultTenant, nil
}

// ValidateTenantAssignment validates whether a user can assign a specific tenant to a resource.
// It checks both visibility (whether the tenant exists from the user's perspective) and
// assignability (whether the user is a member of that tenant).
//
// Returns nil if the tenant is valid and can be assigned, or an error describing why not.
func ValidateTenantAssignment(ctx context.Context, tenancyLogic TenancyLogic, requestedTenant string) error {
	if requestedTenant == "" {
		return nil
	}

	// Check if the tenant is visible to the user
	visibility, err := tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		return err
	}
	if !visibility.IsTenantVisible(requestedTenant) {
		return &TenantInvisibleError{Tenant: requestedTenant}
	}

	// Check if the tenant is assignable (user is a member)
	assignableTenants, err := tenancyLogic.DetermineAssignableTenants(ctx)
	if err != nil {
		return err
	}
	if !assignableTenants.Contains(requestedTenant) {
		return &TenantUnassignableError{Tenant: requestedTenant}
	}

	return nil
}

// TenantInvisibleError indicates that a tenant doesn't exist from the user's perspective.
type TenantInvisibleError struct {
	Tenant string
}

func (e *TenantInvisibleError) Error() string {
	return "tenant '" + e.Tenant + "' doesn't exist"
}

// TenantUnassignableError indicates that a user is not a member of the requested tenant.
type TenantUnassignableError struct {
	Tenant string
}

func (e *TenantUnassignableError) Error() string {
	return "tenant '" + e.Tenant + "' can't be assigned"
}
