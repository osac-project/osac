/*
Copyright (c) 2025 Red Hat Inc.

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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/database"
)

var _ = Describe("Lock", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		pool    *pgxpool.Pool
		tm      database.TxManager
		generic *GenericDAO[*testsv1.Object]
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Prepare the database pool:
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		// Create the transaction manager:
		tm, err = database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create a tenancy logic without restrictions:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewUniversalSet[string](), nil).
			AnyTimes()
		DeferCleanup(ctrl.Finish)

		// Create the DAO:
		generic, err = NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the tenant used in the tests:
		_, err = pool.Exec(ctx, `
			insert into tenants (
				id,
				name,
				tenant,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'my-tenant',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	// createObject inserts an object directly into the database using auto-commit so that it is visible to all
	// transactions.
	createObject := func(ctx context.Context, id string, tenant string) {
		err := tm.Run(ctx, func(tx database.Tx) {
			_, err := tx.Exec(
				ctx,
				"insert into objects (id, tenant, data) values ($1, $2, '{}')",
				id, tenant,
			)
			Expect(err).ToNot(HaveOccurred())
		})
		Expect(err).ToNot(HaveOccurred())
	}

	// fetchUnlocked fetches the identifiers of the objects that are not locked by using 'for update skip locked'.
	fetchUnlocked := func(ctx context.Context, ids ...string) []string {
		var result []string
		err := tm.Run(
			ctx,
			func(tx database.Tx) {
				rows, err := tx.Query(
					ctx,
					"select id from objects where id = any($1) for update skip locked",
					ids,
				)
				Expect(err).ToNot(HaveOccurred())
				defer rows.Close()
				for rows.Next() {
					var id string
					err = rows.Scan(&id)
					Expect(err).ToNot(HaveOccurred())
					result = append(result, id)
				}
				err = rows.Err()
				Expect(err).ToNot(HaveOccurred())
			},
		)
		Expect(err).ToNot(HaveOccurred())
		return result
	}

	// checkLocked verifies that the given identifiers correspond to rows that are currently locked by using 'for
	// update skip locked' to detect locked rows. If a row is locked by another transaction, 'skip locked' will skip
	// it, so an empty result means all the rows are locked.
	checkLocked := func(ctx context.Context, ids ...string) {
		unlocked := fetchUnlocked(ctx, ids...)
		Expect(unlocked).To(BeEmpty())
	}

	// checkNotLocked verifies that the given identifiers correspond to rows that are not locked by using the same
	// 'for update skip locked' technique. If a row is not locked it will be returned, so all the requested rows
	// being returned means none of them are locked.
	checkNotLocked := func(ctx context.Context, ids ...string) {
		unlocked := fetchUnlocked(ctx, ids...)
		Expect(unlocked).To(ConsistOf(ids))
	}

	It("Locks a single object", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) {
				createObject(ctx, "obj1", "my-tenant")
				_, err := generic.Lock().AddId("obj1").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				checkLocked(ctx, "obj1")
			},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Locks multiple objects", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) {
				objects := []string{"obj1", "obj2", "obj3"}
				for _, object := range objects {
					createObject(ctx, object, "my-tenant")
				}
				_, err := generic.Lock().AddIds(objects...).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				checkLocked(ctx, objects...)
			},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Fails with not found error when locking non-existent object", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) {
				_, err := generic.Lock().AddId("does-not-exist").Do(ctx)
				Expect(err).To(HaveOccurred())
				var notFoundErr *ErrNotFound
				Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Fails when one of multiple objects doesn't exist", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) {
				createObject(ctx, "obj1", "my-tenant")
				_, err := generic.Lock().AddIds("obj1", "does-not-exist").Do(ctx)
				Expect(err).To(HaveOccurred())
				var notFoundErr *ErrNotFound
				Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Unlocks object when transaction is committed", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) {
				createObject(ctx, "obj1", "my-tenant")
				_, err := generic.Lock().AddId("obj1").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				checkLocked(ctx, "obj1")
			},
		)
		Expect(err).ToNot(HaveOccurred())
		checkNotLocked(ctx, "obj1")
	})

	It("Unlocks object when transaction is rolled back", func() {
		err := tm.Run(
			ctx,
			func(ctx context.Context) error {
				createObject(ctx, "obj1", "my-tenant")
				_, err := generic.Lock().AddId("obj1").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				return errors.New("my error")
			},
		)
		Expect(err).To(MatchError("my error"))
		checkNotLocked(ctx, "obj1")
	})

	It("Prevents deadlocks by locking in consistent order", func() {
		createObject(ctx, "a", "my-tenant")
		createObject(ctx, "b", "my-tenant")

		// Start a transaction and lock 'a' using direct SQL:
		tx, err := pool.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, "select id from objects where id = 'a' for update")
		Expect(err).ToNot(HaveOccurred())

		// Start a goroutine that tries to lock 'b' and 'a' (in that order) via the DAO.
		// Because the DAO sorts identifiers, it will try to lock 'a' first, which is held
		// by tx1, so it will block before reaching 'b':
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			err := tm.Run(
				ctx,
				func(ctx context.Context) error {
					_, err := generic.Lock().AddIds("b", "a").Do(ctx)
					return err
				},
			)
			done <- err
		}()

		// Give the goroutine time to reach the blocking lock on 'a' and verify that it doesn't complete while
		// 'a' is held:
		Consistently(done, 100*time.Millisecond).ShouldNot(Receive())

		// Verify that 'b' is not locked, proving that the DAO tried to lock 'a' first even though 'b' was
		// passed first:
		checkNotLocked(ctx, "b")

		// Release 'a' by committing, allowing the goroutine to proceed:
		err = tx.Commit(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the goroutine completed without error:
		Eventually(done, time.Second).Should(Receive(BeNil()))
	})
})
