/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fnv32a returns the FNV-32a hash of the value, printed as hex.
func fnv32a(value string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum32()
}

// quotaNameFromDimension converts a dimension to a DNS-1123 name: lowercased, non-alphanumerics
// dashed, trimmed, and suffixed with a short hash of the original dimension so two dimensions that
// sanitize to the same base still get different names.
func quotaNameFromDimension(dimension string) string {
	var sanitized strings.Builder
	for _, r := range strings.ToLower(dimension) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('-')
		}
	}
	base := strings.Trim(sanitized.String(), "-")
	if len(base) > 54 {
		base = base[:54]
	}
	return fmt.Sprintf("%s-%x", base, fnv32a(dimension))
}

// isUniqueViolation tells whether the error is PostgreSQL's unique-violation error
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.UniqueViolation
	}
	return false
}

// upsertQuotaLimit writes the fresh object when no row matches the filter, and copies the
// fresh object's spec onto the matching row otherwise. A lost insert race recovers through
// a savepoint, because a unique violation aborts the whole PostgreSQL transaction even at
// READ COMMITTED: the loser rolls back to before its insert, re-reads the winner's row and
// updates it. The filter names the scope: the dimension, the tenant and the project.
func upsertQuotaLimit[O dao.Object](
	ctx context.Context,
	guard *dao.GenericDAO[O],
	filter string,
	fresh O,
	merge func(existing, fresh O) O,
) (result O, err error) {
	response, err := guard.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return fresh, err
	}
	if len(response.GetItems()) > 0 {
		update, err := guard.Update().SetObject(merge(response.GetItems()[0], fresh)).Do(ctx)
		if err != nil {
			return fresh, err
		}
		return update.GetObject(), nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return fresh, err
	}
	if _, err = tx.Exec(ctx, "savepoint sp_limit_insert"); err != nil {
		return fresh, err
	}
	created, err := guard.Create().SetObject(fresh).Do(ctx)
	if !isUniqueViolation(err) {
		if err != nil {
			return fresh, err
		}
		return created.GetObject(), nil
	}
	if _, err = tx.Exec(ctx, "rollback to savepoint sp_limit_insert"); err != nil {
		return fresh, err
	}
	response, err = guard.List().
		SetFilter(filter).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		return fresh, err
	}
	if len(response.GetItems()) == 0 {
		return fresh, fmt.Errorf("the limit row vanished from its unique scope")
	}
	update, err := guard.Update().SetObject(merge(response.GetItems()[0], fresh)).Do(ctx)
	if err != nil {
		return fresh, err
	}
	return update.GetObject(), nil
}

// quotaDefaultValues returns the default limit of every dimension, from the live rows of
// the shared tenant.
func quotaDefaultValues(
	ctx context.Context, guard *dao.GenericDAO[*privatev1.QuotaDefaultLimit],
) (map[string]int64, error) {
	items, err := listAllRows(ctx, guard, fmt.Sprintf(
		"this.metadata.tenant == %s", strconv.Quote(auth.SharedTenant)))
	if err != nil {
		return nil, err
	}
	result := map[string]int64{}
	for _, item := range items {
		result[item.GetSpec().GetDimension()] = item.GetSpec().GetValue()
	}
	return result, nil
}
