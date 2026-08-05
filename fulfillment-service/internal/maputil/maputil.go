/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package maputil

import "strings"

// GetNestedValue retrieves a value from a nested map using a dot-separated path.
func GetNestedValue(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	current := any(m)
	for _, part := range parts {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = currentMap[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// SetNestedValue sets a value in a nested map using a dot-separated path,
// creating intermediate maps as needed.
func SetNestedValue(m map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := m
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part]
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		currentMap, ok := next.(map[string]any)
		if !ok {
			currentMap = map[string]any{}
			current[part] = currentMap
		}
		current = currentMap
	}
}
