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
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

// errRefNotFound satisfies the IsNotFound() interface expected by the reference validator.
type errRefNotFound struct {
	identifier string
}

type publishedLookupCacheContextKey struct{}

type publishedLookupCache struct {
	loaded      bool
	identifiers []publishedLookupIdentifier
	byID        map[string]*ResolvedRef
	byName      map[string]*ResolvedRef
}

type publishedLookupIdentifier struct {
	id   string
	name string
}

func withPublishedLookupCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, publishedLookupCacheContextKey{}, &publishedLookupCache{})
}

func getPublishedLookupCache(ctx context.Context) *publishedLookupCache {
	cache, _ := ctx.Value(publishedLookupCacheContextKey{}).(*publishedLookupCache)
	return cache
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
	return newDAOLookupFunc(d, false, false)
}

// NewPublishedDAOLookupFunc creates a DAO lookup that only resolves published resources.
// It is intended for references exposed through the public API.
func NewPublishedDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O]) ReferenceLookupFunc {
	fallback := newDAOLookupFunc(d, false, true)
	return func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
		cache := getPublishedLookupCache(ctx)
		if cache == nil {
			return fallback(ctx, tenant, project, id, name)
		}
		if !cache.loaded {
			clauses := make([]string, 0, len(cache.identifiers))
			for _, identifier := range cache.identifiers {
				if clause := IdentifierFilter(identifier.id, identifier.name); clause != "" {
					clauses = append(clauses, "("+clause+")")
				}
			}
			if len(clauses) == 0 {
				return nil, &errRefNotFound{identifier: identifier(id, name)}
			}
			response, err := d.List().
				SetFilter(PublishedFilter(strings.Join(clauses, " || "))).
				SetLimit(100).
				Do(ctx)
			if err != nil {
				return nil, fmt.Errorf("lookup failed: %w", err)
			}
			cache.byID = make(map[string]*ResolvedRef, len(response.GetItems()))
			cache.byName = make(map[string]*ResolvedRef, len(response.GetItems()))
			for _, item := range response.GetItems() {
				resolved := resolvedRefFromObject(item)
				cache.byID[resolved.ID] = resolved
				if resolved.Name != "" {
					cache.byName[resolved.Name] = resolved
				}
			}
			cache.loaded = true
		}
		var resolved *ResolvedRef
		if id != "" {
			resolved = cache.byID[id]
		} else {
			resolved = cache.byName[name]
		}
		if resolved != nil && (name == "" || resolved.Name == name) {
			return resolved, nil
		}
		return nil, &errRefNotFound{identifier: identifier(id, name)}
	}
}

func resolvedRefFromObject[O dao.Object](item O) *ResolvedRef {
	resolved := &ResolvedRef{ID: item.GetId()}
	if metadata, ok := reflection.ResolveFieldPath[protoreflect.Message](item, "metadata"); ok {
		m := metadata.Interface()
		resolved.Name = reflection.ResolveFieldPathOr(m, "name", "")
		resolved.Tenant = reflection.ResolveFieldPathOr(m, "tenant", "")
		resolved.Project = reflection.ResolveFieldPathOr(m, "project", "")
	}
	return resolved
}

// NewScopedDAOLookupFunc creates a DAO lookup that additionally constrains references to an
// explicitly supplied tenant/project. If the caller has no explicit tenant, it retains the
// visibility-based behavior of NewDAOLookupFunc.
func NewScopedDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O]) ReferenceLookupFunc {
	return newDAOLookupFunc(d, true, false)
}

func newDAOLookupFunc[O dao.Object](d *dao.GenericDAO[O], scopeExplicitTenant, publishedOnly bool) ReferenceLookupFunc {
	return func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
		filter := IdentifierFilter(id, name)
		if filter == "" {
			return nil, &errRefNotFound{identifier: "(empty)"}
		}
		// When the caller supplies an explicit scope, keep local references inside it. Requests
		// without metadata retain the existing visibility-based behavior so generic default-tenant
		// assignment remains compatible.
		if scopeExplicitTenant && tenant != "" {
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
		return resolvedRefFromObject(item), nil
	}
}

// IdentifierFilter creates the canonical DAO filter for an id/name reference.
func IdentifierFilter(id, name string) string {
	switch {
	case id != "" && name != "":
		return fmt.Sprintf("this.id == %s && this.metadata.name == %s", strconv.Quote(id), strconv.Quote(name))
	case id != "":
		return fmt.Sprintf("this.id == %s", strconv.Quote(id))
	case name != "":
		return fmt.Sprintf("this.metadata.name == %s", strconv.Quote(name))
	default:
		return ""
	}
}

// PublishedFilter adds the public publication and lifecycle predicates to a DAO filter.
func PublishedFilter(filter string) string {
	return "this.published == true && !has(this.metadata.deletion_timestamp) && (" + filter + ")"
}

// RegisterDAOLookup is a convenience that instantiates a DAO lookup and registers it on the
// validator in one call, using the protobuf full name of the reference message type.
func RegisterDAOLookup[O dao.Object](
	v *ReferenceValidator,
	fullName protoreflect.FullName,
	d *dao.GenericDAO[O],
) {
	v.Register(fullName, NewDAOLookupFunc(d))
}
