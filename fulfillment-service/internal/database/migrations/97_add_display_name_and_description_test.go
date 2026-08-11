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

var _ = DescribeMigration("Add display_name and description columns", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Adds display_name and description text columns to every GenericDAO table", func(ctx context.Context) {
		rows, err := conn.Query(ctx, `
			select
				t.table_name
			from
				information_schema.tables t
			where
				t.table_schema = 'public' and
				t.table_type = 'BASE TABLE' and
				exists (
					select 1 from information_schema.columns c
					where c.table_schema = 'public'
					  and c.table_name = t.table_name
					  and c.column_name = 'name'
				) and
				exists (
					select 1 from information_schema.columns c
					where c.table_schema = 'public'
					  and c.table_name = t.table_name
					  and c.column_name = 'data'
				)
			order by
				t.table_name
		`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var table string
			Expect(rows.Scan(&table)).To(Succeed())
			tables = append(tables, table)
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
		Expect(tables).ToNot(BeEmpty())

		for _, table := range tables {
			var displayNameType, descriptionType string
			err := conn.QueryRow(ctx, `
				select
					format_type(a.atttypid, a.atttypmod)
				from
					pg_catalog.pg_attribute a
				join
					pg_catalog.pg_class c on c.oid = a.attrelid
				join
					pg_catalog.pg_namespace n on n.oid = c.relnamespace
				where
					n.nspname = 'public' and
					c.relname = $1 and
					a.attname = 'display_name' and
					a.attnum > 0 and
					not a.attisdropped
			`, table).Scan(&displayNameType)
			Expect(err).ToNot(HaveOccurred(), "table %s missing display_name", table)
			Expect(displayNameType).To(Equal("text"), "table %s display_name type", table)

			err = conn.QueryRow(ctx, `
				select
					format_type(a.atttypid, a.atttypmod)
				from
					pg_catalog.pg_attribute a
				join
					pg_catalog.pg_class c on c.oid = a.attrelid
				join
					pg_catalog.pg_namespace n on n.oid = c.relnamespace
				where
					n.nspname = 'public' and
					c.relname = $1 and
					a.attname = 'description' and
					a.attnum > 0 and
					not a.attisdropped
			`, table).Scan(&descriptionType)
			Expect(err).ToNot(HaveOccurred(), "table %s missing description", table)
			Expect(descriptionType).To(Equal("text"), "table %s description type", table)
		}
	})

	It("Keeps CheckSchema consistent after the migration", func(ctx context.Context) {
		err := tool.CheckSchema(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
})
