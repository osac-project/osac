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

var _ = DescribeMigration("Rename organizations to tenants", func() {
	It("Renames the organizations table to tenants", func(ctx context.Context) {
		err := tool.Migrate(ctx, 58)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Removes the organizations table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 58)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*) from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where n.nspname = 'public' and c.relkind = 'r' and c.relname = 'organizations'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Renames the archived table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 58)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*) from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where n.nspname = 'public' and c.relkind = 'r' and c.relname = 'archived_tenants'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Preserves tenant foreign key constraints", func(ctx context.Context) {
		err := tool.Migrate(ctx, 58)
		Expect(err).ToNot(HaveOccurred())

		// Insert a valid tenant first:
		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		// Inserting a cluster with a non-existent tenant should fail:
		_, err = conn.Exec(ctx, `
			insert into clusters (id, name, tenant, data)
			values ('123', 'my-cluster', 'no-such-tenant', '{}')
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant_fk"))
	})

	It("Preserves the immutable column trigger", func(ctx context.Context) {
		err := tool.Migrate(ctx, 58)
		Expect(err).ToNot(HaveOccurred())

		// Insert a tenant:
		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		// Updating the name should be rejected:
		_, err = conn.Exec(ctx, `
			update tenants set name = 'new-name' where id = 'my-tenant'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})
})
