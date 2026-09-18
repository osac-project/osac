/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package fieldmask

import (
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// FromMessage extracts an update mask from a request message.
func FromMessage(message proto.Message) (*fieldmaskpb.FieldMask, bool) {
	request := message.ProtoReflect()
	field := request.Descriptor().Fields().ByName("update_mask")
	if field == nil || !request.Has(field) || field.Kind() != protoreflect.MessageKind {
		return nil, false
	}
	mask, ok := request.Get(field).Message().Interface().(*fieldmaskpb.FieldMask)
	return mask, ok
}

// IsCanonicalPath reports whether a field-mask path contains only canonical segments.
func IsCanonicalPath(path string) bool {
	if path == "" || path != strings.TrimSpace(path) || strings.IndexFunc(path, unicode.IsSpace) >= 0 {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			return false
		}
		for i, character := range segment {
			validStart := unicode.IsLetter(character) || character == '_'
			validPart := validStart || unicode.IsDigit(character)
			if (i == 0 && !validStart) || (i > 0 && !validPart) {
				return false
			}
		}
	}
	return true
}

// Includes reports whether a field mask updates any of the supplied paths.
// A nil or empty mask represents a full-object update.
func Includes(mask *fieldmaskpb.FieldMask, prefixes ...string) bool {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return true
	}
	for _, path := range mask.GetPaths() {
		for _, prefix := range prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+".") || strings.HasPrefix(prefix, path+".") {
				return true
			}
		}
	}
	return false
}

// IsCanonical reports whether every path in a field mask is canonical.
func IsCanonical(mask *fieldmaskpb.FieldMask) bool {
	if mask == nil {
		return true
	}
	for _, path := range mask.GetPaths() {
		if !IsCanonicalPath(path) {
			return false
		}
	}
	return true
}
