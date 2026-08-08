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
// "spec.version") and returns the value of the terminal field converted to T via
// protoreflect.Value.Interface(). Returns (zero, false) when any segment in the path does not
// exist or an intermediate segment is not a message, and (value, false) when the terminal field
// exists but is not assignable to T.
func ResolveFieldPath[T any](msg proto.Message, path string) (T, bool) {
	var zero T
	segments := strings.Split(path, ".")
	current := msg.ProtoReflect()
	for i, segment := range segments {
		fieldDesc := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		if fieldDesc == nil {
			return zero, false
		}
		if i < len(segments)-1 {
			if fieldDesc.Kind() != protoreflect.MessageKind || fieldDesc.IsList() || fieldDesc.IsMap() {
				return zero, false
			}
			current = current.Get(fieldDesc).Message()
			continue
		}
		result, ok := current.Get(fieldDesc).Interface().(T)
		return result, ok
	}
	return zero, false
}

// ResolveFieldPathOr is like ResolveFieldPath but returns fallback when the path does not exist
// or the terminal field is not assignable to T.
func ResolveFieldPathOr[T any](msg proto.Message, path string, fallback T) T {
	val, ok := ResolveFieldPath[T](msg, path)
	if !ok {
		return fallback
	}
	return val
}
