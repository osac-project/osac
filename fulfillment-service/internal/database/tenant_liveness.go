/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ResolveLiveTenantNames removes deleted tenants from a claimed tenant set.
// Missing tenants are retained for initial JIT onboarding, but archived tenants
// are not missing tenants. A current live tenant takes precedence over its old
// archive. This reads PostgreSQL on every call; there is no revocation cache.
func ResolveLiveTenantNames(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return []string{}, nil
	}
	tx, err := TxFromContext(ctx)
	if err != nil {
		return WithNewTx(ctx, func(txCtx context.Context) ([]string, error) {
			return ResolveLiveTenantNames(txCtx, names)
		})
	}
	rows, err := tx.Query(ctx, `
		select claimed.name
		from unnest($1::text[]) with ordinality as claimed(name, position)
		left join tenants current on current.name = claimed.name
		where current.deletion_timestamp = 'epoch'
		   or (current.id is null and not exists (
		       select 1 from archived_tenants archived where archived.name = claimed.name
		   ))
		order by claimed.position
	`, names)
	if err != nil {
		return nil, fmt.Errorf("failed to read tenant liveness: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
