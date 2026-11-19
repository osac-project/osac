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
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// TenantBillingHistoryFloor returns the latest interval boundary, including closed-only history.
// The caller serializes assignment changes with the tenant row lock. A zero result means no history.
func TenantBillingHistoryFloor(ctx context.Context, tenantID string) (floor time.Time, err error) {
	tx, err := TxFromContext(ctx)
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
	tx, err := TxFromContext(ctx)
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

// BillingFinalizationTime takes one post-I/O, post-lock database clock sample and clamps it to all history.
// The returned UTC microsecond value is shared by the closed and replacement assignment intervals.
func BillingFinalizationTime(ctx context.Context, historyFloor time.Time) (at time.Time, err error) {
	tx, err := TxFromContext(ctx)
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
