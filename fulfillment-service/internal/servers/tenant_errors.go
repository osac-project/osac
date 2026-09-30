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
	"context"
	"errors"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

// convertTenantErrorToGRPC converts tenant determination errors to gRPC status errors with
// appropriate logging. This ensures consistent error handling across GenericServer and
// SelfSubjectAccessReview servers.
//
// Parameters:
//   - ctx: context for logging
//   - err: the error from auth.DetermineTenantForOperation
//   - logger: logger for warning/error messages
//   - requestedTenant: the tenant that was requested (for log messages)
//
// Returns a gRPC status error with the appropriate code and message.
func convertTenantErrorToGRPC(ctx context.Context, err error, logger *slog.Logger, requestedTenant string) error {
	if err == nil {
		return nil
	}

	var invisibleErr *auth.TenantInvisibleError
	var unassignableErr *auth.TenantUnassignableError

	if errors.As(err, &invisibleErr) {
		logger.WarnContext(
			ctx,
			"User is trying to assign a tenant that is invisible to them",
			slog.String("requested", requestedTenant),
		)
		return grpcstatus.Error(grpccodes.PermissionDenied, "you are not authorized to use the specified tenant")
	}

	if errors.As(err, &unassignableErr) {
		logger.WarnContext(
			ctx,
			"User is trying to assign a tenant that is unassignable",
			slog.String("requested", requestedTenant),
		)
		return grpcstatus.Error(grpccodes.PermissionDenied, "you are not authorized to use the specified tenant")
	}

	// Unknown error during tenant determination
	logger.ErrorContext(
		ctx,
		"Failed to determine tenant",
		slog.Any("error", err),
	)
	return grpcstatus.Errorf(grpccodes.Internal, "failed to determine tenant")
}
