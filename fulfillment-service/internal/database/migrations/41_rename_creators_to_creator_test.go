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

var _ = DescribeMigration("Rename creators to creator", func() {
	DescribeTable(
		"Migrates the creators array to a single creator value",
		func(ctx context.Context, creators []string, expectedCreator string) {
			// Insert a row with the old creators array column:
			_, err := conn.Exec(
				ctx,
				`insert into clusters (id, creators, tenant, data) values ('123', $1, 'my-tenant', '{}')`,
				creators,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 41)
			Expect(err).ToNot(HaveOccurred())

			// Verify the creator column has the expected value:
			var actual string
			row := conn.QueryRow(
				ctx,
				`select creator from clusters where id = '123'`,
			)
			err = row.Scan(&actual)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(Equal(expectedCreator))
		},
		Entry(
			"Single creator",
			[]string{"my-user"},
			"my-user",
		),
		Entry(
			"Multiple creators takes first",
			[]string{"first", "second"},
			"first",
		),
		Entry(
			"Empty array",
			[]string{},
			"",
		),
	)
})
