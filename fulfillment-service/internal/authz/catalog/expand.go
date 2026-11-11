/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package catalog

import (
	"fmt"
	"maps"
	"slices"
)

// ValidateRule checks that the given service and method names are valid values for a
// permission rule
func (c *Catalog) ValidateRule(service string, methods []string) error {
	services, err := c.matchServices(service)
	if err != nil {
		return err
	}
	return validateMethods(services, methods)
}

// Expand resolves a permission rule to the sorted list of full gRPC methods that it grants, for
// example "osac.public.v1.ComputeInstances/Get". Service wildcards select multiple services and
// the method wildcard selects all the methods of the selected services. A method named explicitly
// is resolved only for the selected services that have it, so a rule like `{service:
// "osac.public.*", methods: ["get"]}` grants the `get` method of the public services that provide
// it. Unknown service or method names are rejected.
func (c *Catalog) Expand(service string, methods []string) ([]string, error) {
	services, err := c.matchServices(service)
	if err != nil {
		return nil, err
	}
	if err := validateMethods(services, methods); err != nil {
		return nil, err
	}
	granted := make(map[string]bool)
	for _, method := range methods {
		if method == AllMethodsWildcard {
			for _, service := range services {
				for _, fullMethod := range service.allMethods {
					granted[fullMethod] = true
				}
			}
			continue
		}
		for _, service := range services {
			if fullMethod, ok := service.methods[method]; ok {
				granted[fullMethod] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(granted)), nil
}

// matchServices returns the services selected by the service name of a rule.
func (c *Catalog) matchServices(service string) ([]*Service, error) {
	switch service {
	case AllServicesWildcard:
		return c.all, nil
	case PublicLayerWildcard:
		return c.public, nil
	case PrivateLayerWildcard:
		return c.private, nil
	default:
		result, ok := c.services[service]
		if !ok {
			return nil, fmt.Errorf("unknown service")
		}
		return []*Service{result}, nil
	}
}

// validateMethods checks that the method names of a rule are cataloged for the selected services.
// A method is valid if at least one of the selected services has it, so a layer wildcard can be
// combined with methods that only some of its services provide.
func validateMethods(services []*Service, methods []string) error {
	if len(methods) == 0 {
		return fmt.Errorf("at least one method is required")
	}
	for _, method := range methods {
		if method == AllMethodsWildcard {
			continue
		}
		found := false
		for _, service := range services {
			if _, ok := service.methods[method]; ok {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown method")
		}
	}
	return nil
}
