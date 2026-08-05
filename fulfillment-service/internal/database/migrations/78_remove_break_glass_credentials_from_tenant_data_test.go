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

var _ = DescribeMigration("Remove break glass credentials from tenant data", func() {
	DescribeTable(
		"Strips break_glass_credentials from tenant status",
		func(ctx context.Context, inputData string, expectedData string) {
			_, err := conn.Exec(
				ctx,
				`insert into tenants (id, name, tenant, creator, data) values ('test-tenant', 'test-tenant', 'test-tenant', 'system', $1::jsonb)`,
				inputData,
			)
			Expect(err).ToNot(HaveOccurred())

			err = tool.Migrate(ctx, 78)
			Expect(err).ToNot(HaveOccurred())

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
			"Tenant with break_glass_credentials in status",
			`{
				"status": {
					"state": "TENANT_STATE_SYNCED",
					"idp_tenant_name": "my-tenant",
					"break_glass_user_id": "user-123",
					"break_glass_credentials": {
						"username": "my-tenant-osac-break-glass",
						"password": "secret-password-123!"
					}
				}
			}`,
			`{
				"status": {
					"state": "TENANT_STATE_SYNCED",
					"idp_tenant_name": "my-tenant",
					"break_glass_user_id": "user-123"
				}
			}`,
		),
		Entry(
			"Tenant without break_glass_credentials - should not change",
			`{
				"status": {
					"state": "TENANT_STATE_SYNCED",
					"idp_tenant_name": "clean-tenant",
					"break_glass_user_id": "user-456"
				}
			}`,
			`{
				"status": {
					"state": "TENANT_STATE_SYNCED",
					"idp_tenant_name": "clean-tenant",
					"break_glass_user_id": "user-456"
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
	)

	It("Migrates archived_tenants table", func(ctx context.Context) {
		_, err := conn.Exec(
			ctx,
			`insert into archived_tenants (id, name, tenant, creator, data, creation_timestamp, deletion_timestamp)
			 values ('archived-tenant', 'archived-tenant', 'archived-tenant', 'system', $1::jsonb, now(), now())`,
			`{
				"status": {
					"state": "TENANT_STATE_SYNCED",
					"idp_tenant_name": "old-tenant",
					"break_glass_user_id": "admin-1",
					"break_glass_credentials": {
						"username": "old-tenant-osac-break-glass",
						"password": "old-secret-pass!"
					}
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 78)
		Expect(err).ToNot(HaveOccurred())

		var actualData []byte
		row := conn.QueryRow(
			ctx,
			`select data from archived_tenants where id = 'archived-tenant'`,
		)
		err = row.Scan(&actualData)
		Expect(err).ToNot(HaveOccurred())

		expectedData := `{
			"status": {
				"state": "TENANT_STATE_SYNCED",
				"idp_tenant_name": "old-tenant",
				"break_glass_user_id": "admin-1"
			}
		}`
		Expect(actualData).To(MatchJSON(expectedData))
	})
})
