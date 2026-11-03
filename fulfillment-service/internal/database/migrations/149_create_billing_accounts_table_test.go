/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create billing accounts table", func() {
	insert := func(ctx context.Context, id, providerKey, providerRef string, correlationID *string) error {
		_, err := conn.Exec(ctx, `
			insert into billing_accounts (id, provider_key, provider_account_reference, correlation_id)
			values ($1, $2, $3, $4)`, id, providerKey, providerRef, correlationID)
		return err
	}

	It("defaults display_name, base_currency and state, and stamps status_observed_at", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		Expect(insert(ctx, "ba-1", "m360", "acct-1", nil)).To(Succeed())
		var displayName, baseCurrency, state string
		var correlationID *string
		var hasObservedAt bool
		err := conn.QueryRow(ctx, `
			select display_name, base_currency, state, correlation_id, status_observed_at is not null
			from billing_accounts where id = $1`, "ba-1").
			Scan(&displayName, &baseCurrency, &state, &correlationID, &hasObservedAt)
		Expect(err).ToNot(HaveOccurred())
		Expect(displayName).To(BeEmpty())
		Expect(baseCurrency).To(BeEmpty())
		Expect(state).To(Equal("UNKNOWN"))
		Expect(correlationID).To(BeNil())
		Expect(hasObservedAt).To(BeTrue())
	})

	It("rejects a second account for the same provider_key and provider_account_reference", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		Expect(insert(ctx, "ba-1", "m360", "acct-1", nil)).To(Succeed())
		err := insert(ctx, "ba-2", "m360", "acct-1", nil)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("billing_accounts_unique_provider_account"))
	})

	It("allows the same provider_account_reference under a different provider_key", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		Expect(insert(ctx, "ba-1", "m360", "acct-1", nil)).To(Succeed())
		Expect(insert(ctx, "ba-2", "fake", "acct-1", nil)).To(Succeed())
	})

	It("rejects two accounts sharing one non-null correlation_id", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		corr := "corr-1"
		Expect(insert(ctx, "ba-1", "m360", "acct-1", &corr)).To(Succeed())
		err := insert(ctx, "ba-2", "m360", "acct-2", &corr)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("billing_accounts_unique_correlation_id"))
	})

	It("allows more than one account with a null correlation_id", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		Expect(insert(ctx, "ba-1", "m360", "acct-1", nil)).To(Succeed())
		Expect(insert(ctx, "ba-2", "m360", "acct-2", nil)).To(Succeed())
	})

	It("rejects an empty provider_key or provider_account_reference", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		Expect(insert(ctx, "ba-1", "", "acct-1", nil)).To(HaveOccurred())
		Expect(insert(ctx, "ba-1", "m360", "", nil)).To(HaveOccurred())
	})
})
