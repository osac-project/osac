/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("billing account store", func() {
	It("normalizes a provider currency to its uppercase ISO-4217 code", func() {
		store := billing.NewAccountStore()
		account := billing.Account{Reference: "m360-account-lowercase-currency", DisplayName: "Acme", Currency: "usd"}

		accounts, err := store.SyncAccounts(ctx, "m360", []billing.Account{account}, time.Now().UTC())
		Expect(err).NotTo(HaveOccurred())
		Expect(accounts).To(HaveLen(1))
		Expect(accounts[0].GetBaseCurrency()).To(Equal("USD"))
	})

	It("keeps a missing initial provider currency empty", func() {
		store := billing.NewAccountStore()
		account := billing.Account{Reference: "m360-account-empty-currency", DisplayName: "Acme"}

		accounts, err := store.SyncAccounts(ctx, "m360", []billing.Account{account}, time.Now().UTC())
		Expect(err).NotTo(HaveOccurred())
		Expect(accounts).To(HaveLen(1))
		Expect(accounts[0].GetBaseCurrency()).To(BeEmpty())
	})

	It("rejects unsupported provider currency codes", func() {
		store := billing.NewAccountStore()
		for _, currency := range []string{"US", "ZZZ"} {
			account := billing.Account{
				Reference:   "m360-account-invalid-currency-" + currency,
				DisplayName: "Acme",
				Currency:    currency,
			}
			_, err := store.SyncAccounts(ctx, "m360", []billing.Account{account}, time.Now().UTC())
			Expect(err).To(HaveOccurred(), "currency %q must not be persisted", currency)
		}
	})

	It("keeps a stable opaque OSAC ID when a provider account is rediscovered", func() {
		store := billing.NewAccountStore()
		observedAt := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
		providerAccount := billing.Account{
			Reference:   "m360-account-123",
			DisplayName: "Acme",
			ExternalID:  "tenant-a",
			Currency:    "USD",
			State:       billing.AccountStateActive,
		}

		first, err := store.SyncAccounts(ctx, "m360", []billing.Account{providerAccount}, observedAt)
		Expect(err).NotTo(HaveOccurred())
		providerAccount.Currency = "EUR"
		second, err := store.SyncAccounts(ctx, "m360", []billing.Account{providerAccount}, observedAt.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())

		Expect(first).To(HaveLen(1))
		Expect(second).To(HaveLen(1))
		Expect(first[0].GetId()).NotTo(Equal("m360-account-123"))
		Expect(second[0].GetId()).To(Equal(first[0].GetId()))
		Expect(second[0].GetProviderAccountReference()).To(Equal("m360-account-123"))
		Expect(second[0].GetBaseCurrency()).To(Equal("USD"))
		Expect(second[0].GetStatusObservedAt().AsTime()).To(Equal(observedAt.Add(time.Minute)))
		stored, err := store.GetAccount(ctx, second[0].GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetBaseCurrency()).To(Equal("USD"))
	})
})
