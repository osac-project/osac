/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add changes quarantine", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 126)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Adds nullable quarantine columns to the changes table", func(ctx context.Context) {
		rows, err := conn.Query(ctx, `
			select
				column_name,
				data_type,
				is_nullable
			from
				information_schema.columns
			where
				table_schema = 'public' and
				table_name = 'changes' and
				column_name in ('quarantined_at', 'quarantine_reason')
			order by
				column_name
		`)
		Expect(err).ToNot(HaveOccurred())
		type quarantineColumn struct {
			name     string
			dataType string
			nullable string
		}
		columns, err := pgx.CollectRows(
			rows,
			func(row pgx.CollectableRow) (result quarantineColumn, err error) {
				err = row.Scan(&result.name, &result.dataType, &result.nullable)
				return
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(columns).To(Equal([]quarantineColumn{
			{name: "quarantine_reason", dataType: "text", nullable: "YES"},
			{name: "quarantined_at", dataType: "timestamp with time zone", nullable: "YES"},
		}))
	})
})
