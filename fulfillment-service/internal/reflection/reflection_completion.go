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
)

// ObjectTypeNames scans the proto registry for object types in the given packages and returns the
// singular and plural names that can be used as shell completion candidates. This function does not
// require a gRPC connection — it reads only from the compiled-in proto registry.
func ObjectTypeNames(packages ...string) []string {
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
			scanServiceForNames(serviceDescs.Get(i), seen)
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

// scanServiceForNames checks whether a service has the standard CRUD methods and, if so, adds
// the singular and plural forms of the object type to the seen map.
func scanServiceForNames(serviceDesc protoreflect.ServiceDescriptor, seen map[string]bool) {
	methodDescs := serviceDesc.Methods()
	for _, name := range []protoreflect.Name{getMethodName, listMethodName, createMethodName, updateMethodName, deleteMethodName} {
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
