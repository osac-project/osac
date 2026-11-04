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
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var (
	ErrBillingOperationBusy    = errors.New("billing operation already in progress; retry")
	ErrBillingOperationExpired = errors.New("billing operation ownership expired; retry")
)

// BillingOperationKey separates account, tenant and correlation namespaces and never
// stores the raw value in the guard table or its errors.
func BillingOperationKey(kind, value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(kind+"\x00"+value)))
}

// BillingOperationLease is a durable guard, NOT a provider fencing token. A paused
// owner can still complete remote writes after expiry; only its local finalization
// is fenced. Provider writes and the final database commit cannot be atomic.
// Callers must bound provider I/O and revalidate their snapshot before committing.
type BillingOperationLease struct {
	owner     string
	expiresAt time.Time
	keys      []string
}

// AcquireBillingOperation commits ownership in a short independent transaction.
// The context must carry a manager, not an open request transaction. Contention
// fails immediately, including while another short acquisition/commit is running.
func AcquireBillingOperation(ctx context.Context, ttl time.Duration, keys ...string) (*BillingOperationLease, error) {
	if deadline, ok := ctx.Deadline(); ok {
		ttl = min(ttl, time.Until(deadline))
	}
	if ttl <= 0 || len(keys) == 0 {
		return nil, ErrBillingOperationExpired
	}
	lease := &BillingOperationLease{owner: uuid.New(), keys: slices.Clone(keys)}
	slices.Sort(lease.keys)
	lease.keys = slices.Compact(lease.keys)
	return WithNewTx(ctx, func(txCtx context.Context) (*BillingOperationLease, error) {
		tx, _ := TxFromContext(txCtx)
		if err := tx.QueryRow(txCtx, `select clock_timestamp() + $1 * interval '1 millisecond'`,
			ttl.Milliseconds()).Scan(&lease.expiresAt); err != nil {
			return nil, err
		}
		for _, key := range lease.keys {
			if err := lease.acquireKey(txCtx, tx, key); err != nil {
				return nil, err
			}
		}
		return lease, nil
	})
}

func tryBillingOperationLock(ctx context.Context, tx Tx, key string) error {
	var locked bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1, 152))`, key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrBillingOperationBusy
	}
	return nil
}

func (l *BillingOperationLease) acquireKey(ctx context.Context, tx Tx, key string) error {
	if err := tryBillingOperationLock(ctx, tx, key); err != nil {
		return err
	}
	var owned bool
	err := tx.QueryRow(ctx, `
		insert into billing_operation_leases (operation_key, owner, expires_at) values ($1, $2, $3)
		on conflict (operation_key) do update set owner = excluded.owner, expires_at = excluded.expires_at
		where billing_operation_leases.expires_at <= clock_timestamp()
		   or billing_operation_leases.owner = excluded.owner
		returning true`, key, l.owner, l.expiresAt).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBillingOperationBusy
	}
	return err
}

// Reserve adds a correlation candidate before any external write. Ownership of
// all earlier keys is checked in the same short transaction as the reservation.
func (l *BillingOperationLease) Reserve(ctx context.Context, key string) error {
	_, err := WithNewTx(ctx, func(txCtx context.Context) (struct{}, error) {
		tx, _ := TxFromContext(txCtx)
		if err := l.Check(txCtx, tx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, l.acquireKey(txCtx, tx, key)
	})
	if err == nil && !slices.Contains(l.keys, key) {
		l.keys = append(l.keys, key)
		slices.Sort(l.keys)
	}
	return err
}

// Check fences local finalization, holding the guard rows until this short
// transaction commits. An expired or replaced owner cannot commit local changes.
func (l *BillingOperationLease) Check(ctx context.Context, tx Tx) error {
	for _, key := range l.keys {
		if err := tryBillingOperationLock(ctx, tx, key); err != nil {
			return err
		}
		var valid bool
		err := tx.QueryRow(ctx, `select owner = $2 and expires_at > clock_timestamp()
			from billing_operation_leases where operation_key = $1 for update`, key, l.owner).Scan(&valid)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !valid) {
			return ErrBillingOperationExpired
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Release only removes this owner's records; a delayed cleanup cannot remove a
// successor's lease. Failure is safe: the records remain reclaimable at expiry.
func (l *BillingOperationLease) Release(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := WithNewTx(cleanupCtx, func(txCtx context.Context) (struct{}, error) {
		tx, _ := TxFromContext(txCtx)
		_, err := tx.Exec(txCtx, `delete from billing_operation_leases where owner = $1`, l.owner)
		return struct{}{}, err
	})
	return err
}
