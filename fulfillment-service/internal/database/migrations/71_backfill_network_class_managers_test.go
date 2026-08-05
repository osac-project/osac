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
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Backfill network class managers", func() {
	DescribeTable(
		"Backfills fabric_manager from implementation_strategy",
		func(ctx context.Context, inputData string, expectedData string) {
			// Insert a network class with the given data:
			_, err := conn.Exec(
				ctx,
				`INSERT INTO network_classes (id, tenant, data) VALUES ('nc-1', 'system', $1::jsonb)`,
				inputData,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 71)
			Expect(err).ToNot(HaveOccurred())

			// Verify the data was backfilled correctly:
			var actualData []byte
			row := conn.QueryRow(ctx, `SELECT data FROM network_classes WHERE id = 'nc-1'`)
			err = row.Scan(&actualData)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualData).To(MatchJSON(expectedData))
		},
		Entry(
			"NetworkClass without fabric_manager gets it from implementation_strategy",
			`{
				"title": "UDN Network",
				"implementation_strategy": "udn-net",
				"capabilities": {
					"supports_ipv4": true
				}
			}`,
			`{
				"title": "UDN Network",
				"implementation_strategy": "udn-net",
				"fabric_manager": "udn-net",
				"capabilities": {
					"supports_ipv4": true
				}
			}`,
		),
		Entry(
			"NetworkClass with existing fabric_manager is not overwritten",
			`{
				"title": "Physical Network",
				"implementation_strategy": "phys-net",
				"fabric_manager": "netris",
				"capabilities": {
					"supports_ipv4": true
				}
			}`,
			`{
				"title": "Physical Network",
				"implementation_strategy": "phys-net",
				"fabric_manager": "netris",
				"capabilities": {
					"supports_ipv4": true
				}
			}`,
		),
		Entry(
			"NetworkClass with empty implementation_strategy gets empty fabric_manager",
			`{
				"title": "Empty Strategy",
				"implementation_strategy": "",
				"capabilities": {}
			}`,
			`{
				"title": "Empty Strategy",
				"implementation_strategy": "",
				"fabric_manager": "",
				"capabilities": {}
			}`,
		),
		Entry(
			"NetworkClass with no implementation_strategy gets empty fabric_manager",
			`{
				"title": "No Strategy",
				"capabilities": {}
			}`,
			`{
				"title": "No Strategy",
				"fabric_manager": "",
				"capabilities": {}
			}`,
		),
	)

	It("Backfills archived_network_classes table", func(ctx context.Context) {
		// Insert an archived network class without fabric_manager:
		_, err := conn.Exec(
			ctx,
			`INSERT INTO archived_network_classes (id, data, creation_timestamp, deletion_timestamp)
			 VALUES ('archived-nc-1', $1::jsonb, now(), now())`,
			`{
				"title": "Archived UDN",
				"implementation_strategy": "udn-net",
				"capabilities": {}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 71)
		Expect(err).ToNot(HaveOccurred())

		// Verify the archived data was backfilled:
		var actualData []byte
		row := conn.QueryRow(ctx, `SELECT data FROM archived_network_classes WHERE id = 'archived-nc-1'`)
		err = row.Scan(&actualData)
		Expect(err).ToNot(HaveOccurred())
		Expect(actualData).To(MatchJSON(`{
			"title": "Archived UDN",
			"implementation_strategy": "udn-net",
			"fabric_manager": "udn-net",
			"capabilities": {}
		}`))
	})
})
