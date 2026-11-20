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

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// lockProjectForMembershipCreate serializes membership creation with project deletion.
// The reference may be either the project ID or its tenant-scoped name.
func lockProjectForMembershipCreate(ctx context.Context, tenant, project string) error {
	if tenant == "" || project == "" {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}

	var deleting bool
	err = tx.QueryRow(ctx,
		`select deletion_timestamp <> 'epoch' from projects
		 where tenant = $1 and (id = $2 or name = $2) for update`,
		tenant, project,
	).Scan(&deleting)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.DeadlockDetected {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to lock project '%s'", project)
	}
	if deleting {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "project '%s' is being deleted", project)
	}
	return nil
}
