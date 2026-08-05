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

	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Rename tenants to tenant", func() {
	DescribeTable(
		"Migrates the tenants array to a single tenant value",
		func(ctx context.Context, tenants []string, expectedTenant string) {
			// Insert a row with the old tenants array column:
			_, err := conn.Exec(
				ctx,
				`insert into clusters (id, tenants, data) values ('123', $1, '{}')`,
				tenants,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 40)
			Expect(err).ToNot(HaveOccurred())

			// Verify the tenant column has the expected value:
			var actual string
			row := conn.QueryRow(
				ctx,
				`select tenant from clusters where id = '123'`,
			)
			err = row.Scan(&actual)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(Equal(expectedTenant))
		},
		Entry(
			"Single tenant",
			[]string{"my-tenant"},
			"my-tenant",
		),
		Entry(
			"Multiple tenants takes first",
			[]string{"first", "second", "third"},
			"first",
		),
		Entry(
			"Empty array",
			[]string{},
			"",
		),
	)
})
