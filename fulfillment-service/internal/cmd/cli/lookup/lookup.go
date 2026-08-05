/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package lookup provides a shared name-or-ID resolution helper for CLI commands.
package lookup

import (
	"fmt"
)

// ListFunc fetches up to limit objects matching the given CEL filter.
type ListFunc[T any] func(filter string, limit int32) ([]T, error)

// ErrNotFound is returned when no object matches the given reference.
type ErrNotFound struct {
	Ref  string
	Kind string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("%s %q not found", e.Kind, e.Ref)
}

// ErrAmbiguous is returned when more than one object matches the given reference.
// The user should be asked to use the ID instead.
type ErrAmbiguous struct {
	Ref  string
	Kind string
}

func (e *ErrAmbiguous) Error() string {
	return fmt.Sprintf("multiple %ss match %q, use the ID instead", e.Kind, e.Ref)
}

// Find resolves a name-or-ID reference to exactly one object.
// It builds the CEL filter "this.id == ref || this.metadata.name == ref", calls list, and returns matching object.
func Find[T any](ref, kind string, list ListFunc[T]) (result T, err error) {
	if ref == "" {
		err = fmt.Errorf("%s name or ID must not be empty", kind)
		return
	}
	filter := fmt.Sprintf(`this.id == %[1]q || this.metadata.name == %[1]q`, ref)
	items, err := list(filter, 2)
	if err != nil {
		return
	}
	switch len(items) {
	case 0:
		err = &ErrNotFound{Ref: ref, Kind: kind}
	case 1:
		result = items[0]
	default:
		err = &ErrAmbiguous{Ref: ref, Kind: kind}
	}
	return
}
