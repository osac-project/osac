/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Tenant billing assignments", func() {
	var (
		ctx  context.Context
		pool *pgxpool.Pool
		tx   Tx
	)

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
		tx, err = manager.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(tx.End(ctx)).To(Succeed()) })
	})

	createTenant := func(name string) {
		_, err := tx.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $1, $1, 'system', '{}')
			 on conflict do nothing`, name)
		Expect(err).ToNot(HaveOccurred())
	}
	createAccount := func(correlationID string) string {
		id := uuid.New()
		_, err := UpsertBillingAccount(ctx, tx, BillingAccountUpsert{
			ID: id, ProviderKey: "m360", ProviderAccountReference: id,
			CorrelationID: correlationID, State: "ACTIVE",
		})
		Expect(err).ToNot(HaveOccurred())
		return id
	}

	BeforeEach(func() {
		createTenant("tenant-a")
		createTenant("tenant-b")
	})

	Describe("CloseAndInsertTenantBillingAssignment", func() {
		It("opens the first interval for a tenant/account with no prior assignment", func() {
			account := createAccount("")
			id, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-1", TenantID: "tenant-a", BillingAccountID: account,
				ConnectionState: "PENDING",
			})
			Expect(err).ToNot(HaveOccurred())
			row, err := GetTenantBillingAssignment(ctx, tx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(row.TenantID).To(Equal("tenant-a"))
			Expect(row.EffectiveTo).To(BeNil())
		})

		It("closes the old interval and opens a new one atomically, with no gap or overlap", func() {
			account1 := createAccount("")
			account2 := createAccount("")
			first, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-1", TenantID: "tenant-a", BillingAccountID: account1,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			second, err := CloseAndInsertTenantBillingAssignment(ctx, tx, first, TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-2", TenantID: "tenant-a", BillingAccountID: account2,
				ConnectionState: "PENDING",
			})
			Expect(err).ToNot(HaveOccurred())

			closedRow, err := GetTenantBillingAssignment(ctx, tx, first)
			Expect(err).ToNot(HaveOccurred())
			Expect(closedRow.EffectiveTo).ToNot(BeNil())

			newRow, err := GetTenantBillingAssignment(ctx, tx, second)
			Expect(err).ToNot(HaveOccurred())
			Expect(newRow.EffectiveTo).To(BeNil())
			Expect(newRow.EffectiveFrom.Equal(*closedRow.EffectiveTo)).To(BeTrue(),
				"the new interval must start exactly when the old one ended")

			active, err := GetActiveTenantBillingAssignmentByTenant(ctx, tx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(active.ID).To(Equal(second))
		})

		It("rejects opening a second active interval for the same tenant", func() {
			account1 := createAccount("")
			account2 := createAccount("")
			_, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-1", TenantID: "tenant-a", BillingAccountID: account1,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			// The second insert violates the partial unique index, which aborts a plain statement's transaction;
			// a savepoint isolates that abort so the outer tx (and its DeferCleanup'd End) stays usable.
			spErr := tx.Savepoint(ctx, func(spCtx context.Context) error {
				spTx, err := TxFromContext(spCtx)
				Expect(err).ToNot(HaveOccurred())
				_, err = CloseAndInsertTenantBillingAssignment(spCtx, spTx, "", TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "asg-2", TenantID: "tenant-a", BillingAccountID: account2,
					ConnectionState: "CONNECTED",
				})
				return err
			})
			Expect(spErr).To(HaveOccurred())
		})
	})

	Describe("GetActiveTenantBillingAssignmentByAccount and ByTenant", func() {
		It("returns pgx.ErrNoRows when no active assignment exists", func() {
			_, err := GetActiveTenantBillingAssignmentByTenant(ctx, tx, "tenant-a")
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
			_, err = GetActiveTenantBillingAssignmentByAccount(ctx, tx, "no-such-account")
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		})
	})

	Describe("ListTenantBillingAssignments", func() {
		var account1, account2 string

		BeforeEach(func() {
			account1 = createAccount("")
			account2 = createAccount("")
			first, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-1", TenantID: "tenant-a", BillingAccountID: account1,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			_, err = CloseAndInsertTenantBillingAssignment(ctx, tx, first, TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-1b", TenantID: "tenant-a", BillingAccountID: account1,
				ConnectionState: "PENDING",
			})
			Expect(err).ToNot(HaveOccurred())
			_, err = CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-2", TenantID: "tenant-b", BillingAccountID: account2,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("filters by spec.tenant_id", func() {
			items, total, err := ListTenantBillingAssignments(ctx, tx, 0, 10, `this.spec.tenant_id == "tenant-a"`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(2)), "one closed, one current, both for tenant-a")
			for _, item := range items {
				Expect(item.TenantID).To(Equal("tenant-a"))
			}
		})

		It("filters by spec.billing_account_id", func() {
			_, total, err := ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.billing_account_id == "`+account2+`"`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
		})

		It("filters current-only with !has(this.status.effective_to)", func() {
			items, total, err := ListTenantBillingAssignments(ctx, tx, 0, 10, `!has(this.status.effective_to)`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(2)), "tenant-a's current + tenant-b's current")
			for _, item := range items {
				Expect(item.EffectiveTo).To(BeNil())
			}
		})

		It("filters historical-only with has(this.status.effective_to)", func() {
			_, total, err := ListTenantBillingAssignments(ctx, tx, 0, 10, `has(this.status.effective_to)`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
		})

		It("combines two clauses with &&", func() {
			_, total, err := ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.tenant_id == "tenant-a" && !has(this.status.effective_to)`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
		})

		It("applies offset and limit", func() {
			items, total, err := ListTenantBillingAssignments(ctx, tx, 0, 1, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(3)))
			Expect(items).To(HaveLen(1))
		})

		It("rejects a negative limit or offset as an invalid query, not a SQL error", func() {
			_, _, err := ListTenantBillingAssignments(ctx, tx, 0, -1, "", "")
			var invalid *ErrInvalidFlatTableQuery
			Expect(errors.As(err, &invalid)).To(BeTrue(),
				"a negative limit must reach the caller as INVALID_ARGUMENT, not an opaque INTERNAL")
			_, _, err = ListTenantBillingAssignments(ctx, tx, -1, 0, "", "")
			Expect(errors.As(err, &invalid)).To(BeTrue())
		})

		It("rejects an unknown filter field", func() {
			_, _, err := ListTenantBillingAssignments(ctx, tx, 0, 10, `this.spec.message == "x"`, "")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("LockBillingAccountForAssignment", func() {
		It("succeeds for any account id without error", func() {
			Expect(LockBillingAccountForAssignment(ctx, tx, "any-account-id")).To(Succeed())
		})
	})

	Describe("BillingAccountCorrelationConflict and SetBillingAccountCorrelationID", func() {
		It("reports no conflict when the value is unused", func() {
			account := createAccount("")
			conflict, err := BillingAccountCorrelationConflict(ctx, tx, "corr-1", account)
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(BeFalse())
		})

		It("reports a conflict when another account already holds the value", func() {
			other := createAccount("corr-1")
			mine := createAccount("")
			conflict, err := BillingAccountCorrelationConflict(ctx, tx, "corr-1", mine)
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(BeTrue())
			_ = other
		})

		It("excludes the given account id from the conflict check", func() {
			mine := createAccount("corr-1")
			conflict, err := BillingAccountCorrelationConflict(ctx, tx, "corr-1", mine)
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(BeFalse())
		})

		It("sets the correlation id and rejects a value already used by another account", func() {
			createAccount("corr-dup")
			mine := createAccount("")
			var setErr error
			spErr := tx.Savepoint(ctx, func(spCtx context.Context) error {
				spTx, err := TxFromContext(spCtx)
				Expect(err).ToNot(HaveOccurred())
				setErr = SetBillingAccountCorrelationID(spCtx, spTx, mine, "corr-dup")
				return setErr
			})
			Expect(spErr).To(HaveOccurred())
			Expect(errors.Is(setErr, ErrBillingAccountCorrelationConflict)).To(BeTrue())
		})
	})

	Describe("GetBillingAccountForAssignment", func() {
		It("returns the provider reference, correlation id and state", func() {
			account := createAccount("corr-1")
			row, err := GetBillingAccountForAssignment(ctx, tx, account)
			Expect(err).ToNot(HaveOccurred())
			Expect(row.ProviderAccountReference).To(Equal(account))
			Expect(row.CorrelationID).To(Equal("corr-1"))
			Expect(row.State).To(Equal("ACTIVE"))
		})

		It("returns pgx.ErrNoRows for an unknown account", func() {
			_, err := GetBillingAccountForAssignment(ctx, tx, "no-such-account")
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		})
	})
})
