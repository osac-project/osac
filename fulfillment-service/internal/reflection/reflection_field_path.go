/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ResolveFieldPath navigates a protobuf message following a dot-separated field path (e.g.
// "spec.version") and returns the string representation of the final field. Returns an empty
// string if any segment doesn't exist or if an intermediate segment isn't a message.
func ResolveFieldPath(msg proto.Message, path string) string {
	segments := strings.Split(path, ".")
	current := msg.ProtoReflect()
	for i, segment := range segments {
		fieldDesc := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		if fieldDesc == nil {
			return ""
		}
		if i < len(segments)-1 {
			if fieldDesc.Kind() != protoreflect.MessageKind {
				return ""
			}
			current = current.Get(fieldDesc).Message()
			continue
		}
		return current.Get(fieldDesc).String()
	}
	return ""
}
