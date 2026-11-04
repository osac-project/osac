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
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Billing operation leases", func() {
	var ctx context.Context
	var pool *pgxpool.Pool
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
		manager, err := NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = TxManagerIntoContext(ctx, manager)
	})

	check := func(lease *BillingOperationLease) error {
		_, err := WithNewTx(ctx, func(txCtx context.Context) (struct{}, error) {
			tx, _ := TxFromContext(txCtx)
			return struct{}{}, lease.Check(txCtx, tx)
		})
		return err
	}

	It("commits ownership, fails fast on overlapping keys, and permits unrelated operations", func() {
		a, b, c := BillingOperationKey("account", "a"), BillingOperationKey("tenant", "b"), BillingOperationKey("account", "c")
		lease, err := AcquireBillingOperation(ctx, time.Minute, a, b)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.Stat().AcquiredConns()).To(BeZero())
		_, err = AcquireBillingOperation(ctx, time.Minute, c, b)
		Expect(err).To(MatchError(ErrBillingOperationBusy))
		other, err := AcquireBillingOperation(ctx, time.Minute, c)
		Expect(err).ToNot(HaveOccurred(), "a partial failed acquisition must roll back")
		Expect(other.Release(ctx)).To(Succeed())
		Expect(lease.Release(ctx)).To(Succeed())
		again, err := AcquireBillingOperation(ctx, time.Minute, a, b)
		Expect(err).ToNot(HaveOccurred())
		Expect(again.Release(ctx)).To(Succeed())
	})

	It("reserves correlation candidates durably without exposing their raw value", func() {
		first, err := AcquireBillingOperation(ctx, time.Minute, BillingOperationKey("account", "a"))
		Expect(err).ToNot(HaveOccurred())
		second, err := AcquireBillingOperation(ctx, time.Minute, BillingOperationKey("account", "b"))
		Expect(err).ToNot(HaveOccurred())
		key := BillingOperationKey("correlation", "private-correlation-value")
		Expect(first.Reserve(ctx, key)).To(Succeed())
		err = second.Reserve(ctx, key)
		Expect(err).To(MatchError(ErrBillingOperationBusy))
		Expect(err.Error()).ToNot(ContainSubstring("private-correlation-value"))
		var raw bool
		Expect(pool.QueryRow(ctx, `select exists(select 1 from billing_operation_leases
			where operation_key like '%private-correlation-value%')`).Scan(&raw)).To(Succeed())
		Expect(raw).To(BeFalse())
		Expect(first.Release(ctx)).To(Succeed())
		Expect(second.Release(ctx)).To(Succeed())
	})

	It("permits expired takeover, rejects stale finalization, and makes late release owner-safe", func() {
		key := BillingOperationKey("account", "a")
		stale, err := AcquireBillingOperation(ctx, time.Minute, key)
		Expect(err).ToNot(HaveOccurred())
		_, err = pool.Exec(ctx, `update billing_operation_leases set expires_at = clock_timestamp() - interval '1 second'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(check(stale)).To(MatchError(ErrBillingOperationExpired))
		next, err := AcquireBillingOperation(ctx, time.Minute, key)
		Expect(err).ToNot(HaveOccurred())
		Expect(check(stale)).To(MatchError(ErrBillingOperationExpired))
		Expect(stale.Release(ctx)).To(Succeed())
		Expect(check(next)).To(Succeed())
		Expect(next.Release(ctx)).To(Succeed())
	})

	It("bounds expiry by the request deadline and cleans up after cancellation", func() {
		deadlineCtx, cancel := context.WithTimeout(ctx, time.Second)
		lease, err := AcquireBillingOperation(deadlineCtx, time.Hour, BillingOperationKey("account", "a"))
		Expect(err).ToNot(HaveOccurred())
		var remaining float64
		Expect(pool.QueryRow(ctx, `select extract(epoch from (expires_at - clock_timestamp()))
			from billing_operation_leases`).Scan(&remaining)).To(Succeed())
		Expect(remaining).To(BeNumerically("<=", 1.1))
		cancel()
		Expect(lease.Release(deadlineCtx)).To(Succeed())
	})

	It("fails fast while a short finalization transaction owns a guard row", func() {
		key := BillingOperationKey("account", "a")
		lease, err := AcquireBillingOperation(ctx, time.Minute, key)
		Expect(err).ToNot(HaveOccurred())
		manager, err := TxManagerFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		tx, err := manager.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(lease.Check(ctx, tx)).To(Succeed())
		_, err = AcquireBillingOperation(ctx, time.Minute, key)
		Expect(err).To(MatchError(ErrBillingOperationBusy))
		Expect(tx.End(ctx)).To(Succeed())
		Expect(lease.Release(ctx)).To(Succeed())
	})

	It("separates namespaces and produces only digest keys", func() {
		key := BillingOperationKey("correlation", "value")
		Expect(key).To(HaveLen(64))
		Expect(strings.Contains(key, "value")).To(BeFalse())
		Expect(key).ToNot(Equal(BillingOperationKey("account", "value")))
	})
})
