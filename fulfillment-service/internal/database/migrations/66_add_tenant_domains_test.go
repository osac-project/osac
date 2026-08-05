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
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add tenant domains", func() {
	It("Creates the 'tenant_domains' table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		var count int
		row := conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_catalog.pg_tables
			where
				schemaname = 'public' and
				tablename = 'tenant_domains'
		`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the 'materialize_tenant_domains' trigger", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		var count int
		row := conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_catalog.pg_trigger t
				join pg_catalog.pg_class c on t.tgrelid = c.oid
			where
				t.tgname = 'materialize_tenant_domains' and
				c.relname = 'tenants' and
				t.tgtype & 16 = 16
		`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically(">", 0))
	})

	It("Populates the helper table on insert", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"domains": []string{
					"example.com",
					"corp.example.org",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
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
				$1
			)
			`,
			data,
		)
		Expect(err).ToNot(HaveOccurred())
		var count int
		row := conn.QueryRow(ctx, `select count(*) from tenant_domains where tenant = 'my-tenant'`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically("==", 2))
	})

	It("Rejects duplicate domains across tenants", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"domains": []string{
					"example.com",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
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
				$1
			)
			`,
			data,
		)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
			insert into tenants (
				id,
				name,
				tenant,
				data
			)
			values (
				'your-tenant',
				'your-tenant',
				'your-tenant',
				$1
			)
			`,
			data,
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(Equal(
			"value 'example.com' in field 'spec.domains' is already used by another tenant",
		))
	})

	It("Allows the same tenant to update its own domains", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"domains": []string{
					"example.com",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
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
				$1
			)
			`,
			data,
		)
		Expect(err).ToNot(HaveOccurred())
		data, err = json.Marshal(map[string]any{
			"spec": map[string]any{
				"domains": []string{
					"new.example.com",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update tenants set data = $1 where id = 'my-tenant'`, data)
		Expect(err).ToNot(HaveOccurred())
		var domain string
		row := conn.QueryRow(ctx, `select domain from tenant_domains where tenant = 'my-tenant'`)
		err = row.Scan(&domain)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain).To(Equal("new.example.com"))
	})

	It("Allows tenants without domains", func(ctx context.Context) {
		err := tool.Migrate(ctx, 66)
		Expect(err).ToNot(HaveOccurred())
		data, err := json.Marshal(map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`
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
				$1
			)
			`,
			data,
		)
		Expect(err).ToNot(HaveOccurred())
		var count int
		row := conn.QueryRow(ctx, `select count(*) from tenant_domains where tenant = 'my-tenant'`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
	})
})
