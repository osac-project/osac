/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"sort"
	"strings"

	"github.com/gobuffalo/flect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var getReadOnlyTypes = map[protoreflect.FullName]bool{
	(&publicv1.BillingAccount{}).ProtoReflect().Descriptor().FullName():          true,
	(&publicv1.TenantBillingAssignment{}).ProtoReflect().Descriptor().FullName(): true,
}

// ObjectTypeNames scans the proto registry for object types in the given packages and returns the
// singular and plural names that can be used as shell completion candidates. This function does not
// require a gRPC connection — it reads only from the compiled-in proto registry.
func ObjectTypeNames(packages ...string) []string {
	return objectTypeNames(packages, false, false)
}

// ReadOnlyObjectTypeNames includes list-only resources for the get command.
func ReadOnlyObjectTypeNames(packages ...string) []string {
	return objectTypeNames(packages, false, true)
}

// UpdatableObjectTypeNames scans the proto registry for object types in the given packages and returns
// only the types that expose the standard Update method. It is used by commands that mutate objects.
func UpdatableObjectTypeNames(packages ...string) []string {
	return objectTypeNames(packages, true, false)
}

func objectTypeNames(packages []string, requireUpdate, includeReadOnly bool) []string {
	pkgSet := make(map[protoreflect.FullName]bool, len(packages))
	for _, pkg := range packages {
		pkgSet[protoreflect.FullName(pkg)] = true
	}

	seen := make(map[string]bool)
	protoregistry.GlobalFiles.RangeFiles(func(fileDesc protoreflect.FileDescriptor) bool {
		if !pkgSet[fileDesc.Package()] {
			return true
		}
		serviceDescs := fileDesc.Services()
		for i := range serviceDescs.Len() {
			service := serviceDescs.Get(i)
			scanServiceForNames(service, seen, requireUpdate)
			if includeReadOnly {
				scanReadOnlyServiceForNames(service, seen)
			}
		}
		return true
	})

	results := make([]string, 0, len(seen))
	for name := range seen {
		results = append(results, name)
	}
	sort.Strings(results)
	return results
}

func scanReadOnlyServiceForNames(service protoreflect.ServiceDescriptor, seen map[string]bool) {
	list := service.Methods().ByName(listMethodName)
	if list == nil || list.Input().Fields().ByName(filterFieldName) == nil {
		return
	}
	items := list.Output().Fields().ByName(itemsFieldName)
	if items == nil || items.Cardinality() != protoreflect.Repeated || items.Kind() != protoreflect.MessageKind {
		return
	}
	object := items.Message()
	if !getReadOnlyTypes[object.FullName()] {
		return
	}
	id := object.Fields().ByName(idFieldName)
	if id == nil || id.Kind() != protoreflect.StringKind {
		return
	}
	name := string(object.Name())
	seen[strings.ToLower(name)] = true
	seen[strings.ToLower(flect.Pluralize(name))] = true
}

// scanServiceForNames checks whether a service has the standard CRUD methods and, if so, adds
// the singular and plural forms of the object type to the seen map.
func scanServiceForNames(serviceDesc protoreflect.ServiceDescriptor, seen map[string]bool, requireUpdate bool) {
	methodDescs := serviceDesc.Methods()
	methods := []protoreflect.Name{getMethodName, listMethodName, createMethodName, deleteMethodName}
	if requireUpdate {
		methods = append(methods, updateMethodName)
	}
	for _, name := range methods {
		if methodDescs.ByName(name) == nil {
			return
		}
	}

	getDesc := methodDescs.ByName(getMethodName)
	objectField := getDesc.Output().Fields().ByName(objectFieldName)
	if objectField == nil || objectField.Kind() != protoreflect.MessageKind {
		return
	}

	objectName := string(objectField.Message().Name())
	seen[strings.ToLower(objectName)] = true
	seen[strings.ToLower(flect.Pluralize(objectName))] = true
}
