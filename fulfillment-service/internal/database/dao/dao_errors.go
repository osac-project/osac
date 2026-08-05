/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"fmt"
	"slices"

	"github.com/dustin/go-humanize/english"
)

// ErrNotFound is an error type that indicates that one or more requested objects don't exist.
type ErrNotFound struct {
	// IDs contains the identifiers of the objects that were not found.
	IDs []string
}

// Error returns the error message.
func (e *ErrNotFound) Error() string {
	switch len(e.IDs) {
	case 0:
		return "object not found"
	case 1:
		return fmt.Sprintf("object with identifier '%s' not found", e.IDs[0])
	default:
		quoted := make([]string, len(e.IDs))
		for i, id := range e.IDs {
			quoted[i] = fmt.Sprintf("'%s'", id)
		}
		return fmt.Sprintf("objects with identifiers %s not found", english.WordSeries(quoted, "and"))
	}
}

// ErrAlreadyExists is an error type that indicates that an object can't be created because it already exists.
type ErrAlreadyExists struct {
	// Table is the name of the table.
	Table string

	// ID is the identifier of the object that already exists.
	ID string

	// Name is set when the violation is caused by a name uniqueness constraint rather than a primary key
	// collision. When set, error messages should reference the name instead of the identifier.
	Name string

	// Reason is set when a custom error message is provided by a database trigger (e.g., for uniqueness
	// violations with additional context). When set, this message is returned instead of the default message.
	Reason string
}

// Error returns the error message.
func (e *ErrAlreadyExists) Error() string {
	// If a reason has already been specified then use it directly:
	if e.Reason != "" {
		return e.Reason
	}

	// If no reason has been provided then build the error message according to the table, identifier and name:
	var kind string
	switch e.Table {
	case "projects":
		kind = "project"
	case "tenants":
		kind = "tenant"
	default:
		kind = "object"
	}
	switch {
	case e.ID != "" && e.Name != "":
		if e.ID == e.Name {
			return fmt.Sprintf("%s '%s' already exists", kind, e.Name)
		}
		return fmt.Sprintf("%s with identifier '%s' and name '%s' already exists", kind, e.ID, e.Name)
	case e.ID != "":
		return fmt.Sprintf("%s with identifier '%s' already exists", kind, e.ID)
	case e.Name != "":
		return fmt.Sprintf("%s '%s' already exists", kind, e.Name)
	default:
		return fmt.Sprintf("%s already exists", kind)
	}
}

// ErrConflict is an error type that indicates that an update was rejected because the object's current version does not
// match the version specified by the caller in the request. This is used to implement optimistic locking.
type ErrConflict struct {
	// ID is the identifier of the object.
	ID string

	// RequestVersion is the version that the caller specified in the request.
	RequestVersion int32

	// CurrentVersion is the current version of the object in the database.
	CurrentVersion int32
}

// Error returns the error message.
func (e *ErrConflict) Error() string {
	return fmt.Sprintf(
		"object with identifier '%s' has been modified: requested version %d but current version is %d",
		e.ID, e.RequestVersion, e.CurrentVersion,
	)
}

// ErrDenied is an error type that indicates a requested operation is not allowed. The reason string is a human friendly
// description that will never contain technical details, so it can be safely returned to the user as part of the error
// response, for example as the message of a gRPC status error.
type ErrDenied struct {
	Reason string
}

// Error returns the error message.
func (e *ErrDenied) Error() string {
	return e.Reason
}

// ErrImmutable is an error type that indicates that an update was rejected because it attempted to modify one or more
// immutable fields.
type ErrImmutable struct {
	// Fields contains the names of the fields that the caller tried to modify. For example, if the called tried
	// to modify the name of an object that is immutable, then it will contain 'metadata.name'.
	Fields []string
}

// Error returns the error message.
func (e *ErrImmutable) Error() string {
	if len(e.Fields) == 0 {
		return "some fields are immutable"
	}
	quoted := slices.Clone(e.Fields)
	slices.Sort(quoted)
	for i, field := range quoted {
		quoted[i] = fmt.Sprintf("'%s'", field)
	}
	if len(quoted) == 1 {
		return fmt.Sprintf("field %s is immutable", quoted[0])
	}
	return fmt.Sprintf("fields %s are immutable", english.WordSeries(quoted, "and"))
}

// ErrReference indicates that an operation failed because it references an entity that doesn't exist, for example a
// tenant or a project.
type ErrReference struct {
	// Reason is a human-friendly description of what reference is invalid.
	Reason string
}

// Error returns the error message.
func (e *ErrReference) Error() string {
	if e.Reason == "" {
		return "some reference is invalid"
	}
	return e.Reason
}

// ErrInUse indicates that a deletion was rejected because the object is still referenced by other objects.
type ErrInUse struct {
	// Reason is a human-friendly description of what is still using the object.
	Reason string
}

// Error returns the error message.
func (e *ErrInUse) Error() string {
	if e.Reason == "" {
		return "object is still in use"
	}
	return e.Reason
}

// ErrNotUnique is an error type that indicates that a value that must be globally unique is already in use. For
// example, this is returned when a tenant e-mail domain is already assigned to another tenant.
type ErrNotUnique struct {
	// Reason is a human-readable description of the uniqueness violation.
	Reason string
}

func (e *ErrNotUnique) Error() string {
	return e.Reason
}

// ErrDeadlock indicates that a PostgreSQL deadlock was detected. The caller should retry the operation.
type ErrDeadlock struct{}

// Error returns the error message.
func (e *ErrDeadlock) Error() string {
	return "concurrent modification detected, please retry"
}

// Custom PostgreSQL SQLSTATE error codes used by database triggers. These codes use the 'Z' class, which is reserved
// for user-defined conditions and will not collide with any standard PostgreSQL error code.
const (
	// errImmutableCode is the SQLSTATE error code returned by the 'check_immutable_columns' trigger when
	// an update attempts to modify one or more immutable columns. When this error is received the detail field of
	// the PostgreSQL error contains a JSON array with the names of the columns that the caller tried to modify.
	errImmutableCode = "Z0001"

	// errReferenceCode is the SQLSTATE error code returned by the 'check_compute_instance_subnet_refs'
	// trigger when an insert references a resource that does not exist or has been deleted.
	errReferenceCode = "Z0002"

	// errInUseCode is the SQLSTATE error code returned by the 'check_subnet_not_in_use' trigger when a
	// soft-delete is rejected because the object is still referenced by other objects.
	errInUseCode = "Z0003"

	// errNotUniqueCode is the SQLSTATE error code returned by database triggers when an insert or update violates a
	// custom uniqueness constraint enforced via a helper trigger. For example, the trigger that materializes the
	// relationship between tenants and domains returns this code when a domain is already assigned to
	// another tenant.
	errNotUniqueCode = "Z0004"
)
