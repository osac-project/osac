/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package references

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

// errRefNotFound satisfies the IsNotFound() interface expected by the reference validator.
type errRefNotFound struct {
	identifier string
}

func (e *errRefNotFound) Error() string {
	return fmt.Sprintf("resource %q not found", e.identifier)
}

func (e *errRefNotFound) IsNotFound() bool {
	return true
}

// NewDAOLookupFunc creates a ReferenceLookupFunc backed by a GenericDAO. It queries the DAO
// using a CEL filter that matches by id or metadata.name and returns the resolved reference metadata.
func NewDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O]) ReferenceLookupFunc {
	return newDAOLookupFunc(d, false, false, false)
}

// NewScopedDAOLookupFunc creates a DAO lookup that additionally constrains references to an
// explicitly supplied tenant/project. If the caller has no explicit tenant, it retains the
// visibility-based behavior of NewDAOLookupFunc.
func NewScopedDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O]) ReferenceLookupFunc {
	return newDAOLookupFunc(d, true, false, false)
}

func newDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O], scopeExplicitTenant, scopeNameLookup, publishedOnly bool) ReferenceLookupFunc {
	return func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
		var filter string
		switch {
		case id != "" && name != "":
			filter = fmt.Sprintf("this.id == %s && this.metadata.name == %s", strconv.Quote(id), strconv.Quote(name))
		case id != "":
			filter = fmt.Sprintf("this.id == %s", strconv.Quote(id))
		case name != "":
			filter = fmt.Sprintf("this.metadata.name == %s", strconv.Quote(name))
		default:
			return nil, &errRefNotFound{identifier: "(empty)"}
		}
		// scopeExplicitTenant constrains every lookup (name and ID) to the
		// supplied tenant/project. scopeNameLookup constrains only name-based
		// lookups (id==""), matching the canonical resolver where names require
		// scope disambiguation but IDs are globally unique within a tenant.
		// Requests without metadata retain visibility-based behavior so
		// default-tenant assignment remains compatible.
		applyScope := tenant != "" &&
			(scopeExplicitTenant || (scopeNameLookup && id == ""))
		if applyScope {
			filter += fmt.Sprintf(
				" && this.metadata.tenant == %s && this.metadata.project == %s",
				strconv.Quote(tenant), strconv.Quote(project),
			)
		}
		if publishedOnly {
			filter = PublishedFilter(filter)
		}

		response, err := d.List().
			SetFilter(filter).
			SetLimit(1).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("lookup failed: %w", err)
		}

		items := response.GetItems()
		if len(items) == 0 {
			identifier := name
			if identifier == "" {
				identifier = id
			}
			return nil, &errRefNotFound{identifier: identifier}
		}

		item := items[0]
		resolved := &ResolvedRef{ID: item.GetId()}
		if metadata, ok := reflection.ResolveFieldPath[protoreflect.Message](item, "metadata"); ok {
			m := metadata.Interface()
			resolved.Name = reflection.ResolveFieldPathOr(m, "name", "")
			resolved.Tenant = reflection.ResolveFieldPathOr(m, "tenant", "")
			resolved.Project = reflection.ResolveFieldPathOr(m, "project", "")
		}
		return resolved, nil
	}
}

// PublishedFilter adds the public publication and lifecycle predicates to a DAO filter.
func PublishedFilter(filter string) string {
	return "this.published == true && !has(this.metadata.deletion_timestamp) && (" + filter + ")"
}

// RegisterDAOLookup instantiates a DAO lookup and registers it on the
// validator. Name-based lookups are constrained to the tenant/project
// selected by resolveTenantProject so that a same-named resource in
// another visible project cannot shadow the intended target. ID-based
// lookups retain visibility-based behavior because IDs are globally
// unique within a tenant and the canonical resolver does not scope
// them by project.
func RegisterDAOLookup[O dao.Object](
	v *ReferenceValidator,
	fullName protoreflect.FullName,
	d *dao.GenericDAO[O],
) {
	v.Register(fullName, newDAOLookupFunc(d, false, true, false))
}
