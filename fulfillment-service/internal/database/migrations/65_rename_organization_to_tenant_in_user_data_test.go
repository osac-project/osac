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

var _ = DescribeMigration("Rename organization to tenant in user data", func() {
	DescribeTable(
		"Renames spec.organization to spec.tenant",
		func(ctx context.Context, inputData string, expectedData string) {
			// Insert the tenant to satisfy the FK constraint:
			_, err := conn.Exec(
				ctx,
				`insert into tenants (id, name, tenant, creator, data) values ('test-tenant', 'Test Tenant', 'system', 'system', '{}') on conflict do nothing`,
			)
			Expect(err).ToNot(HaveOccurred())

			// Insert a user with old-style data:
			_, err = conn.Exec(
				ctx,
				`insert into users (id, name, tenant, creator, data) values ('test-user', 'test-user', 'test-tenant', 'system', $1::jsonb)`,
				inputData,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 65)
			Expect(err).ToNot(HaveOccurred())

			// Fetch the migrated data:
			var actualData []byte
			row := conn.QueryRow(
				ctx,
				`select data from users where id = 'test-user'`,
			)
			err = row.Scan(&actualData)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualData).To(MatchJSON(expectedData))
		},
		Entry(
			"User with spec.organization set",
			`{
				"spec": {
					"username": "testuser",
					"email": "test@example.com",
					"organization": "tenant-a"
				}
			}`,
			`{
				"spec": {
					"username": "testuser",
					"email": "test@example.com",
					"tenant": "tenant-a"
				}
			}`,
		),
		Entry(
			"User already using spec.tenant - should not change",
			`{
				"spec": {
					"username": "testuser",
					"tenant": "tenant-b"
				}
			}`,
			`{
				"spec": {
					"username": "testuser",
					"tenant": "tenant-b"
				}
			}`,
		),
		Entry(
			"User with no spec - should not change",
			`{}`,
			`{}`,
		),
		Entry(
			"User with empty spec - should not change",
			`{
				"spec": {}
			}`,
			`{
				"spec": {}
			}`,
		),
		Entry(
			"User with both spec.organization and spec.tenant - preserves tenant",
			`{
				"spec": {
					"username": "testuser",
					"organization": "old-org",
					"tenant": "correct-tenant"
				}
			}`,
			`{
				"spec": {
					"username": "testuser",
					"tenant": "correct-tenant"
				}
			}`,
		),
	)

	It("Migrates archived_users table", func(ctx context.Context) {
		// Insert the tenant to satisfy the FK constraint:
		_, err := conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ('test-tenant', 'Test Tenant', 'system', 'system', '{}') on conflict do nothing`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(
			ctx,
			`insert into archived_users (id, name, tenant, creator, data, creation_timestamp, deletion_timestamp)
			 values ('archived-user', 'archived-user', 'test-tenant', 'system', $1::jsonb, now(), now())`,
			`{
				"spec": {
					"username": "old-user",
					"email": "old@example.com",
					"organization": "tenant-archived"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 65)
		Expect(err).ToNot(HaveOccurred())

		// Verify the archived user was migrated:
		var actualData []byte
		row := conn.QueryRow(
			ctx,
			`select data from archived_users where id = 'archived-user'`,
		)
		err = row.Scan(&actualData)
		Expect(err).ToNot(HaveOccurred())

		expectedData := `{
			"spec": {
				"username": "old-user",
				"email": "old@example.com",
				"tenant": "tenant-archived"
			}
		}`
		Expect(actualData).To(MatchJSON(expectedData))
	})
})
