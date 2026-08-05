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

var _ = DescribeMigration("Create identity providers tables", func() {
	It("Creates the identity_providers table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 64)
		Expect(err).ToNot(HaveOccurred())

		// Verify the identity_providers table exists:
		var exists bool
		row := conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'identity_providers'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Verify the archived_identity_providers table exists:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'archived_identity_providers'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Can insert and query an identity provider", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 64)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"test-tenant", "Test Tenant", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert a test identity provider:
		_, err = conn.Exec(
			ctx,
			`insert into identity_providers (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"test-idp-id", "test-ldap", "test-creator", "test-tenant", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Query it back:
		var name, creator, tenant string
		row := conn.QueryRow(
			ctx,
			`select name, creator, tenant from identity_providers where id = $1`,
			"test-idp-id",
		)
		err = row.Scan(&name, &creator, &tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("test-ldap"))
		Expect(creator).To(Equal("test-creator"))
		Expect(tenant).To(Equal("test-tenant"))
	})

	It("Allows identity providers with same name in different tenants", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 64)
		Expect(err).ToNot(HaveOccurred())

		// Create organizations first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"tenant-1", "Tenant 1", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"tenant-2", "Tenant 2", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert identity provider in tenant1:
		_, err = conn.Exec(
			ctx,
			`insert into identity_providers (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"idp-1", "corporate-ldap", "user-1", "tenant-1", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert identity provider with same name in tenant2 (should succeed):
		_, err = conn.Exec(
			ctx,
			`insert into identity_providers (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"idp-2", "corporate-ldap", "user-2", "tenant-2", "{}",
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects duplicate identity provider names within the same tenant", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 64)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"tenant-1", "Tenant 1", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert first identity provider:
		_, err = conn.Exec(
			ctx,
			`insert into identity_providers (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"idp-3", "corporate-ldap", "user-1", "tenant-1", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to insert identity provider with same name in same tenant (should fail):
		_, err = conn.Exec(
			ctx,
			`insert into identity_providers (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"idp-4", "corporate-ldap", "user-1", "tenant-1", "{}",
		)
		Expect(err).To(HaveOccurred())

		// Verify it's a unique violation:
		var pgErr *pgconn.PgError
		Expect(err).To(BeAssignableToTypeOf(pgErr))
		pgErr = func() *pgconn.PgError {
			target := &pgconn.PgError{}
			_ = errors.As(err, &target)
			return target
		}()
		Expect(pgErr.Code).To(Equal("23505")) // unique_violation
		Expect(pgErr.ConstraintName).To(Equal("identity_providers_tenant_name_unique"))
	})

})
