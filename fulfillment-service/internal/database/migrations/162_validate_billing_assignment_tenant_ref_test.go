/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Validate billing assignment tenant references", func() {
	It("requires a live tenant for writes and retains history after tenant deletion", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, data)
				values ('tenant-a', 'tenant-a', 'tenant-a', '2026-01-01Z', '{}');
			insert into billing_accounts (id, name, tenant, data)
				values ('account', 'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60), 'system',
				'{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-account",
				"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 162)).To(Succeed())

		rejectStatement(ctx, "Z0002", "", insertBillingAssignment,
			"missing", "missing-tenant", billingAssignmentData("missing-tenant", "account", ""))

		_, err = conn.Exec(ctx, insertBillingAssignment,
			"current", "tenant-a", billingAssignmentData("tenant-a", "account", ""))
		Expect(err).NotTo(HaveOccurred())
		rejectStatement(ctx, "Z0002", "", `update tenant_billing_assignments
			set tenant = 'missing-tenant', annotations = jsonb_build_object('osac.openshift.io/tenant', 'missing-tenant')
			where id = 'current'`)
		_, err = conn.Exec(ctx, `update tenants set deletion_timestamp = '2026-01-02Z' where name = 'tenant-a'`)
		Expect(err).NotTo(HaveOccurred())
		rejectStatement(ctx, "Z0002", "", insertBillingAssignment,
			"during-delete", "tenant-a", billingAssignmentData("tenant-a", "account", ""))

		_, err = conn.Exec(ctx, `delete from projects where tenant = 'tenant-a';
			delete from tenants where name = 'tenant-a'`)
		Expect(err).NotTo(HaveOccurred(), "closed history must not prevent physical tenant deletion")

		var tenantCount, assignmentCount int
		Expect(conn.QueryRow(ctx, `select count(*) from tenants where name = 'tenant-a'`).Scan(&tenantCount)).To(Succeed())
		Expect(tenantCount).To(BeZero())
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments
			where id = 'current' and data->'status'->>'effective_to_timestamp' is not null`).
			Scan(&assignmentCount)).To(Succeed())
		Expect(assignmentCount).To(Equal(1), "tenant deletion retains its closed billing history")

		rejectStatement(ctx, "Z0002", "", `insert into tenants (id, name, tenant, creation_timestamp, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', '2026-02-01Z', '{}')`)
	})
})
