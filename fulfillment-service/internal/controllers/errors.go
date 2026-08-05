/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrHubNotFound indicates the hub has been decommissioned or deleted from the database.
	// When a reconciler encounters this error during deletion, it should remove its finalizer
	// and allow the resource to be archived, as the hub is permanently unavailable.
	ErrHubNotFound = errors.New("hub not found")
)

// ClassifyHubError determines if a hub access error is permanent (not found) or transient.
//
// This function distinguishes between two types of hub access errors:
//   - Permanent: The hub has been decommissioned/deleted (gRPC NotFound status)
//   - Transient: Network errors, timeouts, or other temporary failures
//
// Reconcilers use this to decide whether to:
//   - Remove the finalizer and allow archiving (permanent errors)
//   - Retry later (transient errors)
//
// Parameters:
//   - err: The error returned from hub cache or hub client operations
//
// Returns:
//   - ErrHubNotFound if the hub is permanently unavailable (decommissioned)
//   - The original error for all other cases (transient, should retry)
//   - nil if err is nil
func ClassifyHubError(err error) error {
	if err == nil {
		return nil
	}

	// Check if this is a gRPC NotFound error (hub deleted from database)
	if st, ok := status.FromError(err); ok {
		if st.Code() == codes.NotFound {
			return ErrHubNotFound
		}
	}

	// All other errors are considered transient (network, timeout, etc.)
	// Caller should retry these
	return err
}

// RemoveFinalizerOnDecommissionedHub handles the case where a hub is not found
// (likely decommissioned). It logs the event and removes the finalizer to allow
// the resource to be archived.
//
// This helper eliminates the duplicated 12-line block that appears in every reconciler's
// delete handler. All reconcilers follow the same pattern when encountering ErrHubNotFound:
// log a warning with hub_id and resource_id, then remove the finalizer.
//
// Parameters:
//   - ctx: Context for logging
//   - logger: Logger instance from the reconciler
//   - hubID: ID of the decommissioned hub
//   - resourceKey: Name of the resource ID field (e.g., "cluster_id", "compute_instance_id")
//   - resourceVal: Value of the resource ID
//   - removeFinalizer: Function to call to remove the finalizer (from reconciler task)
func RemoveFinalizerOnDecommissionedHub(
	ctx context.Context,
	logger *slog.Logger,
	hubID string,
	resourceKey string,
	resourceVal string,
	removeFinalizer func(),
) {
	logger.WarnContext(
		ctx,
		"Hub not found (likely decommissioned), removing finalizer to allow archiving",
		slog.String("hub_id", hubID),
		slog.String(resourceKey, resourceVal),
	)
	removeFinalizer()
}
