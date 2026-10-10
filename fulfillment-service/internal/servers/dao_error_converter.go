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
	"errors"
	"reflect"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

// ConvertDAOErrorToGRPC converts DAO errors to gRPC status errors with appropriate codes.
// This provides the same error mapping logic that GenericServer uses, making it available
// to custom servers that don't use the generic server pattern.
//
// Returns a gRPC status error with the appropriate code and message, or nil if err is nil.
func ConvertDAOErrorToGRPC(err error, operation string, objectID string) error {
	if err == nil {
		return nil
	}

	// NotFound errors
	var notFoundErr *dao.ErrNotFound
	if errors.As(err, &notFoundErr) {
		if objectID != "" {
			return grpcstatus.Errorf(grpccodes.NotFound, "object with identifier '%s' not found", objectID)
		}
		return grpcstatus.Errorf(grpccodes.NotFound, "%s", notFoundErr.Error())
	}

	// AlreadyExists errors (with special handling for singleton constraints)
	var alreadyExistsErr *dao.ErrAlreadyExists
	if errors.As(err, &alreadyExistsErr) {
		// Singleton constraints should use FailedPrecondition instead of AlreadyExists
		if isSingletonConstraintViolation(alreadyExistsErr.ConstraintName) {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"concurrent create violated a singleton invariant (constraint '%s'); please retry",
				alreadyExistsErr.ConstraintName)
		}
		return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", alreadyExistsErr.Error())
	}

	// NotUnique errors
	var notUniqueErr *dao.ErrNotUnique
	if errors.As(err, &notUniqueErr) {
		return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", notUniqueErr.Error())
	}

	// Conflict errors (optimistic locking)
	var conflictErr *dao.ErrConflict
	if errors.As(err, &conflictErr) {
		return grpcstatus.Errorf(grpccodes.Aborted, "%s", conflictErr.Error())
	}

	// Deadlock errors
	var deadlockErr *dao.ErrDeadlock
	if errors.As(err, &deadlockErr) {
		return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
	}

	// Permission denied errors
	var deniedErr *dao.ErrDenied
	if errors.As(err, &deniedErr) {
		return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Error())
	}

	// Immutable field errors
	var immutableErr *dao.ErrImmutable
	if errors.As(err, &immutableErr) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", immutableErr.Error())
	}

	// Reference errors (different codes depending on operation)
	var referenceErr *dao.ErrReference
	if errors.As(err, &referenceErr) {
		// Create operations treat reference errors as InvalidArgument (bad input)
		// Update/Delete operations treat them as FailedPrecondition (violates constraints)
		if operation == "create" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", referenceErr.Error())
		}
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", referenceErr.Error())
	}

	// InUse errors (deletion blocked by references)
	var inUseErr *dao.ErrInUse
	if errors.As(err, &inUseErr) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", inUseErr.Error())
	}

	// Validation errors
	var validationErr *dao.ErrValidation
	if errors.As(err, &validationErr) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", validationErr.Error())
	}

	// Invalid filter errors
	var invalidFilterErr *dao.ErrInvalidFilter
	if errors.As(err, &invalidFilterErr) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", invalidFilterErr.Error())
	}

	if objectID != "" {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to %s object with identifier '%s'", operation, objectID)
	}
	return grpcstatus.Errorf(grpccodes.Internal, "failed to %s object", operation)
}

// ValidateRequiredString returns a gRPC InvalidArgument error if the value is empty.
// This is a common validation pattern used across servers.
func ValidateRequiredString(fieldName string, value string) error {
	if value == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s' is mandatory", fieldName)
	}
	return nil
}

// ValidateRequiredObject returns a gRPC InvalidArgument error if the object is nil.
// This uses reflection to detect both untyped nils and typed nil pointers.
// This is a common validation pattern used across servers.
func ValidateRequiredObject(fieldName string, obj any) error {
	if obj == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s is required", fieldName)
	}
	// Use reflection to detect nil pointers held in the any value
	v := reflect.ValueOf(obj)
	//nolint:govet // Using named constant reflect.Ptr is more readable than inlining
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s is required", fieldName)
	}
	return nil
}
