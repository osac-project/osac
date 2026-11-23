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
	"slices"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
)

// Allows reports whether the grants include the given full gRPC method.
func (g *Grants) Allows(method string) bool {
	if g == nil {
		return false
	}
	_, found := slices.BinarySearch(g.Methods, strings.TrimPrefix(method, "/"))
	return found
}

// Covers checks both the current catalog permissions and scope of any wildcards.
func (g *Grants) Covers(permissionCatalog *catalog.Catalog, service string, methods []string) (bool, error) {
	expanded, err := permissionCatalog.Expand(service, methods)
	if err != nil {
		return false, err
	}
	if g == nil {
		return false, nil
	}
	for _, method := range expanded {
		if !g.Allows(method) {
			return false, nil
		}
	}
	for _, method := range methods {
		if !strings.HasSuffix(service, "*") && method != catalog.AllMethodsWildcard {
			continue
		}
		if !g.coversWildcard(service, method) {
			return false, nil
		}
	}
	return true, nil
}

func (g *Grants) coversWildcard(service, method string) bool {
	for _, binding := range g.Bindings {
		for _, rule := range binding.Rules {
			if coversService(rule.GetService(), service) &&
				(slices.Contains(rule.GetMethods(), method) || slices.Contains(rule.GetMethods(), catalog.AllMethodsWildcard)) {
				return true
			}
		}
	}
	return false
}

func coversService(held, requested string) bool {
	switch held {
	case catalog.AllServicesWildcard:
		return true
	case catalog.PublicLayerWildcard, catalog.PrivateLayerWildcard:
		return strings.HasPrefix(requested, strings.TrimSuffix(held, "*"))
	default:
		return held == requested
	}
}
