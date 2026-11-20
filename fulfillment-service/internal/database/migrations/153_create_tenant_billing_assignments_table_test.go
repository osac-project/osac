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
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create tenant billing assignments table", func() {
	createTenant := func(ctx context.Context, name string) {
		GinkgoHelper()
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, 'system', '{}')
			 on conflict do nothing`, name)
		Expect(err).ToNot(HaveOccurred())
	}
	createAccount := func(ctx context.Context, id string) {
		GinkgoHelper()
		_, err := conn.Exec(ctx, insertBillingAccount, id, billingAccountName(id), billingAccountData(id, nil))
		Expect(err).ToNot(HaveOccurred())
	}
	seed := func(ctx context.Context) {
		GinkgoHelper()
		createTenant(ctx, "tenant-a")
		createTenant(ctx, "tenant-b")
		createAccount(ctx, "ba-1")
		createAccount(ctx, "ba-2")
	}
	insert := func(ctx context.Context, id, tenant, account, end string) error {
		_, err := conn.Exec(ctx, insertBillingAssignment, id, tenant, billingAssignmentData(tenant, account, end))
		return err
	}

	It("projects interval columns from the payload and owns creation defaults", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		var name, tenantID, accountID, state, message string
		var hasEffectiveFrom bool
		var effectiveTo *string
		Expect(conn.QueryRow(ctx, `select name, tenant_id, billing_account_id, connection_state, message,
			effective_from is not null, effective_to from tenant_billing_assignments where id = 'asg-1'`).
			Scan(&name, &tenantID, &accountID, &state, &message, &hasEffectiveFrom, &effectiveTo)).To(Succeed())
		Expect(name).To(Equal("same-name"))
		Expect(tenantID).To(Equal("tenant-a"))
		Expect(accountID).To(Equal("ba-1"))
		Expect(state).To(Equal("PENDING"))
		Expect(message).To(Equal("pending"))
		Expect(hasEffectiveFrom).To(BeTrue())
		Expect(effectiveTo).To(BeNil())
	})

	It("rejects a second active assignment for the same tenant", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23505", "tenant_billing_assignments_unique_active_tenant",
			insertBillingAssignment, "asg-2", "tenant-a", billingAssignmentData("tenant-a", "ba-2", ""))
	})

	It("rejects a second active assignment for the same billing account", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23505", "tenant_billing_assignments_unique_active_account",
			insertBillingAssignment, "asg-2", "tenant-b", billingAssignmentData("tenant-b", "ba-1", ""))
	})

	It("allows a closed assignment to coexist with a new active one", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "2026-04-01T00:00:00Z")).To(Succeed())
		Expect(insert(ctx, "asg-2", "tenant-a", "ba-1", "")).To(Succeed())
	})

	It("rejects an empty name", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		rejectStatement(ctx, "23514", "", `insert into tenant_billing_assignments
			(id, name, tenant, data) values ('asg-1', '', 'tenant-a', $1::jsonb)`,
			billingAssignmentData("tenant-a", "ba-1", ""))
	})

	It("rejects a billing_account_id that does not reference an existing account", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		rejectStatement(ctx, "23503", "tenant_billing_assignments_billing_account_id_fkey",
			insertBillingAssignment, "asg-1", "tenant-a", billingAssignmentData("tenant-a", "no-such-account", ""))
	})

	It("keeps interval history for a tenant that no longer exists", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-gone", "ba-1", "")).To(Succeed())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_constraint where contype = 'f'
			and conrelid = 'tenant_billing_assignments'::regclass and confrelid = 'tenants'::regclass`).
			Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "retained history must not reference live tenants")
	})

	It("indexes the List default order and the current-interval uniqueness rules", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		var indexes int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid
			where i.indisvalid and c.relname in ('tenant_billing_assignments_unique_active_tenant',
			'tenant_billing_assignments_unique_active_account', 'tenant_billing_assignments_by_tenant',
			'tenant_billing_assignments_by_account', 'tenant_billing_assignments_by_effective_from',
			'tenant_billing_assignments_by_name')`).Scan(&indexes)).To(Succeed())
		Expect(indexes).To(Equal(6))
		var definition string
		Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes
			where indexname = 'tenant_billing_assignments_by_effective_from'`).Scan(&definition)).To(Succeed())
		Expect(definition).To(ContainSubstring("effective_from DESC"),
			"the List default order is effective_from desc")
	})

	It("enforces canonical owner/reference tuples, valid intervals and immutable closed history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "current", "tenant-a", "ba-1", "")).To(Succeed())
		Expect(insert(ctx, "closed", "tenant-a", "ba-2", "2026-04-01T00:00:00Z")).To(Succeed())

		rejectStatement(ctx, "23514", "", insertBillingAssignment, "inverted", "tenant-b",
			billingAssignmentData("tenant-b", "ba-1", "2026-02-01T00:00:00Z"))
		rejectStatement(ctx, "23514", "", insertBillingAssignment, "wrong-owner", "tenant-b",
			billingAssignmentData("tenant-a", "ba-1", ""))
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{spec,billing_account,name}', '"wrong"') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_from_timestamp}', '"2026-01-01T00:00:00Z"') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set message = 'projection only'
			where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,message}', '"rewritten history"') where id = 'closed'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set creator = 'invented'
			where id = 'closed'`)

		_, err := conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', '"2026-04-01T00:00:00Z"'), version = version + 1 where id = 'current'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(insert(ctx, "replacement", "tenant-a", "ba-1", "")).To(Succeed())
	})

	It("rejects deletion and noncanonical JSON field types without converting their values", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "current", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23514", "", `delete from tenant_billing_assignments where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,connection_state}', '1') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', 'null') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{spec,tenant}', '"tenant-a"') where id = 'current'`)
	})

	It("rejects noncanonical or inexact interval endpoints on insert and update", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "current", "tenant-a", "ba-1", "")).To(Succeed())
		const insert = `insert into tenant_billing_assignments (id, name, tenant, creator, annotations, data)
			values ('timestamp-check', 'same-name', 'tenant-b', 'request-principal',
				'{"osac.openshift.io/tenant":"tenant-b"}', jsonb_set($1::jsonb, $2::text[], to_jsonb($3::text)))`
		const update = `update tenant_billing_assignments set data = jsonb_set(data, $1::text[], to_jsonb($2::text))
			where id = 'current'`
		for _, endpoint := range []string{"effective_from_timestamp", "effective_to_timestamp"} {
			path := []string{"status", endpoint}
			// If rounding were allowed this timestamp would match the open interval's start on update.
			rejectStatement(ctx, "23514", "", update, path, "2026-03-01T00:00:00.000005001Z")
			for _, stamp := range invalidBillingTimestamps {
				By("rejecting " + endpoint + " = " + stamp)
				rejectStatement(ctx, "23514", "", insert,
					billingAssignmentData("tenant-b", "ba-1", "9999-12-31T23:59:59.999999Z"), path, stamp)
				rejectStatement(ctx, "23514", "", update, path, stamp)
			}
		}
		// A sub-microsecond inversion would round to a legal zero-length interval.
		rejectStatement(ctx, "23514", "", insert,
			billingAssignmentData("tenant-b", "ba-1", "2026-03-01T00:00:00.000005Z"),
			[]string{"status", "effective_from_timestamp"}, "2026-03-01T00:00:00.000005001Z")
		rejectStatement(ctx, "23514", "tenant_billing_assignments_valid_interval", update,
			[]string{"status", "effective_to_timestamp"}, "2026-02-01T00:00:00.000001Z")
	})

	It("projects exactly representable interval endpoints without rewriting JSON", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "exact", "tenant-a", "ba-1", "")).To(Succeed())
		_, err := conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(
			jsonb_set(data, '{status,effective_from_timestamp}', '"2026-03-01T00:00:00.000005000Z"'),
			'{status,effective_to_timestamp}', '"2028-02-29T00:00:00.123Z"') where id = 'exact'`)
		Expect(err).NotTo(HaveOccurred())
		var matches bool
		Expect(conn.QueryRow(ctx, `select effective_from = '2026-03-01T00:00:00.000005Z'::timestamptz
			and effective_to = '2028-02-29T00:00:00.123Z'::timestamptz
			and data#>>'{status,effective_from_timestamp}' = '2026-03-01T00:00:00.000005000Z'
			and data#>>'{status,effective_to_timestamp}' = '2028-02-29T00:00:00.123Z'
			from tenant_billing_assignments where id = 'exact'`).Scan(&matches)).To(Succeed())
		Expect(matches).To(BeTrue())
	})

	It("keys the active companion on deletion and publishes no changes", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "closed", "tenant-a", "ba-1", "2026-04-01T00:00:00Z")).To(Succeed())
		Expect(insert(ctx, "open", "tenant-b", "ba-2", "")).To(Succeed())
		// Closed intervals stay lifecycle-active: the companion tracks deletion, not interval currentness.
		var ids []string
		Expect(conn.QueryRow(ctx, `select array_agg(id order by id) from active_tenant_billing_assignments`).
			Scan(&ids)).To(Succeed())
		Expect(ids).To(Equal([]string{"closed", "open"}))

		var before, after int
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&before)).To(Succeed())
		Expect(insert(ctx, "unpublished", "tenant-a", "ba-2", "2026-05-01T00:00:00Z")).To(Succeed())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before))
		var triggers int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_trigger t join pg_proc p on p.oid = t.tgfoid
			where t.tgrelid = 'tenant_billing_assignments'::regclass and p.proname = 'enqueue_change'
			and not t.tgisinternal`).Scan(&triggers)).To(Succeed())
		Expect(triggers).To(BeZero())

		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})
})
