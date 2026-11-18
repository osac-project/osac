/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Separate provider external ID", func() {
	It("releases discovery-only reservations but keeps tokens used by closed assignments", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, data)
				values ('billed', 'billed', 'billed', '2026-01-01Z', '{}');
			insert into billing_accounts (id, name, tenant, data)
				select id, 'ba-' || left(encode(sha256(convert_to(id, 'UTF8')), 'hex'), 60), 'system',
				 jsonb_build_object('status', jsonb_build_object('provider_key','m360','provider_account_reference',id,
				 'external_billing_account_id',id,'state_observation_timestamp','2026-01-01T00:00:00Z'))
				from unnest(array['assigned', 'discovered']) id;
			insert into tenant_billing_assignments (id, name, tenant, annotations, data)
				select 'closed', 'assignment', 'billed', '{"osac.openshift.io/tenant":"billed"}',
				 jsonb_build_object('spec', jsonb_build_object('tenant', jsonb_build_object('id','billed','name','billed'),
				  'billing_account', jsonb_build_object('id',id,'name',name)),
				  'status', jsonb_build_object('connection_state','TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				   'effective_from_timestamp','2026-01-01T00:00:00Z','effective_to_timestamp','2026-02-01T00:00:00Z'))
				from billing_accounts where id = 'assigned';`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 159)).To(Succeed())
		var token string
		var released bool
		Expect(conn.QueryRow(ctx, `select external_billing_account_id from billing_accounts where id = 'assigned'`).
			Scan(&token)).To(Succeed())
		Expect(token).To(Equal("assigned"))
		Expect(conn.QueryRow(ctx, `select external_billing_account_id is null
			and not ((data->'status') ? 'external_billing_account_id') from billing_accounts where id = 'discovered'`).
			Scan(&released)).To(Succeed())
		Expect(released).To(BeTrue())
		_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,provider_external_id}', '"shared"')`)
		Expect(err).NotTo(HaveOccurred(), "the same observation is allowed on distinct provider accounts")
		_, err = conn.Exec(ctx, `update billing_accounts set data = data #- '{status,external_billing_account_id}' where id = 'assigned'`)
		Expect(err).To(HaveOccurred(), "migration keeps committed assignment tokens immutable")
	})
})
