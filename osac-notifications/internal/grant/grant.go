/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grant

import "fmt"

// DefaultMap is the producer source → allowed CloudEvent kind grant map.
var DefaultMap = Map{
	"osac-fulfillment": {"event": {}},
}

// Map authorizes producer sources to publish specific notification kinds.
type Map map[string]map[string]struct{}

// Allows reports whether source may publish kind.
func (m Map) Allows(source, kind string) bool {
	kinds, ok := m[source]
	if !ok {
		return false
	}
	_, ok = kinds[kind]
	return ok
}

// Check returns an error when the source/kind pair is not granted.
func (m Map) Check(source, kind string) error {
	if m.Allows(source, kind) {
		return nil
	}
	return fmt.Errorf("source %q is not allowed to publish kind %q", source, kind)
}
