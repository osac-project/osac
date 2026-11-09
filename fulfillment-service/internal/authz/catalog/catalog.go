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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// This is needed to ensure that the types and services are loaded into the protocol buffers registry, otherwise
	// they will be visible only if they are explicitly used in some part of the code.
	_ "github.com/osac-project/osac/proto/gen/osac/private/v1"
	_ "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	publicPackage  = protoreflect.FullName("osac.public.v1")
	privatePackage = protoreflect.FullName("osac.private.v1")
)

const (
	// Rule name prefixes of the services of each API layer
	publicLayerPrefix  = "osac.public."
	privateLayerPrefix = "osac.private."

	// Accepted wildcard values for services
	AllServicesWildcard  = "*"
	PublicLayerWildcard  = publicLayerPrefix + "*"
	PrivateLayerWildcard = privateLayerPrefix + "*"

	// Accepted wildcard values for methods
	AllMethodsWildcard = "*"
)

// excludedServices lists services of the scanned packages that are never grantable through
// permission rules.
var excludedServices = map[protoreflect.Name]struct{}{
	// Capabilities must be usable before login, for issuer discovery, and is anonymous today, so
	// it stays outside of role composition.
	"Capabilities": {},
}

// Service describes one gRPC service of the catalog
type Service struct {
	// name is the rule name of the service, for example "osac.public.computeinstances".
	name string
	// fullName is the full protobuf name of the service, for example "osac.public.v1.ComputeInstances".
	fullName string
	// methods maps the rule names of the methods to the full gRPC methods
	methods map[string]string
	// methodNames contains the rule names of the methods, sorted alphabetically.
	methodNames []string
	// allMethods contains the full gRPC methods of the service, sorted alphabetically.
	allMethods []string
}

// Name returns the authoring-facing rule name of the service, for example "osac.public.rolebindings".
func (s *Service) Name() string {
	return s.name
}

// FullName returns the full protobuf name of the service, for example "osac.public.v1.RoleBindings".
func (s *Service) FullName() string {
	return s.fullName
}

// Methods returns the authoring-facing names of the methods of the service, sorted alphabetically.
func (s *Service) Methods() []string {
	return slices.Clone(s.methodNames)
}

// Method returns the full gRPC method corresponding to the given authoring-facing method name. The
// second return value is false if the service doesn't have such a method.
func (s *Service) Method(name string) (string, bool) {
	result, ok := s.methods[name]
	return result, ok
}

// Catalog is the permission catalog of a running build. It maps the authoring-facing rule names
// of the services and methods to the full gRPC methods that they grant. Use the Build function to
// create instances.
type Catalog struct {
	// services maps the rule names of the services to their descriptions:
	services map[string]*Service
	// all contains all the services, ordered by rule name:
	all []*Service
	// public and private contain the services of each API layer, ordered by rule name:
	public  []*Service
	private []*Service
}

// Services returns the authoring-facing rule names of all the cataloged services, sorted
// alphabetically.
func (c *Catalog) Services() []string {
	result := make([]string, len(c.all))
	for i, service := range c.all {
		result[i] = service.name
	}
	return result
}

// LookupService returns the cataloged service with the given rule name, or nil if there is no
// such service.
func (c *Catalog) LookupService(name string) *Service {
	return c.services[name]
}

// Build creates the permission catalog, scanning the given registry of protobuf files. All the
// services and methods of the `osac.public.v1` and `osac.private.v1` packages are included, except
// those explicitly excluded. Production code passes `protoregistry.GlobalFiles`; tests can pass an
// isolated registry. The catalog is built once at startup, as it never changes while the process
// is running.
func Build(files *protoregistry.Files) (result *Catalog, err error) {
	if files == nil {
		return nil, errors.New("registry is mandatory")
	}
	result = &Catalog{
		services: map[string]*Service{},
	}
	var scanErr error
	files.RangeFiles(func(fileDesc protoreflect.FileDescriptor) bool {
		scanErr = result.scanFile(fileDesc)
		return scanErr == nil
	})
	if scanErr != nil {
		return nil, scanErr
	}
	result.sort()
	return result, nil
}

// scanFile scans one file of the registry, adding all its services to the catalog. Files that
// don't belong to one of the API packages are ignored. This excludes the gRPC infrastructure
// methods, like metadata, reflection and health, which live in different packages.
func (c *Catalog) scanFile(fileDesc protoreflect.FileDescriptor) error {
	packageDesc := fileDesc.Package()
	if packageDesc != publicPackage && packageDesc != privatePackage {
		return nil
	}
	serviceDescs := fileDesc.Services()
	for i := range serviceDescs.Len() {
		if err := c.scanService(packageDesc, serviceDescs.Get(i)); err != nil {
			return err
		}
	}
	return nil
}

// scanService adds one service to the catalog. Note that no assumptions are made about the shape
// of the service: methods that aren't part of the usual create, read, update and delete set, like
// `Events.Watch`, `ServiceAccounts.RegenerateSecret` or the review services, are cataloged as
// grantable methods.
func (c *Catalog) scanService(packageDesc protoreflect.FullName, serviceDesc protoreflect.ServiceDescriptor) error {
	serviceName := serviceDesc.Name()
	if _, ok := excludedServices[serviceName]; ok {
		return nil
	}
	ruleName := serviceRuleName(packageDesc, serviceName)
	existing, ok := c.services[ruleName]
	if ok {
		first, second := existing.fullName, string(serviceDesc.FullName())
		if second < first {
			first, second = second, first
		}
		return fmt.Errorf(
			"services %q and %q both map to rule name %q",
			first, second, ruleName,
		)
	}
	service := &Service{
		name:     ruleName,
		fullName: string(serviceDesc.FullName()),
		methods:  make(map[string]string, serviceDesc.Methods().Len()),
	}
	methodDescs := serviceDesc.Methods()
	for i := range methodDescs.Len() {
		methodDesc := methodDescs.Get(i)
		methodName := strings.ToLower(string(methodDesc.Name()))
		previous, ok := service.methods[methodName]
		if ok {
			return fmt.Errorf(
				"methods %q and %q of service %q both map to rule name %q",
				protoMethodName(previous), methodDesc.Name(), serviceDesc.FullName(), methodName,
			)
		}
		service.methods[methodName] = fullMethod(serviceDesc, methodDesc)
	}
	service.methodNames = slices.Sorted(maps.Keys(service.methods))
	service.allMethods = slices.Sorted(maps.Values(service.methods))
	c.services[ruleName] = service
	return nil
}

// sort prepares the ordered lists of services used to expand wildcards deterministically.
func (c *Catalog) sort() {
	c.all = slices.SortedFunc(maps.Values(c.services), func(a, b *Service) int {
		return strings.Compare(a.name, b.name)
	})
	c.public = make([]*Service, 0, len(c.all))
	c.private = make([]*Service, 0, len(c.all))
	for _, service := range c.all {
		if strings.HasPrefix(service.name, publicLayerPrefix) {
			c.public = append(c.public, service)
		} else {
			c.private = append(c.private, service)
		}
	}
}

// serviceRuleName calculates the authoring-facing rule name of a service: it takes the protobuf
// package, drops the version segment, appends the name of the service and converts everything to
// lower case. For example `osac.public.v1.RoleBindings` becomes `osac.public.rolebindings`.
func serviceRuleName(packageDesc protoreflect.FullName, serviceName protoreflect.Name) string {
	result := string(packageDesc)
	if idx := strings.LastIndex(result, "."); idx >= 0 {
		result = result[:idx]
	}
	return result + "." + strings.ToLower(string(serviceName))
}

// fullMethod returns the full name of a gRPC method, for example "osac.public.v1.RoleBindings/Get".
func fullMethod(serviceDesc protoreflect.ServiceDescriptor, methodDesc protoreflect.MethodDescriptor) string {
	return string(serviceDesc.FullName()) + "/" + string(methodDesc.Name())
}

// protoMethodName extracts the name of the protobuf method from a full gRPC method name.
func protoMethodName(fullMethod string) string {
	if idx := strings.LastIndex(fullMethod, "/"); idx >= 0 {
		return fullMethod[idx+1:]
	}
	return fullMethod
}
