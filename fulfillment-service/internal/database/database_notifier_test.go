/*
Copyright (c) 2025 Red Hat Inc.

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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Notifier", func() {
	const channel = "my_channel"

	var (
		ctx  context.Context
		pool *pgxpool.Pool
		tm   TxManager
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Prepare the database pool:
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		// Prepare the transaction manager:
		tm, err = NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be created when all the required parameters are set", func() {
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(notifier).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			notifier, err := NewNotifier().
				SetChannel(channel).
				SetPool(pool).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(notifier).To(BeNil())
		})

		It("Can't be created without a channel", func() {
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).To(MatchError("channel is mandatory"))
			Expect(notifier).To(BeNil())
		})

		It("Can't be created without a pool", func() {
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				Build()
			Expect(err).To(MatchError("database connection pool is mandatory"))
			Expect(notifier).To(BeNil())
		})

		It("Can't be created with negative cleanup interval", func() {
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				SetCleanupInterval(-1 * time.Second).
				Build()
			Expect(err).To(MatchError("cleanup interval should be positive, but it is -1s"))
			Expect(notifier).To(BeNil())
		})

		It("Can't be created with zero cleanup interval", func() {
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				SetCleanupInterval(0).
				Build()
			Expect(err).To(MatchError("cleanup interval should be positive, but it is 0s"))
			Expect(notifier).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Sends notification", func() {
			// Create the notifier:
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Send the notification:
			payload := wrapperspb.Int32(42)
			err = tm.Run(ctx, notifier.Notify, payload)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Deletes old notifications asynchronously", func() {
			// Create a cancellable context for the cleanup goroutine:
			cleanupCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			// Create the notifier with a short cleanup interval:
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				SetCleanupInterval(100 * time.Millisecond).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the cleanup goroutine:
			err = notifier.Start(cleanupCtx)
			Expect(err).ToNot(HaveOccurred())

			// Manually create notifications older than one minute:
			_, err = pool.Exec(
				ctx,
				`
				insert into notifications (id, creation_timestamp, payload) values
				('123', now() - interval '2 minutes', 'junk'),
				('456', now() - interval '10 minutes', 'stuff')
				`,
			)
			Expect(err).ToNot(HaveOccurred())

			// Wait for the cleanup to run:
			Eventually(func(g Gomega) {
				row := pool.QueryRow(ctx, `select count(*) from notifications where id in ('123', '456')`)
				var count int
				err := row.Scan(&count)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(count).To(BeZero())
			}).Should(Succeed())
		})

		It("Stops cleanup when context is cancelled", func() {
			// Create a cancellable context for the cleanup goroutine:
			var cancel context.CancelFunc
			cleanupCtx, cancel := context.WithCancel(ctx)

			// Create the notifier with a short cleanup interval:
			notifier, err := NewNotifier().
				SetLogger(logger).
				SetChannel(channel).
				SetPool(pool).
				SetCleanupInterval(50 * time.Millisecond).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Start the cleanup goroutine:
			err = notifier.Start(cleanupCtx)
			Expect(err).ToNot(HaveOccurred())

			// Cancel the context to stop the cleanup:
			cancel()

			// Give the goroutine time to stop:
			time.Sleep(100 * time.Millisecond)

			// Insert old notifications after the goroutine has stopped:
			_, err = pool.Exec(
				ctx,
				`
				insert into notifications (id, creation_timestamp, payload) values
				('789', now() - interval '2 minutes', 'data')
				`,
			)
			Expect(err).ToNot(HaveOccurred())

			// Wait a bit and verify the notification is still there (cleanup stopped):
			time.Sleep(150 * time.Millisecond)
			row := pool.QueryRow(ctx, `select count(*) from notifications where id = '789'`)
			var count int
			err = row.Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1))
		})
	})
})
