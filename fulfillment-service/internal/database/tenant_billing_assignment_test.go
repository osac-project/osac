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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
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

	// newManager creates a transaction manager over the test pool, for opening transactions independent of the
	// shared one from the BeforeEach above (which stays open, and uncommitted, while a spec runs).
	newManager := func() TxManager {
		manager, err := NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).ToNot(HaveOccurred())
		return manager
	}

	// runCommitted runs fn in its own transaction and commits it, so rows are visible to the independent
	// transactions the concurrency specs open. The shared tx keeps its rows uncommitted until End.
	runCommitted := func(fn func(tx Tx)) {
		Expect(newManager().Run(ctx, fn)).To(Succeed())
	}

	// createCommittedTenant inserts and commits a tenant so concurrent transactions can reference it. It uses
	// names the BeforeEach never inserts, which would otherwise block on its uncommitted primary key entry.
	createCommittedTenant := func(name string) {
		runCommitted(func(tx Tx) {
			_, err := tx.Exec(ctx,
				`insert into tenants (id, name, tenant, creator, data) values ($1, $1, $1, 'system', '{}')
				 on conflict do nothing`, name)
			Expect(err).ToNot(HaveOccurred())
		})
	}

	// createCommittedAccount inserts a billing account and commits it, so concurrent transactions can
	// reference it.
	createCommittedAccount := func(correlationID string) string {
		id := uuid.New()
		runCommitted(func(tx Tx) {
			_, err := UpsertBillingAccount(ctx, tx, BillingAccountUpsert{
				ID: id, ProviderKey: "m360", ProviderAccountReference: id,
				CorrelationID: correlationID, State: "ACTIVE",
			})
			Expect(err).ToNot(HaveOccurred())
		})
		return id
	}

	// endOnce returns a function that ends tx at most once, asserting the outcome. End is not idempotent, so a
	// spec that ends the transaction itself must not have its cleanup end it again.
	endOnce := func(tx Tx) func() {
		ended := false
		return func() {
			if ended {
				return
			}
			ended = true
			Expect(tx.End(ctx)).To(Succeed())
		}
	}

	// tryAdvisory reports whether the advisory lock for accountID, the exact key LockBillingAccountForAssignment
	// takes, can be acquired now from a fresh independent transaction.
	tryAdvisory := func(accountID string) bool {
		var acquired bool
		runCommitted(func(tx Tx) {
			Expect(tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1, 1))`, accountID).
				Scan(&acquired)).To(Succeed())
		})
		return acquired
	}

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

	Describe("CloseTenantBillingAssignmentOnDeletion", func() {
		It("does not close before an assignment committed after the deletion transaction started", func() {
			createCommittedTenant("tenant-c")
			account := createCommittedAccount("")
			deletionTx, err := newManager().Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endDeletion := endOnce(deletionTx)
			DeferCleanup(endDeletion)

			// Materialize the lazy transaction before assignment finalization, without taking the tenant lock.
			// BEGIN and the first locking query are separate commands in the production transaction manager.
			var deletionTime time.Time
			var deletionPID int32
			Expect(deletionTx.QueryRow(ctx, `select now(), pg_backend_pid()`).
				Scan(&deletionTime, &deletionPID)).To(Succeed())

			var assigned TenantBillingAssignmentRow
			runCommitted(func(assignmentTx Tx) {
				var assignmentPID int32
				Expect(assignmentTx.QueryRow(ctx, `select pg_backend_pid()`).Scan(&assignmentPID)).To(Succeed())
				Expect(assignmentPID).ToNot(Equal(deletionPID))
				var name string
				Expect(assignmentTx.QueryRow(ctx, `select name from tenants where name = $1
					and deletion_timestamp = 'epoch' for update`, "tenant-c").Scan(&name)).To(Succeed())
				id, err := CloseAndInsertTenantBillingAssignment(ctx, assignmentTx, "", TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "later-assignment", TenantID: "tenant-c", BillingAccountID: account,
					ConnectionState: "CONNECTED",
				})
				Expect(err).ToNot(HaveOccurred())
				assigned, err = GetTenantBillingAssignment(ctx, assignmentTx, id)
				Expect(err).ToNot(HaveOccurred())
			})
			Expect(assigned.EffectiveFrom.After(deletionTime)).To(BeTrue(),
				"the independent assignment must have a later transaction timestamp")

			// Deletion acquires the same tenant lock only after the assignment has committed.
			var name string
			Expect(deletionTx.QueryRow(ctx, `select name from tenants where name = $1 for update`, "tenant-c").
				Scan(&name)).To(Succeed())
			Expect(CloseTenantBillingAssignmentOnDeletion(ctx, deletionTx, "tenant-c")).To(Succeed())
			endDeletion()

			runCommitted(func(readTx Tx) {
				closed, err := GetTenantBillingAssignment(ctx, readTx, assigned.ID)
				Expect(err).ToNot(HaveOccurred())
				Expect(closed.EffectiveTo).ToNot(BeNil())
				Expect(closed.EffectiveTo.Before(closed.EffectiveFrom)).To(BeFalse(),
					"a deletion transaction predating assignment finalization must not create a negative interval")
				Expect(closed.EffectiveTo.Equal(closed.EffectiveFrom)).To(BeTrue())
				Expect(closed.Message).To(Equal(TenantBillingAssignmentDeletedMessage))
				Expect(closed.ConnectionState).To(Equal(assigned.ConnectionState))
			})
		})

		It("preserves the deletion transaction timestamp when the assignment is older", func() {
			createCommittedTenant("tenant-c")
			account := createCommittedAccount("")
			var id string
			runCommitted(func(assignmentTx Tx) {
				var err error
				id, err = CloseAndInsertTenantBillingAssignment(ctx, assignmentTx, "", TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "earlier-assignment", TenantID: "tenant-c", BillingAccountID: account,
					ConnectionState: "CONNECTED",
				})
				Expect(err).ToNot(HaveOccurred())
			})
			runCommitted(func(deletionTx Tx) {
				var deletionTime time.Time
				Expect(deletionTx.QueryRow(ctx, `select now()`).Scan(&deletionTime)).To(Succeed())
				var name string
				Expect(deletionTx.QueryRow(ctx, `select name from tenants where name = $1 for update`, "tenant-c").
					Scan(&name)).To(Succeed())
				Expect(CloseTenantBillingAssignmentOnDeletion(ctx, deletionTx, "tenant-c")).To(Succeed())
				closed, err := GetTenantBillingAssignment(ctx, deletionTx, id)
				Expect(err).ToNot(HaveOccurred())
				Expect(closed.EffectiveFrom.Before(deletionTime)).To(BeTrue())
				Expect(closed.EffectiveTo).ToNot(BeNil())
				Expect(closed.EffectiveTo.Equal(deletionTime)).To(BeTrue())
			})
		})

		It("closes only the tenant's active interval, preserves history, and is idempotent", func() {
			account := createAccount("")
			first, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "history", TenantID: "tenant-a", BillingAccountID: account,
				ConnectionState: "PENDING", Message: "original history message",
			})
			Expect(err).ToNot(HaveOccurred())
			current, err := CloseAndInsertTenantBillingAssignment(ctx, tx, first, TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "current", TenantID: "tenant-a", BillingAccountID: account,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			historyBefore, err := GetTenantBillingAssignment(ctx, tx, first)
			Expect(err).ToNot(HaveOccurred())
			other, err := CloseAndInsertTenantBillingAssignment(ctx, tx, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "other", TenantID: "tenant-b", BillingAccountID: createAccount(""),
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			otherBefore, err := GetTenantBillingAssignment(ctx, tx, other)
			Expect(err).ToNot(HaveOccurred())

			Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tx, "tenant-a")).To(Succeed())
			closed, err := GetTenantBillingAssignment(ctx, tx, current)
			Expect(err).ToNot(HaveOccurred())
			Expect(closed.EffectiveTo).ToNot(BeNil())
			Expect(closed.EffectiveTo.Equal(closed.EffectiveFrom)).To(BeTrue(),
				"an assignment opened and deleted in one transaction has a zero-length interval")
			Expect(closed.Message).To(Equal(TenantBillingAssignmentDeletedMessage))
			Expect(closed.ConnectionState).To(Equal("CONNECTED"))
			_, err = GetActiveTenantBillingAssignmentByTenant(ctx, tx, "tenant-a")
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
			_, err = GetActiveTenantBillingAssignmentByAccount(ctx, tx, account)
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())

			Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tx, "tenant-a")).To(Succeed())
			repeated, err := GetTenantBillingAssignment(ctx, tx, current)
			Expect(err).ToNot(HaveOccurred())
			Expect(repeated).To(Equal(closed))
			historyAfter, err := GetTenantBillingAssignment(ctx, tx, first)
			Expect(err).ToNot(HaveOccurred())
			Expect(historyAfter).To(Equal(historyBefore))
			otherAfter, err := GetTenantBillingAssignment(ctx, tx, other)
			Expect(err).ToNot(HaveOccurred())
			Expect(otherAfter).To(Equal(otherBefore))
		})

		It("is a no-op when the tenant has no active assignment", func() {
			Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tx, "tenant-a")).To(Succeed())
			Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tx, "missing-tenant")).To(Succeed())
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

		It("scopes SQL counts and pages to finite, universal, empty and exclusion tenant sets", func() {
			for _, tc := range []struct {
				tenants collections.Set[string]
				total   int32
			}{
				{collections.NewSet("tenant-a"), 2},
				{collections.NewSet("tenant-a", "tenant-b"), 3},
				{collections.NewUniversalSet[string](), 3},
				{collections.NewSet[string](), 0},
				{collections.Set[string]{}, 0},
				{collections.NewSet("tenant-b").Negate(), 2},
				{collections.NewSet(`tenant-a' OR true --`), 0},
			} {
				seen := map[string]bool{}
				for offset := int32(0); offset <= tc.total; offset++ {
					items, total, err := ListTenantBillingAssignmentsForTenants(ctx, tx, offset, 1, "", "id", tc.tenants)
					Expect(err).ToNot(HaveOccurred())
					Expect(total).To(Equal(tc.total))
					if offset == tc.total {
						Expect(items).To(BeEmpty())
						continue
					}
					Expect(items).To(HaveLen(1))
					Expect(tc.tenants.Contains(items[0].TenantID)).To(BeTrue())
					Expect(seen[items[0].ID]).To(BeFalse())
					seen[items[0].ID] = true
				}
				Expect(seen).To(HaveLen(int(tc.total)))
			}
		})

		It("intersects caller filters with the SQL tenant scope for current and historical rows", func() {
			scope := collections.NewSet("tenant-a")
			for _, filter := range []string{
				`this.spec.tenant_id == "tenant-b"`,
				`this.spec.billing_account_id == "` + account2 + `" && !has(this.status.effective_to)`,
			} {
				items, total, err := ListTenantBillingAssignmentsForTenants(ctx, tx, 0, 1, filter, "", scope)
				Expect(err).ToNot(HaveOccurred())
				Expect(total).To(BeZero())
				Expect(items).To(BeEmpty())
			}
			items, total, err := ListTenantBillingAssignmentsForTenants(ctx, tx, 0, 1,
				`this.status.connection_state == "CONNECTED" && has(this.status.effective_to)`, "", scope)
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
			Expect(items).To(HaveLen(1))
			Expect(items[0].TenantID).To(Equal("tenant-a"))
			Expect(items[0].EffectiveTo).ToNot(BeNil())
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

	Describe("ListTenantBillingAssignments pagination", func() {
		// seedHistory inserts count closed intervals for tenant-a and one billing account, all sharing one
		// effective_from, the append-only shape the list query pages over. It uses direct SQL because the helper
		// opens one interval at a time and stamps a fresh now(). Every effective_to is set, so the partial unique
		// active indexes are never violated, and the zero-padded ids order the same in Go and PostgreSQL.
		seedHistory := func(count int) []string {
			account := createAccount("")
			from := time.Now().UTC().Truncate(time.Second)
			to := from.Add(time.Hour)
			ids := make([]string, count)
			for i := 0; i < count; i++ {
				ids[i] = fmt.Sprintf("hist-%03d", i)
				_, err := tx.Exec(ctx, `
					insert into tenant_billing_assignments (
						id, name, tenant_id, billing_account_id, effective_from, effective_to, creator
					) values ($1, $1, 'tenant-a', $2, $3, $4, 'system')`,
					ids[i], account, from, to)
				Expect(err).ToNot(HaveOccurred())
			}
			return ids
		}

		idsOf := func(items []TenantBillingAssignmentRow) []string {
			ids := make([]string, len(items))
			for i, item := range items {
				ids[i] = item.ID
			}
			return ids
		}

		It("serves the default page of 100 and continues at offset 100, tie-broken by ascending id", func() {
			ids := seedHistory(105)
			first, total, err := ListTenantBillingAssignments(ctx, tx, 0, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(105)))
			Expect(first).To(HaveLen(100))
			// Every row ties on effective_from, so the latest-first order degenerates to the ascending-id
			// tie-breaker.
			Expect(idsOf(first)).To(Equal(ids[:100]))

			second, total, err := ListTenantBillingAssignments(ctx, tx, 100, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(105)))
			Expect(second).To(HaveLen(5))
			Expect(idsOf(second)).To(Equal(ids[100:]))

			union := append(append([]string{}, idsOf(first)...), idsOf(second)...)
			seen := map[string]bool{}
			for _, id := range union {
				Expect(seen[id]).To(BeFalse(), "the two pages must not overlap: %q appears twice", id)
				seen[id] = true
			}
			Expect(seen).To(HaveLen(105), "the two pages must cover all 105 unique ids")
		})

		It("serves the full default page at exactly 100 rows and an empty page at offset 100", func() {
			seedHistory(100)
			page, total, err := ListTenantBillingAssignments(ctx, tx, 0, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(100)))
			Expect(page).To(HaveLen(100))
			rest, total, err := ListTenantBillingAssignments(ctx, tx, 100, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(100)))
			Expect(rest).To(BeEmpty())
		})
	})

	Describe("LockBillingAccountForAssignment", func() {
		It("succeeds for any account id without error", func() {
			Expect(LockBillingAccountForAssignment(ctx, tx, "any-account-id")).To(Succeed())
		})

		It("holds a real per-account advisory transaction lock: the same account contends, other accounts "+
			"stay usable, and the lock is released when the transaction ends", func() {
			accountA := createCommittedAccount("")
			accountB := createCommittedAccount("")
			createCommittedTenant("tenant-c")
			manager := newManager()

			// Account A is locked by txA, on its own connection:
			txA, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTxA := endOnce(txA)
			DeferCleanup(endTxA)
			Expect(LockBillingAccountForAssignment(ctx, txA, accountA)).To(Succeed())

			// A contends: the same advisory lock cannot be taken from an independent transaction. This is
			// what a no-op lock implementation would fail.
			Expect(tryAdvisory(accountA)).To(BeFalse(), "account A's advisory lock must be held by txA")
			// The lock is keyed per account: B's key stays free while A's is held.
			Expect(tryAdvisory(accountB)).To(BeTrue(), "account B's advisory lock must not be affected")

			// B remains usable end to end while A is locked: an independent transaction locks B, opens B's
			// active assignment and commits.
			txB, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTxB := endOnce(txB)
			DeferCleanup(endTxB)
			Expect(LockBillingAccountForAssignment(ctx, txB, accountB)).To(Succeed())
			_, err = CloseAndInsertTenantBillingAssignment(ctx, txB, "", TenantBillingAssignmentInsert{
				ID: uuid.New(), Name: "asg-b", TenantID: "tenant-c", BillingAccountID: accountB,
				ConnectionState: "CONNECTED",
			})
			Expect(err).ToNot(HaveOccurred())
			endTxB()

			// Release after end: committing txA frees A's lock, and A can be acquired again through the
			// helper.
			endTxA()
			Expect(tryAdvisory(accountA)).To(BeTrue(), "txA's End must have released account A's lock")
			txC, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTxC := endOnce(txC)
			DeferCleanup(endTxC)
			Expect(LockBillingAccountForAssignment(ctx, txC, accountA)).To(Succeed())
			Expect(tryAdvisory(accountA)).To(BeFalse(), "account A's lock must be held again, now by txC")
			endTxC()
		})

		It("blocks another session waiting for the same account's advisory lock until the holding "+
			"transaction ends", func() {
			accountA := createCommittedAccount("")
			manager := newManager()

			// The holder takes A's lock through the production helper, on its own connection:
			txA, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTxA := endOnce(txA)
			DeferCleanup(endTxA)
			var holderPID int32
			Expect(txA.QueryRow(ctx, `select pg_backend_pid()`).Scan(&holderPID)).To(Succeed())
			Expect(LockBillingAccountForAssignment(ctx, txA, accountA)).To(Succeed())

			// The waiter is an independent transaction asking for the same lock with the blocking primitive the
			// helper is built on. Raw SQL is used because LockBillingAccountForAssignment may become fail-fast
			// instead of waiting.
			txW, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTxW := endOnce(txW)
			DeferCleanup(endTxW)
			var waiterPID int32
			Expect(txW.QueryRow(ctx, `select pg_backend_pid()`).Scan(&waiterPID)).To(Succeed())
			Expect(waiterPID).ToNot(Equal(holderPID), "the waiter must run on a distinct connection")

			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			acquired := make(chan error, 1)
			// DeferCleanup runs LIFO, so this runs before txW.End: cancel the wait, then wait for the goroutine to
			// leave Exec. Cancelling alone would leave txW's connection checked out, and ending txW earlier would
			// use the transaction concurrently. The buffered channel is sent once and closed, so a receive always
			// returns.
			DeferCleanup(func() {
				cancel()
				<-acquired
			})
			go func() {
				_, err := txW.Exec(waitCtx, `select pg_advisory_xact_lock(hashtextextended($1, 1))`, accountA)
				acquired <- err
				close(acquired)
			}()

			// Observe the actual blocking through pg_stat_activity's lock data, not by sleeping:
			probe, err := pool.Acquire(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(probe.Release)
			Eventually(func() []int32 {
				var blockedBy []int32
				Expect(probe.QueryRow(ctx, `select pg_blocking_pids($1)`, waiterPID).Scan(&blockedBy)).To(Succeed())
				return blockedBy
			}, 10*time.Second, 50*time.Millisecond).Should(ContainElement(holderPID),
				"the waiter must be blocked by the transaction holding account A's lock")

			// Ending the holding transaction releases the lock and lets the waiter take it:
			endTxA()
			var waitErr error
			Eventually(acquired, 10*time.Second).Should(Receive(&waitErr))
			Expect(waitErr).ToNot(HaveOccurred())
			Expect(tryAdvisory(accountA)).To(BeFalse(), "the waiter must now hold account A's lock")
			endTxW()
			Expect(tryAdvisory(accountA)).To(BeTrue(), "the waiter's End must release account A's lock")
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

	Describe("CloseAndInsertTenantBillingAssignment raced from independent transactions", func() {
		// raceSecondInsertAgainstHolder opens the winner's active interval on one transaction, then inserts the
		// loser's from a second connection. It verifies through pg_stat_activity's pg_blocking_pids, not by
		// sleeping, that the insert blocks on the uncommitted index entry, then commits the winner and expects the
		// partial unique index violation.
		raceSecondInsertAgainstHolder := func(winner, loser TenantBillingAssignmentInsert, uniqueIndex string) string {
			manager := newManager()

			// The first racer opens the active interval on its own transaction; the row and its unique-index
			// entries stay uncommitted while the race runs:
			tx1, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTx1 := endOnce(tx1)
			// Registered here and again below: this one covers a failure before the ordered cleanup, which has to
			// run before the racer's cancel and wait. endOnce makes the duplicate call a no-op.
			DeferCleanup(endTx1)
			var holderPID int32
			Expect(tx1.QueryRow(ctx, `select pg_backend_pid()`).Scan(&holderPID)).To(Succeed())
			winnerID, err := CloseAndInsertTenantBillingAssignment(ctx, tx1, "", winner)
			Expect(err).ToNot(HaveOccurred())

			// The second racer runs on an independent transaction and a distinct connection:
			tx2, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			endTx2 := endOnce(tx2)
			DeferCleanup(endTx2)
			var waiterPID int32
			Expect(tx2.QueryRow(ctx, `select pg_backend_pid()`).Scan(&waiterPID)).To(Succeed())
			Expect(waiterPID).ToNot(Equal(holderPID), "the racing inserts must run on distinct connections")

			// The raced insert blocks inside PostgreSQL on tx1's uncommitted index entry. A bounded context
			// keeps a lost race from hanging the spec.
			racedCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			racedErr := make(chan error, 1)
			// DeferCleanup runs LIFO: the holder is ended first (resolving a still-blocked racer), then the
			// racer's context is canceled, and only then is the waiter's transaction ended, after its
			// goroutine has finished.
			DeferCleanup(func() {
				select {
				case <-racedErr:
				case <-time.After(5 * time.Second):
				}
				endTx2()
			})
			DeferCleanup(cancel)
			go func() {
				_, err := CloseAndInsertTenantBillingAssignment(racedCtx, tx2, "", loser)
				tx2.ReportError(&err)
				racedErr <- err
				close(racedErr)
			}()
			DeferCleanup(endTx1)

			// Observe the actual blocking through pg_stat_activity: the raced insert must wait on the holder's
			// transaction.
			probe, err := pool.Acquire(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(probe.Release)
			Eventually(func() []int32 {
				var blockedBy []int32
				Expect(probe.QueryRow(ctx, `select pg_blocking_pids($1)`, waiterPID).Scan(&blockedBy)).To(Succeed())
				return blockedBy
			}, 10*time.Second, 50*time.Millisecond).Should(ContainElement(holderPID),
				"the raced insert must block on the first racer's transaction")

			// Committing the first racer resolves the race in its favor: the raced insert hits the partial
			// unique index.
			endTx1()
			var raced error
			Eventually(racedErr, 10*time.Second).Should(Receive(&raced))
			endTx2()

			var pgErr *pgconn.PgError
			Expect(errors.As(raced, &pgErr)).To(BeTrue(),
				"the raced insert must fail with a PostgreSQL error, got: %v", raced)
			Expect(pgErr.Code).To(Equal("23505"), "a unique violation, as the server error mapper expects")
			Expect(pgErr.ConstraintName).To(Equal(uniqueIndex))
			return winnerID
		}

		// readActive reads one active assignment row in its own committed transaction, after the race has
		// been resolved.
		readActive := func(read func(tx Tx) (TenantBillingAssignmentRow, error)) TenantBillingAssignmentRow {
			var row TenantBillingAssignmentRow
			Expect(newManager().Run(ctx, func(tx Tx) error {
				var err error
				row, err = read(tx)
				return err
			})).To(Succeed())
			return row
		}

		It("rejects a raced second active interval for the same tenant", func() {
			createCommittedTenant("tenant-c")
			account1 := createCommittedAccount("")
			account2 := createCommittedAccount("")

			winner := raceSecondInsertAgainstHolder(
				TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "asg-race-first", TenantID: "tenant-c",
					BillingAccountID: account1, ConnectionState: "CONNECTED",
				},
				TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "asg-race-second", TenantID: "tenant-c",
					BillingAccountID: account2, ConnectionState: "CONNECTED",
				},
				"tenant_billing_assignments_unique_active_tenant",
			)

			// The committed winner remains the single active interval for the tenant:
			active := readActive(func(tx Tx) (TenantBillingAssignmentRow, error) {
				return GetActiveTenantBillingAssignmentByTenant(ctx, tx, "tenant-c")
			})
			Expect(active.ID).To(Equal(winner))
		})

		It("rejects a raced second active interval for the same billing account", func() {
			createCommittedTenant("tenant-c")
			createCommittedTenant("tenant-d")
			account := createCommittedAccount("")

			winner := raceSecondInsertAgainstHolder(
				TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "asg-race-first", TenantID: "tenant-c",
					BillingAccountID: account, ConnectionState: "CONNECTED",
				},
				TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: "asg-race-second", TenantID: "tenant-d",
					BillingAccountID: account, ConnectionState: "CONNECTED",
				},
				"tenant_billing_assignments_unique_active_account",
			)

			// The committed winner remains the single active interval for the account:
			active := readActive(func(tx Tx) (TenantBillingAssignmentRow, error) {
				return GetActiveTenantBillingAssignmentByAccount(ctx, tx, account)
			})
			Expect(active.ID).To(Equal(winner))
		})
	})
})
