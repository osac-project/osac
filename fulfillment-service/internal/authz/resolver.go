/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package authz

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Subject identifies one authenticated RoleBinding subject and its verified tenants.
type Subject struct {
	Username         string
	ServiceAccountID string
	Tenants          collections.Set[string]
}

// BindingGrant records the binding and Role that supplied a tenant's methods.
type BindingGrant struct {
	BindingID  string
	RoleID     string
	RoleName   string
	RoleTenant string
	Rules      []*privatev1.PermissionRule
}

// Grants contains expanded methods for one tenant and their provenance.
type Grants struct {
	Tenant   string
	Methods  []string
	Bindings []BindingGrant
}

// Store streams grants for a verified subject. User lookups resolve live tenant User rows
// by username. Service-account IDs must already be validated against live account state for
// this request. Both methods return live bindings in tenant with live Roles in tenant or
// shared, ordered by binding ID.
type Store interface {
	ListUserGrants(context.Context, string, string) iter.Seq2[BindingGrant, error]
	ListServiceAccountGrants(context.Context, string, string) iter.Seq2[BindingGrant, error]
}

// Resolver reads subject grants and expands their permission rules.
type Resolver struct {
	store   Store
	catalog *catalog.Catalog
}

// NewResolver creates a resolver reading from store and expanding methods through permissionCatalog.
func NewResolver(store Store, permissionCatalog *catalog.Catalog) (*Resolver, error) {
	if store == nil || permissionCatalog == nil {
		return nil, errors.New("store and catalog are required")
	}
	return &Resolver{store: store, catalog: permissionCatalog}, nil
}

// Resolve computes grants for one verified subject in one tenant. Any database or catalog error discards the result.
func (r *Resolver) Resolve(ctx context.Context, tenant string, subject Subject) (*Grants, error) {
	if tenant == "" || !subject.Tenants.Contains(tenant) {
		return nil, errors.New("requested tenant must belong to the authenticated subject")
	}
	if (subject.Username == "") == (subject.ServiceAccountID == "") {
		return nil, errors.New("exactly one subject identifier is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var bindings iter.Seq2[BindingGrant, error]
	if subject.ServiceAccountID != "" {
		bindings = r.store.ListServiceAccountGrants(ctx, tenant, subject.ServiceAccountID)
	} else {
		bindings = r.store.ListUserGrants(ctx, tenant, subject.Username)
	}
	grants := &Grants{Tenant: tenant}
	methods := make(map[string]struct{})
	roleMethods := make(map[string][]string)
	for binding, err := range bindings {
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		expanded, known := roleMethods[binding.RoleID]
		if !known {
			for _, rule := range binding.Rules {
				values, err := r.catalog.Expand(rule.GetService(), rule.GetMethods())
				if err != nil {
					return nil, fmt.Errorf("expand Role %q: %w", binding.RoleID, err)
				}
				expanded = append(expanded, values...)
			}
			roleMethods[binding.RoleID] = expanded
		}
		for _, method := range expanded {
			methods[method] = struct{}{}
		}
		grants.Bindings = append(grants.Bindings, binding)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	grants.Methods = slices.Sorted(maps.Keys(methods))
	return grants, nil
}
