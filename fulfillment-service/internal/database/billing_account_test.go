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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
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
			first := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme", State: "ACTIVE",
			})
			second := upsert(BillingAccountUpsert{
				ProviderKey: "m360", ProviderAccountReference: "acct-1",
				DisplayName: "Acme Renamed", BaseCurrency: "USD", State: "INACTIVE",
			})
			Expect(second).To(Equal(first), "same provider_key+reference must keep the same OSAC id")
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].DisplayName).To(Equal("Acme Renamed"))
			Expect(items[0].BaseCurrency).To(Equal("USD"))
			Expect(items[0].State).To(Equal("INACTIVE"))
		})

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

		It("does not touch accounts under a different provider_key", func() {
			upsert(BillingAccountUpsert{ProviderKey: "m360", ProviderAccountReference: "acct-1", State: "ACTIVE"})
			upsert(BillingAccountUpsert{ProviderKey: "fake", ProviderAccountReference: "acct-1", State: "ACTIVE"})
			Expect(MarkBillingAccountsUnobserved(ctx, tx, "m360", []string{}, time.Now().UTC())).To(Succeed())
			items, _, err := ListBillingAccounts(ctx, tx, 0, 10, `this.provider_key == "fake"`, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(items[0].State).To(Equal("ACTIVE"))
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
})
