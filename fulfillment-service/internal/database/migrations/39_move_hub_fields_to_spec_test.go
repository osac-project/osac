/* Copyright (c) 2026 Red Hat Inc.

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

var _ = DescribeMigration("Add 'spec' and 'status' fields to hubs", func() {
	DescribeTable(
		"Data migration",
		func(ctx context.Context, original, expected string) {
			// Create a row with the original data:
			_, err := conn.Exec(
				ctx,
				`insert into hubs (id, data) values ('123', $1)`,
				[]byte(original),
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 39)
			Expect(err).ToNot(HaveOccurred())

			// Fetch the migraded data:
			var actual []byte
			row := conn.QueryRow(
				ctx,
				`select data from hubs where id = '123'`,
			)
			err = row.Scan(&actual)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(MatchJSON(expected))
		},
		Entry(
			"No fields to move",
			`{}`,
			`{
				"spec": {},
				"status": {}
			}`,
		),
		Entry(
			"Only 'kubeconfig' field",
			`{
				"kubeconfig": "bXlfa2M="
			}`,
			`{
				"spec": {
					"kubeconfig": "bXlfa2M="
				},
				"status": {}
			}`,
		),
		Entry(
			"Only 'namespace' field",
			`{
				"namespace": "my-ns"
			}`,
			`{
				"spec": {
					"namespace": "my-ns"
				},
				"status": {}
			}`,
		),
		Entry(
			"Both 'kubeconfig' and 'namespace' fields",
			`{
				"kubeconfig": "bXlfa2M=",
				"namespace": "my-ns"
			}`,
			`{
				"spec": {
					"kubeconfig": "bXlfa2M=",
					"namespace": "my-ns"
				},
				"status": {}
			}`,
		),
	)
})
