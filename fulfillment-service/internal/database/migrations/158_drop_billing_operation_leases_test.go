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
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Drop billing operation leases", func() {
	BeforeEach(func(ctx context.Context) {
		var version int
		Expect(conn.QueryRow(ctx, `select version from schema_migrations`).Scan(&version)).To(Succeed())
		Expect(version).To(Equal(157))
		_, err := conn.Exec(ctx, `
			insert into billing_operation_leases (operation_key, owner, expires_at) values
				($1, 'expired-owner', '2025-01-01Z'), ($2, 'stale-owner', '2099-01-01Z');
		`, strings.Repeat("a", 64), strings.Repeat("b", 64))
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, data)
				values ('billed', 'billed', 'billed', '2026-01-01Z', '{}');
			insert into billing_accounts (id, name, tenant, data) values
				('account', 'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60), 'system',
				 '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native",
				   "external_billing_account_id":" raw-token ","state":"BILLING_ACCOUNT_STATE_ACTIVE",
				   "state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id, name, tenant, annotations, data)
				select h, 'assignment', 'billed', '{"osac.openshift.io/tenant":"billed"}',
				 jsonb_build_object('spec', jsonb_build_object('tenant', jsonb_build_object('id','billed','name','billed'),
				  'billing_account', jsonb_build_object('id','account','name',name)),
				  'status', jsonb_build_object('connection_state','TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING',
				   'effective_from_timestamp','2026-01-01T00:00:00Z','message','preserved')
				   || case when h = 'closed' then '{"effective_to_timestamp":"2026-02-01T00:00:00Z"}'::jsonb
				      else '{}'::jsonb end)
				from billing_accounts cross join unnest(array['closed', 'current']) h where id = 'account';
			insert into deleted_tenant_identities
				(tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
				values ('retained', '2025-01-01Z', '2025-02-01Z', 'original-actor', '2025-02-02Z', true);
		`)
		Expect(err).NotTo(HaveOccurred())
	})

	// Compare complete rows, schema definitions, every non-lease index and all foreign keys, not just counts.
	snapshot := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_build_array(
			(select jsonb_agg(t order by id) from tenants t),
			(select jsonb_agg(t order by id) from billing_accounts t),
			(select jsonb_agg(t order by id) from tenant_billing_assignments t),
			(select jsonb_agg(t order by tenant_name) from deleted_tenant_identities t),
			(select jsonb_agg(t order by table_name, ordinal_position) from information_schema.columns t
			 where table_schema = current_schema() and table_name <> 'billing_operation_leases'),
			(select jsonb_agg(t order by indexname) from pg_indexes t
			 where schemaname = current_schema() and tablename <> 'billing_operation_leases'),
			(select jsonb_agg(jsonb_build_array(conrelid::regclass::text, conname, pg_get_constraintdef(oid),
			 convalidated) order by conrelid::regclass::text, conname) from pg_constraint
			 where contype = 'f' and connamespace = current_schema()::regnamespace)
		)::text`).Scan(&result)).To(Succeed())
		return result
	}

	It("drops populated obsolete guards and only their indexes, preserving billing and identity evidence", func(ctx context.Context) {
		before := snapshot(ctx)
		var leaseIndexes int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_indexes where tablename = 'billing_operation_leases'`).
			Scan(&leaseIndexes)).To(Succeed())
		Expect(leaseIndexes).To(Equal(1))
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		var absent bool
		Expect(conn.QueryRow(ctx, `select to_regclass('billing_operation_leases') is null
			and to_regclass('billing_operation_leases_pkey') is null`).Scan(&absent)).To(Succeed())
		Expect(absent).To(BeTrue())
		Expect(snapshot(ctx)).To(Equal(before))
		var version int
		var dirty bool
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(158))
		Expect(dirty).To(BeFalse())
		_, err := conn.Exec(ctx, `delete from billing_accounts where id = 'account'`)
		Expect(err).To(HaveOccurred(), "the retained history account foreign key still restricts deletion")
		_, err = conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,message}', '"rewritten"') where id = 'closed'`)
		Expect(err).To(HaveOccurred(), "closed history remains immutable")
		Expect(snapshot(ctx)).To(Equal(before))
	})

	It("refuses an unexpected dependent object without cascading or changing unrelated data", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `create view lease_dependency as select owner from billing_operation_leases`)
		Expect(err).NotTo(HaveOccurred())
		before := snapshot(ctx)
		Expect(tool.Migrate(ctx, 158)).NotTo(Succeed())
		Expect(snapshot(ctx)).To(Equal(before))
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from lease_dependency`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2), "failed migration preserves both stale lease rows and the dependent object")
		// A failed migration does not damage ordinary transaction rollback on the same database.
		tx, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { _ = tx.Rollback(ctx) })
		_, err = tx.Exec(ctx, `update billing_accounts set data = jsonb_set(data,
			'{status,display_name}', '"uncommitted"') where id = 'account'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Rollback(ctx)).To(Succeed())
		Expect(snapshot(ctx)).To(Equal(before))
	})
})
