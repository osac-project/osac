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
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Billing accounts", func() {
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

	upsert := func(in BillingAccountUpsert) string {
		if in.ID == "" {
			in.ID = uuid.New()
		}
		if in.State == "" {
			in.State = "UNKNOWN"
		}
		if in.StatusObservedAt.IsZero() {
			in.StatusObservedAt = time.Now().UTC().Truncate(time.Second)
		}
		id, err := UpsertBillingAccount(ctx, tx, in)
		Expect(err).ToNot(HaveOccurred())
		return id
	}

	Describe("UpsertBillingAccount", func() {
		It("inserts a new row with the given id", func() {
			id := uuid.New()
			got := upsert(BillingAccountUpsert{
				ID: id, ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme", State: "ACTIVE",
			})
			Expect(got).To(Equal(id))
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
			Expect(items[0].DisplayName).To(Equal("Acme"))
			Expect(items[0].State).To(Equal("ACTIVE"))
		})

		It("refreshes display_name, base_currency and state on a second discovery", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			first := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme", BaseCurrency: "USD", State: "ACTIVE", StatusObservedAt: observed,
			})
			second := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme Renamed", BaseCurrency: "EUR", State: "INACTIVE",
				StatusObservedAt: observed.Add(time.Minute),
			})
			Expect(second).To(Equal(first), "same provider_key+reference must keep the same OSAC id")
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].DisplayName).To(Equal("Acme Renamed"))
			Expect(items[0].BaseCurrency).To(Equal("EUR"), "a newer provider currency must refresh the stored value")
			Expect(items[0].State).To(Equal("INACTIVE"))
		})

		It("keeps state and status_observed_at when an older observation is applied last", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			older := observed.Add(-time.Minute)
			id := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme", State: "ACTIVE", StatusObservedAt: observed,
			})
			// A walk that started before the one above - a rolling update briefly runs two servers - can
			// still commit after it, so its older observation must not overwrite the stored one.
			got := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme Stale", State: "INACTIVE", StatusObservedAt: older,
			})
			// The guard must be a conditional assignment, never a WHERE on the conflict branch: a WHERE
			// would suppress the row, and returning id would then yield no row at all.
			Expect(got).To(Equal(id), "the stale upsert must still return the existing row's id")
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
			Expect(items[0].State).To(Equal("ACTIVE"), "a stale walk must not restore its state")
			Expect(items[0].StatusObservedAt).To(BeTemporally("==", observed),
				"the observation time must never move backwards")
			Expect(items[0].DisplayName).To(Equal("Acme"),
				"a stale walk must not revert a newer display_name")
		})

		DescribeTable("applies an observation that is not older than the stored one",
			func(delta time.Duration) {
				observed := time.Now().UTC().Truncate(time.Second)
				upsert(BillingAccountUpsert{
					ProviderKey: "m360", ProviderAccountReference: "acct-1",
					State: "ACTIVE", StatusObservedAt: observed,
				})
				upsert(BillingAccountUpsert{
					ProviderKey: "m360", ProviderAccountReference: "acct-1",
					State: "INACTIVE", StatusObservedAt: observed.Add(delta),
				})
				items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
				Expect(err).ToNot(HaveOccurred())
				Expect(items[0].State).To(Equal("INACTIVE"),
					"the observation-order guard must not freeze state updates")
				Expect(items[0].StatusObservedAt).To(BeTemporally("==", observed.Add(delta)))
			},
			Entry("equal observation time", time.Duration(0)),
			Entry("newer observation time", time.Minute),
		)

		It("never clears display_name or base_currency when the provider does not supply one", func() {
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme", BaseCurrency: "USD", State: "ACTIVE",
			})
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "", BaseCurrency: "", State: "ACTIVE",
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].DisplayName).To(Equal("Acme"))
			Expect(items[0].BaseCurrency).To(Equal("USD"))
		})

		DescribeTable("refreshes a nonempty base_currency only from a not-older observation",
			func(delta time.Duration, currency, expected string) {
				observed := time.Now().UTC().Truncate(time.Second)
				upsert(BillingAccountUpsert{
					ProviderKey: "m360", ProviderAccountReference: "acct-1", BaseCurrency: "USD",
					StatusObservedAt: observed,
				})
				upsert(BillingAccountUpsert{
					ProviderKey: "m360", ProviderAccountReference: "acct-1", BaseCurrency: currency,
					StatusObservedAt: observed.Add(delta),
				})
				items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
				Expect(err).ToNot(HaveOccurred())
				Expect(items[0].BaseCurrency).To(Equal(expected))
			},
			Entry("equal observation time refreshes", time.Duration(0), "EUR", "EUR"),
			Entry("newer observation time refreshes", time.Minute, "EUR", "EUR"),
			Entry("stale observation cannot re-denominate", -time.Minute, "EUR", "USD"),
			Entry("empty currency never erases a known value", time.Minute, "", "USD"),
		)

		It("keeps a known currency when the observation carries a zero timestamp", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", BaseCurrency: "USD",
				StatusObservedAt: observed,
			})
			_, err := UpsertBillingAccount(ctx, tx, BillingAccountUpsert{
				ID: uuid.New(), ProviderKey: "m360", ProviderAccountReference: "acct-1",
				BaseCurrency: "EUR", State: "UNKNOWN",
			})
			Expect(err).ToNot(HaveOccurred())
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].BaseCurrency).To(Equal("USD"),
				"a zero observation time is older than the stored observation and must not overwrite currency")
		})

		It("does not initialize currency from an older observation", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", StatusObservedAt: observed,
			})
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", BaseCurrency: "USD",
				StatusObservedAt: observed.Add(-time.Second),
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].BaseCurrency).To(BeEmpty())
		})

		It("copies the provider correlation value only when the local value is null", func() {
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", CorrelationID: "ext-1", State: "ACTIVE",
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].CorrelationID).To(Equal("ext-1"))
		})

		It("never overwrites a non-null local correlation_id with a different provider value", func() {
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", CorrelationID: "ext-1", State: "ACTIVE",
			})
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", CorrelationID: "ext-2", State: "ACTIVE",
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].CorrelationID).To(Equal("ext-1"), "discovery must never overwrite a differing value")
		})

		It("leaves both rows unchanged when two accounts would share one correlation_id", func() {
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", CorrelationID: "ext-dup", State: "ACTIVE",
			})
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-2", CorrelationID: "ext-dup", State: "ACTIVE",
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, `this.provider_key == "m360"`, "id")
			Expect(err).ToNot(HaveOccurred())
			var withCorrelation int
			for _, item := range items {
				if item.CorrelationID == "ext-dup" {
					withCorrelation++
				}
			}
			Expect(withCorrelation).To(Equal(1), "the unique index must never be violated by a second write")
		})

		It("leaves correlation_id null when the account has no native value", func() {
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", State: "ACTIVE",
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].CorrelationID).To(BeEmpty())
		})
	})

	Describe("ObserveBillingAccountState", func() {
		It("applies a not-older observation and returns the stored one", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			id := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", DisplayName: "Acme",
				CorrelationID: "corr", State: "ACTIVE", StatusObservedAt: observed,
			})
			state, at, err := ObserveBillingAccountState(ctx, tx, id, "INACTIVE", observed.Add(time.Minute))
			Expect(err).ToNot(HaveOccurred())
			Expect(state).To(Equal("INACTIVE"))
			Expect(at).To(BeTemporally("==", observed.Add(time.Minute)))
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("INACTIVE"))
			Expect(items[0].StatusObservedAt).To(BeTemporally("==", observed.Add(time.Minute)))
			Expect(items[0].DisplayName).To(Equal("Acme"), "an assignment read must not change account metadata")
			Expect(items[0].CorrelationID).To(Equal("corr"))
		})

		It("ignores an older observation and returns the newer stored one", func() {
			observed := time.Now().UTC().Truncate(time.Second)
			id := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", State: "INACTIVE",
				StatusObservedAt: observed,
			})
			// A concurrent discovery stored a newer state while this read was in flight. The older assignment
			// read must not move the observation backwards or restore its own state, and the newer state is
			// what eligibility must evaluate.
			state, at, err := ObserveBillingAccountState(ctx, tx, id, "ACTIVE", observed.Add(-time.Minute))
			Expect(err).ToNot(HaveOccurred())
			Expect(state).To(Equal("INACTIVE"))
			Expect(at).To(BeTemporally("==", observed))
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("INACTIVE"))
			Expect(items[0].StatusObservedAt).To(BeTemporally("==", observed))
		})

		It("returns not found for an unknown account", func() {
			_, _, err := ObserveBillingAccountState(ctx, tx, "missing", "ACTIVE", time.Now().UTC())
			Expect(errors.Is(err, pgx.ErrNoRows)).To(BeTrue())
		})
	})

	Describe("MarkBillingAccountsUnobserved", func() {
		It("marks UNKNOWN an account missing from the latest discovery pass, retaining the row", func() {
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-1", DisplayName: "Kept", State: "ACTIVE"})
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-2", DisplayName: "Vanished", State: "ACTIVE"})
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", []string{"acct-1"}, time.Now().UTC())).To(Succeed())
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(2)), "the row must be retained, not deleted")
			Expect(items[0].DisplayName).To(Equal("Kept"))
			Expect(items[0].State).To(Equal("ACTIVE"))
			Expect(items[1].DisplayName).To(Equal("Vanished"))
			Expect(items[1].State).To(Equal("UNKNOWN"))
		})

		It("leaves a row untouched when its observation is newer than the sweep", func() {
			sweepAt := time.Now().UTC().Truncate(time.Second)
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1", DisplayName: "Fresh",
				State: "ACTIVE", StatusObservedAt: sweepAt.Add(time.Minute),
			})
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-2", DisplayName: "Stale",
				State: "ACTIVE", StatusObservedAt: sweepAt.Add(-time.Minute),
			})
			// A walk that started after this sweep already stored acct-1, so the older sweep committing last must
			// not demote it.
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, sweepAt)).To(Succeed())
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(2)))
			Expect(items[0].DisplayName).To(Equal("Fresh"))
			Expect(items[0].State).To(Equal("ACTIVE"),
				"a sweep older than the stored observation must not demote")
			Expect(items[0].StatusObservedAt).To(BeTemporally("==", sweepAt.Add(time.Minute)))
			Expect(items[1].DisplayName).To(Equal("Stale"))
			Expect(items[1].State).To(Equal("UNKNOWN"))
		})

		It("advances the observation time of an account a later sweep also omits", func() {
			first := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			second := first.Add(30 * time.Second)
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				State: "ACTIVE", StatusObservedAt: first,
			})
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, first)).To(Succeed())
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, second)).To(Succeed())
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("UNKNOWN"))
			Expect(items[0].StatusObservedAt).To(BeTemporally("==", second),
				"a sweep must move an already UNKNOWN row's observation forward")
		})

		It("keeps an account UNKNOWN when a walk that read it earlier commits after the newer sweep", func() {
			walkAt := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
			sweepAt := walkAt.Add(30 * time.Second)
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				State: "ACTIVE", StatusObservedAt: walkAt,
			})
			// The sweep that omits the account commits first, carrying the newer observation. The walk that still
			// listed it commits last, carrying the older one, and must not restore it.
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, walkAt)).To(Succeed())
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, sweepAt)).To(Succeed())
			upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				State: "ACTIVE", StatusObservedAt: walkAt,
			})
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("UNKNOWN"),
				"the newer sweep must win over a walk that read the account earlier")
		})

		It("does not touch accounts under a different provider_key", func() {
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-1", State: "ACTIVE"})
			upsert(BillingAccountUpsert{ProviderKey: "fake", ProviderAccountReference: "acct-1", State: "ACTIVE"})
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", []string{}, time.Now().UTC())).To(Succeed())
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, `this.provider_key == "fake"`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("ACTIVE"))
		})

		It("marks every account of the provider UNKNOWN when seenReferences is nil, like empty", func() {
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-1", State: "ACTIVE"})
			// A nil slice must behave like an empty one: pgx would encode it as SQL NULL, and the sweep's
			// not-in predicate would match no rows, silently skipping the vanished-account sweep.
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", nil, time.Now().UTC())).To(Succeed())
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)), "the row must be retained, not deleted")
			Expect(items[0].State).To(Equal("UNKNOWN"))
		})
	})

	Describe("ListBillingAccounts", func() {
		BeforeEach(func() {
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-1", DisplayName: "Beta", State: "ACTIVE"})
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-2", DisplayName: "Alpha", State: "INACTIVE"})
		})

		It("filters by an exact field match", func() {
			items, total, err := ListBillingAccounts(ctx, tx, 0, 10, `this.state == "INACTIVE"`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
			Expect(items[0].DisplayName).To(Equal("Alpha"))
		})

		It("orders by a known field ascending and descending", func() {
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].DisplayName).To(Equal("Alpha"))
			items, _, err = ListBillingAccounts(ctx, tx, 0, 10, "", "display_name desc")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].DisplayName).To(Equal("Beta"))
		})

		It("applies offset and limit", func() {
			items, total, err := ListBillingAccounts(ctx, tx, 1, 1, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(2)))
			Expect(items).To(HaveLen(1))
			Expect(items[0].DisplayName).To(Equal("Beta"))
		})

		It("rejects a negative limit or offset as an invalid query, not a SQL error", func() {
			_, _, err := ListBillingAccounts(ctx, tx, 0, -1, "", "")
			var invalid *ErrInvalidFlatTableQuery
			Expect(errors.As(err, &invalid)).To(BeTrue(),
				"a negative limit must reach the caller as INVALID_ARGUMENT, not an opaque INTERNAL")
			_, _, err = ListBillingAccounts(ctx, tx, -1, 0, "", "")
			Expect(errors.As(err, &invalid)).To(BeTrue())
		})

		It("rejects a filter outside the supported grammar", func() {
			_, _, err := ListBillingAccounts(ctx, tx, 0, 10, `this.state != "ACTIVE"`, "")
			Expect(err).To(HaveOccurred())
		})

		It("rejects a filter on a private or unknown field", func() {
			_, _, err := ListBillingAccounts(ctx, tx, 0, 10, `this.provider_account_reference == "acct-1"`, "")
			Expect(err).To(HaveOccurred())
		})

		It("rejects an order on a private or unknown field", func() {
			_, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "provider_account_reference")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ListBillingAccounts pagination", func() {
		// seedAccounts inserts count accounts with unique provider references and, when non-empty, one shared
		// display_name. The zero-padded fixed-shape ids order the same in Go and PostgreSQL, so expected pages are
		// exact.
		seedAccounts := func(count int, displayName string) []string {
			ids := make([]string, count)
			for i := 0; i < count; i++ {
				ids[i] = fmt.Sprintf("seed-%03d", i)
				upsert(BillingAccountUpsert{
					ID: ids[i], ProviderKey: "m360",
					ProviderAccountReference: fmt.Sprintf("acct-%03d", i),
					DisplayName:              displayName, State: "ACTIVE",
				})
			}
			return ids
		}

		idsOf := func(items []BillingAccountRow) []string {
			ids := make([]string, len(items))
			for i, item := range items {
				ids[i] = item.ID
			}
			return ids
		}

		It("serves the default page of 100 and continues at offset 100 without overlap or loss", func() {
			ids := seedAccounts(105, "Acme")
			first, total, err := ListBillingAccounts(ctx, tx, 0, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(105)))
			Expect(first).To(HaveLen(100))
			// The default order is the unique id, so page one is exactly the first 100 ids ascending.
			Expect(idsOf(first)).To(Equal(ids[:100]))

			second, total, err := ListBillingAccounts(ctx, tx, 100, 0, "", "")
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

		It("breaks ties on an explicit non-unique order with ascending id across pages", func() {
			ids := seedAccounts(6, "Same Name")
			first, total, err := ListBillingAccounts(ctx, tx, 0, 3, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(6)))
			Expect(first).To(HaveLen(3))
			// All six rows tie on display_name, so the pages must fall back to the ascending-id
			// tie-breaker rather than an arbitrary within-tie order.
			Expect(idsOf(first)).To(Equal(ids[:3]))

			second, total, err := ListBillingAccounts(ctx, tx, 3, 3, "", "display_name")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(6)))
			Expect(second).To(HaveLen(3))
			Expect(idsOf(second)).To(Equal(ids[3:]))
			Expect(append(idsOf(first), idsOf(second)...)).To(Equal(ids),
				"the concatenation must be the full ascending-id sequence")
		})

		It("serves the full default page at exactly 100 rows and an empty page at offset 100", func() {
			seedAccounts(100, "Acme")
			page, total, err := ListBillingAccounts(ctx, tx, 0, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(100)))
			Expect(page).To(HaveLen(100))
			rest, total, err := ListBillingAccounts(ctx, tx, 100, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(100)))
			Expect(rest).To(BeEmpty())
		})

		It("returns no items and total 0 for an empty collection", func() {
			items, total, err := ListBillingAccounts(ctx, tx, 0, 0, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(0)))
			Expect(items).To(BeEmpty())
		})
	})
})
