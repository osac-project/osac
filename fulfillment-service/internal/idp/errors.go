/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

import "fmt"

// ErrNotFound indicates that a requested IDP resource (organization, group, etc.) does not exist.
type ErrNotFound struct {
	Kind string
	Name string
}

func (e *ErrNotFound) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("%s %q not found", e.Kind, e.Name)
	}
	return fmt.Sprintf("%s not found", e.Kind)
}
