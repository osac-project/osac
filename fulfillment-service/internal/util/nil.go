/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package util

import "reflect"

// NormalizeNil returns a true nil when the interface holds a nil pointer, preventing typed-nil values from bypassing
// nil checks and causing panics on method calls.
func NormalizeNil[T any](value T) T {
	v := reflect.ValueOf(&value).Elem()
	if v.Kind() == reflect.Interface {
		elem := v.Elem()
		if !elem.IsValid() {
			var zero T
			return zero
		}
		if elem.Kind() == reflect.Pointer && elem.IsNil() {
			var zero T
			return zero
		}
	}
	return value
}
