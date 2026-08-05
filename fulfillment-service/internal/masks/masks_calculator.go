/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package masks

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Calculator computes field masks by comparing two protobuf messages.
// It identifies which fields have changed between a before and after state,
// enabling precise updates that don't overwrite concurrent changes.
type Calculator struct {
	// Future extension points can be added here as needed
}

// CalculatorBuilder builds Calculator instances.
type CalculatorBuilder struct {
	// Future configuration options can be added here as needed
}

// NewCalculator creates a new builder for constructing a Calculator.
func NewCalculator() *CalculatorBuilder {
	return &CalculatorBuilder{}
}

// Build creates a Calculator instance from the builder.
func (b *CalculatorBuilder) Build() *Calculator {
	return &Calculator{}
}

// Calculate returns a field mask of fields that changed between before and after.
// This prevents updates from overwriting concurrent changes to fields not being modified.
// Uses deep recursive comparison to detect changes at any level of nesting.
func (c *Calculator) Calculate(before, after proto.Message) *fieldmaskpb.FieldMask {
	paths := c.compareMessages(before.ProtoReflect(), after.ProtoReflect(), "")
	return &fieldmaskpb.FieldMask{Paths: paths}
}

// compareMessages recursively compares two protobuf messages and returns paths of changed fields.
func (c *Calculator) compareMessages(before, after protoreflect.Message, prefix string) []string {
	var paths []string

	// Iterate through all fields once using the message descriptor
	fields := after.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)

		// Build the field path (e.g., "status.state" or "metadata.finalizers")
		fieldPath := string(fd.Name())
		if prefix != "" {
			fieldPath = prefix + "." + fieldPath
		}

		// For maps, Has() doesn't work as expected - always compare them
		if fd.IsMap() {
			beforeVal := before.Get(fd)
			afterVal := after.Get(fd)

			if !c.compareMaps(beforeVal.Map(), afterVal.Map(), fd) {
				paths = append(paths, fieldPath)
			}
			continue
		}

		// For lists, Has() doesn't work as expected - always compare them
		if fd.IsList() {
			beforeVal := before.Get(fd)
			afterVal := after.Get(fd)

			if !c.compareLists(beforeVal.List(), afterVal.List(), fd) {
				paths = append(paths, fieldPath)
			}
			continue
		}

		// For non-list/non-map fields, use Has() to check if field is set
		hasInBefore := before.Has(fd)
		hasInAfter := after.Has(fd)

		// Handle three cases: field in both, only in before, only in after
		switch {
		case hasInBefore && hasInAfter:
			// Field exists in both: compare values
			beforeVal := before.Get(fd)
			afterVal := after.Get(fd)

			if fd.Message() != nil {
				// Recursively compare message fields for granularity
				subPaths := c.compareMessages(beforeVal.Message(), afterVal.Message(), fieldPath)
				if len(subPaths) > 0 {
					paths = append(paths, subPaths...)
				}
			} else {
				// Compare scalar fields (string, int32, int64, bool, enum, bytes, float, double, etc.)
				if !beforeVal.Equal(afterVal) {
					paths = append(paths, fieldPath)
				}
			}

		case !hasInBefore && hasInAfter:
			// Field only in after (newly added): add top-level path only
			// No need to recurse since field masks are hierarchical
			paths = append(paths, fieldPath)

		case hasInBefore && !hasInAfter:
			// Field was present in before but cleared in after
			paths = append(paths, fieldPath)
		}
	}

	return paths
}

// compareLists compares two protobuf list fields element by element.
// Handles both scalar and message element types.
func (c *Calculator) compareLists(beforeList, afterList protoreflect.List, fd protoreflect.FieldDescriptor) bool {
	if beforeList.Len() != afterList.Len() {
		return false
	}

	for i := 0; i < beforeList.Len(); i++ {
		beforeVal := beforeList.Get(i)
		afterVal := afterList.Get(i)

		// For message elements, use proto.Equal for deep comparison
		if fd.Message() != nil {
			if !proto.Equal(beforeVal.Message().Interface(), afterVal.Message().Interface()) {
				return false
			}
		} else {
			// For scalar elements (string, int, bool, bytes, etc.), use direct comparison
			if !beforeVal.Equal(afterVal) {
				return false
			}
		}
	}

	return true
}

// compareMaps compares two protobuf map fields key by key.
// Handles both scalar and message value types.
func (c *Calculator) compareMaps(beforeMap, afterMap protoreflect.Map, fd protoreflect.FieldDescriptor) bool {
	if beforeMap.Len() != afterMap.Len() {
		return false
	}

	equal := true
	beforeMap.Range(func(key protoreflect.MapKey, beforeVal protoreflect.Value) bool {
		// Check if key exists in both maps
		if !afterMap.Has(key) {
			equal = false
			return false
		}

		afterVal := afterMap.Get(key)

		// For message values, use proto.Equal for deep comparison
		if fd.MapValue().Message() != nil {
			if !proto.Equal(beforeVal.Message().Interface(), afterVal.Message().Interface()) {
				equal = false
				return false
			}
		} else {
			// For scalar values (string, int, bool, bytes, etc.), use direct comparison
			if !beforeVal.Equal(afterVal) {
				equal = false
				return false
			}
		}

		return true
	})

	return equal
}
