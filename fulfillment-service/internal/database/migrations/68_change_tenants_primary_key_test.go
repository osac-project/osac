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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Change tenants primary key", func() {
	It("Changes the primary key from identifier to name", func(ctx context.Context) {
		// Insert a tenant before the migration:
		_, err := conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'my-tenant',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 68)
		Expect(err).ToNot(HaveOccurred())

		// Verify that 'name' is now the primary key by attempting to insert a duplicate name:
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'my-tenant',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenants_pkey"))
	})

	It("Preserves uniqueness of the id column", func(ctx context.Context) {
		// Run the migration on an empty database:
		err := tool.Migrate(ctx, 68)
		Expect(err).ToNot(HaveOccurred())

		// Insert a tenant:
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'tenant-a',
				'tenant-a',
				'tenant-a',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Bypass the check constraint temporarily to verify the unique constraint on identifier independently.
		// This is done by setting the identifier to a value that already exists:
		_, err = conn.Exec(ctx, `alter table tenants drop constraint tenants_single_identity_chk`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'tenant-a',
				'tenant-b',
				'tenant-b',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenants_id_unique"))
	})

	It("Re-creates foreign key constraints referencing tenants(name)", func(ctx context.Context) {
		// Insert a tenant before migration:
		_, err := conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'my-tenant',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 68)
		Expect(err).ToNot(HaveOccurred())

		// Bypass the check constraint to verify the foreign key independently:
		_, err = conn.Exec(ctx, `alter table tenants drop constraint tenants_single_identity_chk`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'new-tenant',
				'new-tenant',
				'non-existent',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant_fk"))
	})

	It("Rejects a tenant where identifier, name and tenant are not identical", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 68)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to insert a tenant where identifier differs from name:
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'different-id',
				'my-tenant',
				'my-tenant',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenants_single_identity_chk"))

		// Attempt to insert a tenant where tenant differs from name:
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				creator,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'different-tenant',
				'system',
				'{}'
			)
			`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenants_single_identity_chk"))
	})
})
