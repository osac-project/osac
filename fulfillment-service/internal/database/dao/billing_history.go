/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// TenantBillingHistoryFloor returns the latest interval boundary, including closed-only history.
// The caller serializes assignment/deletion using the tenant row lock. A zero result means no history.
func TenantBillingHistoryFloor(ctx context.Context, tenantID string) (floor time.Time, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if tenantID == "" {
		return floor, errors.New("tenant is mandatory for billing history")
	}
	var stored *time.Time
	err = tx.QueryRow(ctx, `select max(greatest(effective_from, effective_to))
		from tenant_billing_assignments where tenant_id = $1`, tenantID).Scan(&stored)
	if err == nil && stored != nil {
		floor = stored.UTC()
	}
	return
}

// BillingAccountHistoryFloor returns the latest interval boundary across tenants. The caller must
// hold the billing account row lock; a zero result means no history.
func BillingAccountHistoryFloor(ctx context.Context, accountID string) (floor time.Time, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if accountID == "" {
		return floor, errors.New("billing account is mandatory for billing history")
	}
	var stored *time.Time
	err = tx.QueryRow(ctx, `select max(greatest(effective_from, effective_to))
		from tenant_billing_assignments where billing_account_id = $1`, accountID).Scan(&stored)
	if err == nil && stored != nil {
		floor = stored.UTC()
	}
	return
}

// LockTenantBillingAssignmentForDeletion locks the complete affected account set before intervals,
// both in SQL id order. The caller must already hold the tenant lock; no provider calls may follow.
// History remains in the main table and contributes to the floor even when no current interval exists.
func LockTenantBillingAssignmentForDeletion(ctx context.Context, tenantID string) (floor time.Time, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if tenantID == "" {
		return floor, errors.New("tenant is mandatory for billing history")
	}
	for _, query := range []string{
		`select id from billing_accounts where id in (
			select billing_account_id from tenant_billing_assignments where tenant_id = $1 and effective_to is null
		) order by id for update`,
		`select id from tenant_billing_assignments where tenant_id = $1 order by id for update`,
	} {
		// Fully consume and close each result before issuing the next query on this connection.
		rows, queryErr := tx.Query(ctx, query, tenantID)
		if queryErr != nil {
			return floor, queryErr
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return
		}
	}
	return TenantBillingHistoryFloor(ctx, tenantID)
}

// BillingFinalizationTime takes one post-I/O/post-lock database clock sample and clamps it to all
// history. The returned UTC microsecond value must be passed unchanged to both interval boundaries.
func BillingFinalizationTime(ctx context.Context, historyFloor time.Time) (at time.Time, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if !historyFloor.IsZero() && timestamppb.New(historyFloor).CheckValid() != nil {
		return at, errors.New("billing history floor is outside the supported timestamp range")
	}
	err = tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&at)
	if err != nil {
		return
	}
	at = at.UTC().Truncate(time.Microsecond)
	if at.Before(historyFloor) {
		// Database floors already have microsecond precision. Round a supplied finer floor UP,
		// never down through history, and fail rather than overflow the protobuf timestamp range.
		at = historyFloor.UTC().Truncate(time.Microsecond)
		if at.Before(historyFloor) {
			at = at.Add(time.Microsecond)
		}
	}
	if at.IsZero() || timestamppb.New(at).CheckValid() != nil {
		return time.Time{}, errors.New("billing finalization time is outside the supported timestamp range")
	}
	return
}

// CloseTenantBillingAssignmentOnDeletion closes only the current interval using canonical JSON.
// The caller holds the locks acquired above and supplies the accepted tenant deletion time.
// An absent current interval is idempotent; closed history and original creation metadata are immutable.
func CloseTenantBillingAssignmentOnDeletion(ctx context.Context, tenantID string, effectiveAt time.Time) (err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if tenantID == "" {
		return errors.New("tenant is mandatory for billing history")
	}
	var currentID string
	var currentStart time.Time
	err = tx.QueryRow(ctx, `select id, effective_from from tenant_billing_assignments
		where tenant_id = $1 and effective_to is null`, tenantID).Scan(&currentID, &currentStart)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return
	}
	if effectiveAt.IsZero() || timestamppb.New(effectiveAt).CheckValid() != nil || effectiveAt.Nanosecond()%1000 != 0 {
		return status.Error(codes.Aborted, "invalid billing interval finalization time; retry")
	}
	if effectiveAt.Before(currentStart) {
		return &ErrInUse{Reason: fmt.Sprintf(
			"tenant %q deletion predates the start of open billing interval %q; repair the billing history before retrying deletion",
			tenantID, currentID,
		)}
	}
	tag, err := tx.Exec(ctx, `update tenant_billing_assignments
		set data = jsonb_set(data, '{status}', data->'status' || jsonb_build_object(
			'effective_to_timestamp', to_char($3::timestamptz at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
			'message', $4::text)), version = version + 1
		where id = $1 and tenant_id = $2 and effective_to is null and effective_from <= $3`,
		currentID, tenantID, effectiveAt.UTC(), database.TenantBillingAssignmentDeletedMessage)
	if err != nil {
		return
	}
	if tag.RowsAffected() != 1 {
		return status.Error(codes.Aborted, "billing interval changed during deletion; retry")
	}
	return
}
