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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeletedTenantIdentity is minimal revocation/attribution evidence, never an archived Tenant payload.
// A nil FinalizedAt means physical removal has not been recorded; DeletedBy nil means the actor is unknown.
type DeletedTenantIdentity struct {
	TenantName        string
	TenantCreatedAt   time.Time
	DeletionTimestamp time.Time
	DeletedBy         *string
	FinalizedAt       *time.Time
	BillingRetained   bool
}

// ErrTenantIdentityRetained denies reuse of an absent tenant name whose deletion identity is retained. Billing
// history is a fallback signal, not the source of the reservation.
var ErrTenantIdentityRetained = errors.New("tenant identity retained")

// LockTenantIdentity serializes an exact tenant name before any tenant row lock or mutation. It uses the ambient
// transaction, permits reentrant acquisition, and lasts until that transaction ends. Sort names before locking several.
func LockTenantIdentity(ctx context.Context, name string) (err error) {
	tx, err := TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if name == "" {
		return errors.New("tenant identity name is empty")
	}
	_, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('osac/tenant-identity/' || $1,0))`, name)
	return
}

// CheckTenantIdentityReusable takes the identity key first. An extant tenant, including one being deleted, is left
// to the ordinary INSERT duplicate path. An absent identity is denied when billing history exists or a retained
// deletion marker exists, whether or not that marker retained billing; pending or malformed markers remain audit errors.
func CheckTenantIdentityReusable(ctx context.Context, name string) (err error) {
	tx, err := TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	if err = LockTenantIdentity(ctx, name); err != nil {
		return
	}
	_, _, _, err = tenantIdentityReuseState(ctx, tx, name)
	return
}

// tenantIdentityReuseState deliberately reads live existence without a row lock, AFTER acquiring the identity key.
// All supported tenant lifecycle writers use that same key before their row locks. An extant tenant keeps ordinary
// duplicate precedence. History remains a defensive fallback, and any finalized deletion marker reserves the name
// independently of billing. An absent marker with pending or malformed evidence remains an audit error.
func tenantIdentityReuseState(ctx context.Context, tx Tx, name string) (previous DeletedTenantIdentity, found, live bool, err error) {
	if err = tx.QueryRow(ctx, `select exists(select 1 from tenants where name = $1)`, name).Scan(&live); err != nil || live {
		return
	}
	var history bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from tenant_billing_assignments where tenant_id = $1)`, name).Scan(&history); err != nil {
		return
	}
	previous, found, err = GetDeletedTenantIdentity(ctx, name)
	if err != nil {
		return
	}
	if history || (found && previous.BillingRetained) {
		err = ErrTenantIdentityRetained
		return
	}
	if found {
		if err = validateDeletedTenantIdentity(previous); err != nil {
			return
		}
		if previous.FinalizedAt == nil {
			err = errors.New("absent tenant identity has pending finalization; audit required")
			return
		}
		err = ErrTenantIdentityRetained
	}
	return
}

// GetDeletedTenantIdentity returns (zero, false, nil) for an expected miss without marking the ambient transaction
// for rollback. There is no implicit transaction, liveness cache or Tenant payload read.
func GetDeletedTenantIdentity(ctx context.Context, name string) (result DeletedTenantIdentity, found bool, err error) {
	tx, err := TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	err = tx.QueryRow(ctx, `select tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at,
		billing_retained from deleted_tenant_identities where tenant_name = $1`, name).Scan(
		&result.TenantName, &result.TenantCreatedAt, &result.DeletionTimestamp,
		&result.DeletedBy, &result.FinalizedAt, &result.BillingRetained)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletedTenantIdentity{}, false, nil
	}
	if err != nil {
		return DeletedTenantIdentity{}, false, fmt.Errorf("failed to read deleted tenant identity: %w", err)
	}
	result.TenantCreatedAt = result.TenantCreatedAt.UTC()
	result.DeletionTimestamp = result.DeletionTimestamp.UTC()
	if result.FinalizedAt != nil {
		utc := result.FinalizedAt.UTC()
		result.FinalizedAt = &utc
	}
	return result, true, nil
}

func validateDeletedTenantIdentity(in DeletedTenantIdentity) error {
	epoch := time.Unix(0, 0).UTC()
	if in.TenantName == "" || !tenantIdentityTimestampValid(in.TenantCreatedAt) ||
		!tenantIdentityTimestampValid(in.DeletionTimestamp) || in.TenantCreatedAt.Equal(epoch) ||
		in.DeletionTimestamp.Equal(epoch) || in.DeletionTimestamp.Before(in.TenantCreatedAt) ||
		(in.FinalizedAt != nil && (!tenantIdentityTimestampValid(*in.FinalizedAt) ||
			in.FinalizedAt.Before(in.DeletionTimestamp))) {
		return errors.New("invalid deleted tenant identity chronology; audit required")
	}
	return nil
}

func tenantIdentityTimestampValid(value time.Time) bool {
	return value.UTC().Year() >= 1 && value.UTC().Year() <= 9999 && value.Nanosecond()%1000 == 0
}
