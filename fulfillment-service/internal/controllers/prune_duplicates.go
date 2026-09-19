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
	"log/slog"

	clnt "sigs.k8s.io/controller-runtime/pkg/client"
)

// PruneDuplicateCRs deletes duplicate CRs from the hub cluster. The caller
// passes the extra objects that should be removed (everything except the one
// to keep, typically all items after the oldest when sorted by creation time).
// This provides defense-in-depth against startup races that can produce
// duplicate CRs sharing the same UUID label (OSAC-4208).
func PruneDuplicateCRs(
	ctx context.Context,
	logger *slog.Logger,
	hubClient clnt.Client,
	extras []clnt.Object,
	resourceKind string,
	id string,
) {
	logger.WarnContext(ctx, "Found duplicate "+resourceKind+" CRs, pruning extras",
		slog.String("id", id),
		slog.Int("duplicates", len(extras)),
	)
	for _, obj := range extras {
		if err := hubClient.Delete(ctx, obj); err != nil {
			logger.ErrorContext(ctx, "Failed to delete duplicate "+resourceKind+" CR",
				slog.String("id", id),
				slog.String("name", obj.GetName()),
				slog.Any("error", err),
			)
		} else {
			logger.WarnContext(ctx, "Deleted duplicate "+resourceKind+" CR",
				slog.String("id", id),
				slog.String("name", obj.GetName()),
			)
		}
	}
}
