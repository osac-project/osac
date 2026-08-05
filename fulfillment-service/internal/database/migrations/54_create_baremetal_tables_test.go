/*
Copyright (c) 2025 Red Hat Inc.

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
	"fmt"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create baremetal tables", func() {
	DescribeTable(
		"Creates the expected tables",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 54)
			Expect(err).ToNot(HaveOccurred())

			quotedTable := pgx.Identifier{table}.Sanitize()

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"test-id", "system", `{}`,
			)
			Expect(err).ToNot(HaveOccurred())

			var count int
			err = conn.QueryRow(ctx,
				fmt.Sprintf(`select count(*) from %s where id = $1`, quotedTable),
				"test-id",
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1))

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"bad-tenant-id", "no-such-tenant", `{}`,
			)
			Expect(err).To(HaveOccurred())
		},
		Entry("bare_metal_instance_templates", "bare_metal_instance_templates"),
		Entry("bare_metal_instance_catalog_items", "bare_metal_instance_catalog_items"),
		Entry("bare_metal_instances", "bare_metal_instances"),
	)
})
