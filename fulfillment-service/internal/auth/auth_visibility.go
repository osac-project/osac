/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// Visibility describes which tenants and projects a user is allowed to see.
//
// A visibility value is in one of three modes:
//
//   - Zero: no tenants or projects are visible. IsZero returns true.
//   - Total: every tenant and project is visible. IsTotal returns true. The set of tenants and projects is not
//     enumerable in this mode.
//   - Partial: a finite set of tenants is visible, and within each tenant a finite set of projects is visible.
//
// Project names are hierarchical, using the dot character as a separator. For example, project "a.b" is a child of
// project "a", and project "a.b.c" is a child of project "a.b". Visibility follows this hierarchy: if a user has
// permission to see the objects within a project, they also have permission to see the objects within all of its
// descendant projects.
//
// The root of the hierarchy is the default project, whose name is the empty string. The hierarchy rule does not apply
// to the default project: visibility of the default project does not imply visibility of any other project. Without
// this exception, granting access to the default project would be equivalent to granting access to every project in
// the tenant, which is not the intended behavior. The default project is always visible for any tenant that the user
// has access to, regardless of explicit project grants.
//
// Callers that need to enumerate tenants or projects should check IsZero and IsTotal first. VisibleTenants and
// VisibleProjects return nil when the visibility is total, because there is no finite list to return.
type Visibility struct {
	// rules describes the visibility for each tenant when the visibility is not total.
	rules []visibilityRule

	// total indicates, when true, that the user has permission to see all tenants and projects.
	total bool
}

// VisibilityBuilder is a builder for creating new visibility objects.
type VisibilityBuilder struct {
	rules []visibilityRule
	total bool
}

// visibilityRule represents the visibility of the current user for a given tenant.
type visibilityRule struct {
	// tenant is the name of the tenant.
	tenant string

	// projects is the list of projects visible for the tenant. Project names are hierarchical paths separated by dots;
	// granting a parent also grants its descendants, except for the default project (empty string) which only grants
	// access to itself.
	projects []string
}

// zeroVisibility is a pre-built visibility that grants no access to any tenants or projects.
var zeroVisibility Visibility = Visibility{
	total: false,
}

// ZeroVisibility returns a visibility that grants no access to any tenants or projects.
func ZeroVisibility() *Visibility {
	return &zeroVisibility
}

// totalVisibility is a pre-built visibility that grants access to all tenants and projects.
var totalVisibility Visibility = Visibility{
	total: true,
}

// TotalVisibility returns a visibility that grants access to all tenants and projects.
func TotalVisibility() *Visibility {
	return &totalVisibility
}

// NewVisibility creates a new builder for visibility objects.
func NewVisibility() *VisibilityBuilder {
	return &VisibilityBuilder{}
}

// SetTotal sets whether the visibility grants access to all tenants and projects. When this isn't explicitly specified
// the default is false.
func (b *VisibilityBuilder) SetTotal(value bool) *VisibilityBuilder {
	b.total = value
	return b
}

// AddVisibleTenant adds the given tenant to the visibility.
func (b *VisibilityBuilder) AddVisibleTenant(value string) *VisibilityBuilder {
	for i := range b.rules {
		if b.rules[i].tenant == value {
			return b
		}
	}
	b.rules = append(b.rules, visibilityRule{
		tenant:   value,
		projects: nil,
	})
	return b
}

// AddVisibleTenants adds the given tenants to the visibility.
func (b *VisibilityBuilder) AddVisibleTenants(values ...string) *VisibilityBuilder {
	for _, value := range values {
		b.AddVisibleTenant(value)
	}
	return b
}

// AddVisibleProject adds the given project to the visibility for the given tenant. If the tenant is not already present
// it is created. Duplicate projects and projects that are descendants of another granted project are removed during
// build.
func (b *VisibilityBuilder) AddVisibleProject(tenant, project string) *VisibilityBuilder {
	for i := range b.rules {
		if b.rules[i].tenant == tenant {
			b.rules[i].projects = append(b.rules[i].projects, project)
			return b
		}
	}
	b.rules = append(b.rules, visibilityRule{
		tenant: tenant,
		projects: []string{
			project,
		},
	})
	return b
}

// AddVisibleProjects adds the given projects to the visibility for the given tenant.
func (b *VisibilityBuilder) AddVisibleProjects(tenant string, projects ...string) *VisibilityBuilder {
	for _, project := range projects {
		b.AddVisibleProject(tenant, project)
	}
	return b
}

// Build creates a new visibility using the configuration stored in the builder.
func (b *VisibilityBuilder) Build() (result *Visibility, err error) {
	// Deep-copy the rules so a reused builder cannot share project backing arrays with the result:
	rules := make([]visibilityRule, len(b.rules))
	for i, rule := range b.rules {
		if rule.tenant == "" {
			err = errors.New("tenant cannot be empty")
			return
		}
		rules[i] = visibilityRule{
			tenant:   rule.tenant,
			projects: slices.Clone(rule.projects),
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		tenantI := rules[i].tenant
		tenantJ := rules[j].tenant
		return tenantI < tenantJ
	})

	// For each rule drop duplicate projects and descendants of a retained ancestor, keeping the list sorted. Coverage
	// is checked against a map of retained projects by looking up the candidate and its dot-delimited ancestors, so a
	// lexicographically intervening name cannot hide a descendant from its ancestor.
	for i := range rules {
		rule := &rules[i]
		projects := rule.projects
		sort.Strings(projects)
		normalized := make([]string, 0, len(projects))
		retained := make(map[string]struct{}, len(projects))
		for _, project := range projects {
			covered := false
			if _, found := retained[project]; found {
				covered = true
			} else {
				for j := 0; j < len(project); j++ {
					if project[j] != '.' {
						continue
					}
					ancestor := project[:j]
					if _, found := retained[ancestor]; found && visibilityCovers(ancestor, project) {
						covered = true
						break
					}
				}
			}
			if !covered {
				normalized = append(normalized, project)
				retained[project] = struct{}{}
			}
		}
		rule.projects = normalized
	}

	// Create the result:
	result = &Visibility{
		total: b.total,
		rules: rules,
	}
	return
}

// IsZero reports whether the visibility object grants no access to any tenants or projects.
func (v *Visibility) IsZero() bool {
	return v == nil || (!v.total && len(v.rules) == 0)
}

// IsTotal reports whether the visibility grants access to all tenants and projects.
func (v *Visibility) IsTotal() bool {
	return v != nil && v.total
}

// IsTenantVisible reports whether the given tenant is visible, regardless of which projects are visible within it.
func (v *Visibility) IsTenantVisible(tenant string) bool {
	if v == nil {
		return false
	}
	if v.total {
		return true
	}
	for i := range v.rules {
		if v.rules[i].tenant == tenant {
			return true
		}
	}
	return false
}

// IsProjectVisible reports whether the given project within the given tenant is visible. The default project (empty
// string) is always visible for any tenant that the user has visibility of, regardless of explicit project grants.
func (v *Visibility) IsProjectVisible(tenant, project string) bool {
	if v == nil {
		return false
	}
	if v.total {
		return true
	}
	if !v.IsTenantVisible(tenant) {
		return false
	}
	if project == "" {
		return true
	}
	var rule *visibilityRule
	for i := range v.rules {
		if v.rules[i].tenant == tenant {
			rule = &v.rules[i]
			break
		}
	}
	if rule == nil {
		return false
	}
	for _, visible := range rule.projects {
		if project == visible || visibilityCovers(visible, project) {
			return true
		}
	}
	return false
}

// VisibleTenants returns the visible tenants, sorted alphabetically. It returns nil when the visibility is nil or
// total.
func (v *Visibility) VisibleTenants() []string {
	if v == nil || v.total {
		return nil
	}
	result := make([]string, len(v.rules))
	for i := range v.rules {
		result[i] = v.rules[i].tenant
	}
	sort.Strings(result)
	return result
}

// VisibleProjects returns the visible projects for the given tenant, sorted alphabetically. The default project (empty
// string) is always included when the tenant is present. It returns nil when the visibility is nil, total, or does not
// contain the given tenant.
func (v *Visibility) VisibleProjects(tenant string) []string {
	if v == nil || v.total {
		return nil
	}
	for i := range v.rules {
		if v.rules[i].tenant == tenant {
			result := make([]string, 0, len(v.rules[i].projects)+1)
			hasDefault := false
			for _, p := range v.rules[i].projects {
				if p == "" {
					hasDefault = true
				}
				result = append(result, p)
			}
			if !hasDefault {
				result = append(result, "")
			}
			sort.Strings(result)
			return result
		}
	}
	return nil
}

// visibilityCovers reports whether granting visibility of the granted project also implies visibility of the queried
// project. This is true when the queried project is a descendant of the granted project in the dot-separated hierarchy,
// for example granting "a" covers "a.b" and "a.b.c". The default project (empty string) does not cover any other
// project; granting it only gives access to the default project itself. This returns false when granted and queried are
// the same, because exact matches are handled separately by the caller.
func visibilityCovers(granted, queried string) bool {
	grantedLen := len(granted)
	queriedLen := len(queried)
	if grantedLen == 0 || queriedLen <= grantedLen {
		return false
	}
	if !strings.HasPrefix(queried, granted) {
		return false
	}
	if queried[grantedLen] != '.' {
		return false
	}
	return true
}
