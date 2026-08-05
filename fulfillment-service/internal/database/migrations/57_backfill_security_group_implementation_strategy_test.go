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

var _ = DescribeMigration("Backfill security group implementation strategy", func() {
	DescribeTable(
		"Backfills implementation_strategy on security_groups",
		func(ctx context.Context, inputData string, expectedData string) {
			// Create the tenant organization (required by foreign key constraint):
			_, err := conn.Exec(
				ctx,
				`insert into organizations (id, name, tenant, creator, data) values ('test-tenant', 'test-tenant', 'system', 'system', '{}') on conflict do nothing`,
			)
			Expect(err).ToNot(HaveOccurred())

			// Create virtual networks referenced by the security group data:
			for _, vn := range []string{"vnet-123", "vnet-456", "vnet-789"} {
				_, err = conn.Exec(
					ctx,
					`insert into virtual_networks (id, tenant, data) values ($1, 'test-tenant', '{}') on conflict do nothing`,
					vn,
				)
				Expect(err).ToNot(HaveOccurred())
			}

			// Insert a security group with the given data:
			_, err = conn.Exec(
				ctx,
				`insert into security_groups (id, tenant, data) values ('test-sg-123', 'test-tenant', $1::jsonb)`,
				inputData,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 57)
			Expect(err).ToNot(HaveOccurred())

			// Fetch the migrated data:
			var actualData []byte
			row := conn.QueryRow(
				ctx,
				`select data from security_groups where id = 'test-sg-123'`,
			)
			err = row.Scan(&actualData)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualData).To(MatchJSON(expectedData))
		},
		Entry(
			"Security group without implementation_strategy",
			`{
				"spec": {
					"virtual_network": "vnet-123"
				}
			}`,
			`{
				"spec": {
					"virtual_network": "vnet-123",
					"implementation_strategy": "network_policy"
				}
			}`,
		),
		Entry(
			"Security group with empty implementation_strategy",
			`{
				"spec": {
					"virtual_network": "vnet-456",
					"implementation_strategy": ""
				}
			}`,
			`{
				"spec": {
					"virtual_network": "vnet-456",
					"implementation_strategy": "network_policy"
				}
			}`,
		),
		Entry(
			"Security group already has implementation_strategy - should not change",
			`{
				"spec": {
					"virtual_network": "vnet-789",
					"implementation_strategy": "network_policy"
				}
			}`,
			`{
				"spec": {
					"virtual_network": "vnet-789",
					"implementation_strategy": "network_policy"
				}
			}`,
		),
	)

	It("Backfills archived_security_groups table", func(ctx context.Context) {
		// Create the tenant organization (required by foreign key constraint):
		_, err := conn.Exec(
			ctx,
			`insert into organizations (id, name, tenant, creator, data) values ('test-tenant', 'test-tenant', 'system', 'system', '{}') on conflict do nothing`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert an archived security group without implementation_strategy:
		_, err = conn.Exec(
			ctx,
			`insert into archived_security_groups (id, tenant, data, creation_timestamp, deletion_timestamp) values ('archived-sg-123', 'test-tenant', $1::jsonb, now(), now())`,
			`{
				"spec": {
					"virtual_network": "archived-vnet"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 57)
		Expect(err).ToNot(HaveOccurred())

		// Verify the archived instance was backfilled:
		var actualData []byte
		row := conn.QueryRow(
			ctx,
			`select data from archived_security_groups where id = 'archived-sg-123'`,
		)
		err = row.Scan(&actualData)
		Expect(err).ToNot(HaveOccurred())

		expectedData := `{
			"spec": {
				"virtual_network": "archived-vnet",
				"implementation_strategy": "network_policy"
			}
		}`
		Expect(actualData).To(MatchJSON(expectedData))
	})
})
