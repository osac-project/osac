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

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add changes", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 117)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the changes table", func(ctx context.Context) {
		var count int
		err := conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_tables
			where
				schemaname = 'public' and
				tablename = 'changes'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		rows, err := conn.Query(ctx, `
			select
				column_name
			from
				information_schema.columns
			where
				table_schema = 'public' and
				table_name = 'changes'
			order by
				column_name
		`)
		Expect(err).ToNot(HaveOccurred())
		columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())
		Expect(columns).To(Equal([]string{
			"data",
			"id",
			"op",
			"table",
			"timestamp",
		}))

		for _, column := range []string{"id", "data", "table", "op"} {
			var isNullable string
			err = conn.QueryRow(ctx, `
				select
					is_nullable
				from
					information_schema.columns
				where
					table_schema = 'public' and
					table_name = 'changes' and
					column_name = $1
			`, column).Scan(&isNullable)
			Expect(err).ToNot(HaveOccurred())
			Expect(isNullable).To(Equal("NO"), "column %q should be required", column)
		}

		var dataType, columnDefault string
		err = conn.QueryRow(ctx, `
			select
				data_type,
				column_default
			from
				information_schema.columns
			where
				table_schema = 'public' and
				table_name = 'changes' and
				column_name = 'id'
		`).Scan(&dataType, &columnDefault)
		Expect(err).ToNot(HaveOccurred())
		Expect(dataType).To(Equal("uuid"))
		Expect(columnDefault).To(Equal("uuidv7()"))

		var primaryKeyColumn string
		err = conn.QueryRow(ctx, `
			select
				a.attname
			from
				pg_constraint c
			join
				pg_class t on t.oid = c.conrelid
			join
				pg_namespace n on n.oid = t.relnamespace
			join
				pg_attribute a on a.attrelid = t.oid and a.attnum = any(c.conkey)
			where
				n.nspname = 'public' and
				t.relname = 'changes' and
				c.contype = 'p'
		`).Scan(&primaryKeyColumn)
		Expect(err).ToNot(HaveOccurred())
		Expect(primaryKeyColumn).To(Equal("id"))
	})

	It("Writes the complete object row on insert, update, and delete", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('acme', 'acme', 'acme', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values ('p-1', 'acme', '', 'p-1', 'system', '{"spec":{"title":"Acme"}}')
		`)
		Expect(err).ToNot(HaveOccurred())

		var changeID, tableName, op, objectID, tenant, title string
		err = conn.QueryRow(ctx, `
			select
				id::text,
				"table",
				op,
				data->>'id',
				data->>'tenant',
				data->'data'->'spec'->>'title'
			from
				changes
			where
				data->>'id' = 'p-1'
			order by
				id
		`).Scan(&changeID, &tableName, &op, &objectID, &tenant, &title)
		Expect(err).ToNot(HaveOccurred())
		Expect(changeID).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
		Expect(tableName).To(Equal("projects"))
		Expect(op).To(Equal("INSERT"))
		Expect(objectID).To(Equal("p-1"))
		Expect(tenant).To(Equal("acme"))
		Expect(title).To(Equal("Acme"))

		_, err = conn.Exec(ctx, `
			update projects
			set data = '{"spec":{"title":"Updated"}}'
			where id = 'p-1'
		`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx, `
			select
				op,
				data->'data'->'spec'->>'title'
			from
				changes
			where
				data->>'id' = 'p-1'
			order by
				id desc
			limit 1
		`).Scan(&op, &title)
		Expect(err).ToNot(HaveOccurred())
		Expect(op).To(Equal("UPDATE"))
		Expect(title).To(Equal("Updated"))

		_, err = conn.Exec(ctx, `
			delete from projects
			where id = 'p-1'
		`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx, `
			select
				op,
				data->'data'->'spec'->>'title'
			from
				changes
			where
				data->>'id' = 'p-1'
			order by
				id desc
			limit 1
		`).Scan(&op, &title)
		Expect(err).ToNot(HaveOccurred())
		Expect(op).To(Equal("DELETE"))
		Expect(title).To(Equal("Updated"))
	})

	It("Installs the enqueue_change trigger on every object table", func(ctx context.Context) {
		rows, err := conn.Query(ctx, `
			select
				c.relname
			from
				pg_catalog.pg_class c
			join
				pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where
				n.nspname = 'public' and
				c.relkind = 'r' and
				c.relname not like 'active_%' and
				c.relname not like 'archived_%' and
				c.relname not in (
					'changes',
					'notifications',
					'project_membership_subjects',
					'schema_migrations',
					'storage_tier_backends',
					'tenant_domains'
				)
			order by
				c.relname
		`)
		Expect(err).ToNot(HaveOccurred())
		tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())
		Expect(tables).ToNot(BeEmpty())

		for _, table := range tables {
			var count int
			err = conn.QueryRow(ctx, `
				select
					count(*)
				from
					pg_trigger t
				join
					pg_class c on c.oid = t.tgrelid
				join
					pg_namespace n on n.oid = c.relnamespace
				where
					n.nspname = 'public' and
					c.relname = $1 and
					t.tgname = 'enqueue_change' and
					not t.tgisinternal
			`, table).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(
				Equal(1),
				"expected enqueue_change trigger on table %q", table,
			)
		}

		var changeTriggers int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_trigger t
			join
				pg_class c on c.oid = t.tgrelid
			join
				pg_namespace n on n.oid = c.relnamespace
			where
				n.nspname = 'public' and
				c.relname = 'changes' and
				t.tgname = 'enqueue_change' and
				not t.tgisinternal
		`).Scan(&changeTriggers)
		Expect(err).ToNot(HaveOccurred())
		Expect(changeTriggers).To(Equal(0))
	})
})
