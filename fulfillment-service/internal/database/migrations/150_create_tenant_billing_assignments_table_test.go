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

var _ = DescribeMigration("Create tenant billing assignments table", func() {
	createTenant := func(ctx context.Context, name string) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, 'system', '{}')
			 on conflict do nothing`, name)
		Expect(err).ToNot(HaveOccurred())
	}
	createAccount := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into billing_accounts (id, provider_key, provider_account_reference)
			 values ($1, 'm360', $1)`, id)
		Expect(err).ToNot(HaveOccurred())
	}
	insert := func(ctx context.Context, id, name, tenantID, accountID string, effectiveTo any) error {
		_, err := conn.Exec(ctx, `
			insert into tenant_billing_assignments (id, name, tenant_id, billing_account_id, effective_to)
			values ($1, $2, $3, $4, $5)`, id, name, tenantID, accountID, effectiveTo)
		return err
	}

	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 150)).To(Succeed())
		createTenant(ctx, "tenant-a")
		createTenant(ctx, "tenant-b")
		createAccount(ctx, "ba-1")
		createAccount(ctx, "ba-2")
	})

	It("defaults connection_state to PENDING, message to empty, and stamps effective_from", func(ctx context.Context) {
		Expect(insert(ctx, "asg-1", "asg-1", "tenant-a", "ba-1", nil)).To(Succeed())
		var state, message string
		var hasEffectiveFrom bool
		var effectiveTo *string
		err := conn.QueryRow(ctx, `
			select connection_state, message, effective_from is not null, effective_to
			from tenant_billing_assignments where id = $1`, "asg-1").
			Scan(&state, &message, &hasEffectiveFrom, &effectiveTo)
		Expect(err).ToNot(HaveOccurred())
		Expect(state).To(Equal("PENDING"))
		Expect(message).To(BeEmpty())
		Expect(hasEffectiveFrom).To(BeTrue())
		Expect(effectiveTo).To(BeNil())
	})

	It("rejects a second active assignment for the same tenant", func(ctx context.Context) {
		Expect(insert(ctx, "asg-1", "asg-1", "tenant-a", "ba-1", nil)).To(Succeed())
		err := insert(ctx, "asg-2", "asg-2", "tenant-a", "ba-2", nil)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("tenant_billing_assignments_unique_active_tenant"))
	})

	It("rejects a second active assignment for the same billing account", func(ctx context.Context) {
		Expect(insert(ctx, "asg-1", "asg-1", "tenant-a", "ba-1", nil)).To(Succeed())
		err := insert(ctx, "asg-2", "asg-2", "tenant-b", "ba-1", nil)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("tenant_billing_assignments_unique_active_account"))
	})

	It("allows a closed assignment to coexist with a new active one for the same tenant and account", func(ctx context.Context) {
		Expect(insert(ctx, "asg-1", "asg-1", "tenant-a", "ba-1", "2026-01-01T00:00:00Z")).To(Succeed())
		Expect(insert(ctx, "asg-2", "asg-2", "tenant-a", "ba-1", nil)).To(Succeed())
	})

	It("rejects an empty name", func(ctx context.Context) {
		Expect(insert(ctx, "asg-1", "", "tenant-a", "ba-1", nil)).To(HaveOccurred())
	})

	It("rejects a tenant_id that does not reference an existing tenant", func(ctx context.Context) {
		err := insert(ctx, "asg-1", "asg-1", "no-such-tenant", "ba-1", nil)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23503")) // foreign_key_violation
	})

	It("rejects a billing_account_id that does not reference an existing account", func(ctx context.Context) {
		err := insert(ctx, "asg-1", "asg-1", "tenant-a", "no-such-account", nil)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23503")) // foreign_key_violation
	})
})
