/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"strings"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// updateIncludesField reports whether a field (or any of its children) is being
// updated. When mask is nil or empty the caller is doing a full object
// replacement, so every field is considered updated and the function returns
// true. This matches GenericServer.Update semantics: a nil mask replaces the
// entire object, while a non-nil mask applies only the listed fields.
//
// Use this to guard field-specific validation in Update hooks so that
// validation only runs when the field is actually part of the update.
func updateIncludesField(mask *fieldmaskpb.FieldMask, prefixes ...string) bool {
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

// updateMaskIncludesExactPath reports whether the mask selects one exact path.
// Parent paths are intentionally excluded so servers can distinguish an
// explicit field update from a public parent-object replacement.
func updateMaskIncludesExactPath(mask *fieldmaskpb.FieldMask, path string) bool {
	if mask == nil {
		return false
	}
	for _, maskPath := range mask.GetPaths() {
		if maskPath == path {
			return true
		}
	}
	return false
}

// updateMaskIncludesDescendantField reports whether a mask selects any child
// of the named field. The exact field and its parents are not descendants.
func updateMaskIncludesDescendantField(mask *fieldmaskpb.FieldMask, path string) bool {
	if mask == nil {
		return false
	}
	prefix := path + "."
	for _, maskPath := range mask.GetPaths() {
		if strings.HasPrefix(maskPath, prefix) {
			return true
		}
	}
	return false
}
