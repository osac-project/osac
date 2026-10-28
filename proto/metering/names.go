/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package metering

import (
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// ShortName strips everything through the last dot of a full proto name, so rules and
// history say "ComputeInstance" rather than "osac.private.v1.ComputeInstance".
func ShortName(full string) string {
	if index := strings.LastIndex(full, "."); index >= 0 {
		return full[index+1:]
	}
	return full
}

// EnumValuePrefix returns the longest prefix ending in an underscore that every value of
// the enum starts with, or an empty string when the values share none. Rules and history
// use it to say RUNNING instead of COMPUTE_INSTANCE_STATE_RUNNING.
func EnumValuePrefix(descriptor protoreflect.EnumDescriptor) string {
	values := descriptor.Values()
	prefix := string(values.Get(0).Name())
	for i := 1; i < values.Len(); i++ {
		for !strings.HasPrefix(string(values.Get(i).Name()), prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	if index := strings.LastIndex(prefix, "_"); index >= 0 {
		return prefix[:index+1]
	}
	return ""
}

// OwnerSpec names the field that carries a resource's ownership root, and the type of that
// root.
type OwnerSpec struct {
	// Field is the tracked field that holds the owner reference, for example
	// "status.cluster".
	Field string

	// Resource is the root's resource type, for example "Cluster".
	Resource string
}

// Owners maps the short name of each tracked resource that carries an owner reference to
// that reference. Resources without an owner reference are absent.
func Owners(resources []Resource) map[string]OwnerSpec {
	owners := map[string]OwnerSpec{}
	for _, resource := range resources {
		for _, field := range resource.Fields {
			if field.OwnerResource == "" {
				continue
			}
			owners[ShortName(string(resource.Message))] = OwnerSpec{
				Field:    field.Path,
				Resource: field.OwnerResource,
			}
		}
	}
	return owners
}
