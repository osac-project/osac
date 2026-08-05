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

var _ = DescribeMigration("Rename organization fields in tenant data", func() {
	DescribeTable(
		"Renames idp_organization_name to idp_tenant_name",
		func(ctx context.Context, inputData string, expectedData string) {
			// Insert a tenant with old-style data:
			_, err := conn.Exec(
				ctx,
				`insert into tenants (id, name, tenant, creator, data) values ('test-tenant', 'test-tenant', 'test-tenant', 'system', $1::jsonb)`,
				inputData,
			)
			Expect(err).ToNot(HaveOccurred())

			// Run the migration:
			err = tool.Migrate(ctx, 63)
			Expect(err).ToNot(HaveOccurred())

			// Fetch the migrated data:
			var actualData []byte
			row := conn.QueryRow(
				ctx,
				`select data from tenants where id = 'test-tenant'`,
			)
			err = row.Scan(&actualData)
			Expect(err).ToNot(HaveOccurred())
			Expect(actualData).To(MatchJSON(expectedData))
		},
		Entry(
			"Tenant with idp_organization_name and ORGANIZATION_STATE_PENDING",
			`{
				"status": {
					"idp_organization_name": "my-org-in-idp",
					"state": "ORGANIZATION_STATE_PENDING",
					"message": "waiting for sync"
				}
			}`,
			`{
				"status": {
					"idp_tenant_name": "my-org-in-idp",
					"state": "TENANT_STATE_PENDING",
					"message": "waiting for sync"
				}
			}`,
		),
		Entry(
			"Tenant with idp_organization_name and ORGANIZATION_STATE_SYNCED",
			`{
				"status": {
					"idp_organization_name": "synced-org",
					"state": "ORGANIZATION_STATE_SYNCED",
					"break_glass_user_id": "user-123"
				}
			}`,
			`{
				"status": {
					"idp_tenant_name": "synced-org",
					"state": "TENANT_STATE_SYNCED",
					"break_glass_user_id": "user-123"
				}
			}`,
		),
		Entry(
			"Tenant with ORGANIZATION_STATE_FAILED but no idp_organization_name",
			`{
				"status": {
					"state": "ORGANIZATION_STATE_FAILED",
					"message": "sync error"
				}
			}`,
			`{
				"status": {
					"state": "TENANT_STATE_FAILED",
					"message": "sync error"
				}
			}`,
		),
		Entry(
			"Tenant already using new field names - should not change",
			`{
				"status": {
					"idp_tenant_name": "already-migrated",
					"state": "TENANT_STATE_SYNCED"
				}
			}`,
			`{
				"status": {
					"idp_tenant_name": "already-migrated",
					"state": "TENANT_STATE_SYNCED"
				}
			}`,
		),
		Entry(
			"Tenant with no status - should not change",
			`{
				"spec": {}
			}`,
			`{
				"spec": {}
			}`,
		),
		Entry(
			"Tenant with empty status - should not change",
			`{
				"status": {}
			}`,
			`{
				"status": {}
			}`,
		),
	)

	It("Migrates archived_tenants table", func(ctx context.Context) {
		// Insert the parent tenant (required by foreign key on the main table for other tests, and
		// archived_tenants has no FK so we just insert directly):
		_, err := conn.Exec(
			ctx,
			`insert into archived_tenants (id, name, tenant, creator, data, creation_timestamp, deletion_timestamp)
			 values ('archived-tenant', 'archived-tenant', 'archived-tenant', 'system', $1::jsonb, now(), now())`,
			`{
				"status": {
					"idp_organization_name": "old-org-name",
					"state": "ORGANIZATION_STATE_SYNCED",
					"break_glass_user_id": "admin-1"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Run the migration:
		err = tool.Migrate(ctx, 63)
		Expect(err).ToNot(HaveOccurred())

		// Verify the archived tenant was migrated:
		var actualData []byte
		row := conn.QueryRow(
			ctx,
			`select data from archived_tenants where id = 'archived-tenant'`,
		)
		err = row.Scan(&actualData)
		Expect(err).ToNot(HaveOccurred())

		expectedData := `{
			"status": {
				"idp_tenant_name": "old-org-name",
				"state": "TENANT_STATE_SYNCED",
				"break_glass_user_id": "admin-1"
			}
		}`
		Expect(actualData).To(MatchJSON(expectedData))
	})
})
