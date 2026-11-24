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
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
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

	It("stores assignment fields in data and uses standard object columns", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		var name, tenant, creator, accountID, state, message, effectiveFrom string
		var hasCreationTimestamp, isLifecycleActive bool
		var effectiveTo *string
		Expect(conn.QueryRow(ctx, `select name, tenant, creator, creation_timestamp is not null,
			deletion_timestamp = 'epoch', data#>>'{spec,billing_account,id}',
			data#>>'{status,connection_state}', data#>>'{status,message}',
			data#>>'{status,effective_from_timestamp}', data#>>'{status,effective_to_timestamp}'
			from tenant_billing_assignments where id = 'asg-1'`).
			Scan(&name, &tenant, &creator, &hasCreationTimestamp, &isLifecycleActive,
				&accountID, &state, &message, &effectiveFrom, &effectiveTo)).To(Succeed())
		Expect(name).To(Equal("same-name"))
		Expect(tenant).To(Equal("tenant-a"))
		Expect(creator).To(Equal("request-principal"))
		Expect(hasCreationTimestamp).To(BeTrue())
		Expect(isLifecycleActive).To(BeTrue())
		Expect(accountID).To(Equal("ba-1"))
		Expect(state).To(Equal("TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING"))
		Expect(message).To(Equal("pending"))
		Expect(effectiveFrom).To(Equal("2026-03-01T00:00:00.000005Z"))
		Expect(effectiveTo).To(BeNil())

		var standardColumns, projectedColumns int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_attribute
			where attrelid = 'tenant_billing_assignments'::regclass and attnum > 0 and not attisdropped
			and attname in ('id', 'name', 'creation_timestamp', 'creator', 'deletion_timestamp', 'finalizers',
				'tenant', 'project', 'labels', 'annotations', 'data', 'version')`).Scan(&standardColumns)).To(Succeed())
		Expect(standardColumns).To(Equal(12))
		Expect(conn.QueryRow(ctx, `select count(*) from pg_attribute
			where attrelid = 'tenant_billing_assignments'::regclass and attnum > 0 and not attisdropped
			and attname in ('tenant_id', 'billing_account_id', 'effective_from', 'effective_to',
				'connection_state', 'message')`).Scan(&projectedColumns)).To(Succeed())
		Expect(projectedColumns).To(BeZero())
	})

	It("rejects a second active assignment for the same tenant", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23505", "tenant_billing_assignments_unique_active_tenant",
			insertBillingAssignment, "asg-2", "tenant-a", billingAssignmentData("tenant-a", "ba-2", ""))
	})

	It("rejects a second active assignment for the same billing account", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23505", "tenant_billing_assignments_unique_active_account",
			insertBillingAssignment, "asg-2", "tenant-b", billingAssignmentData("tenant-b", "ba-1", ""))
	})

	It("keeps closed assignment history when a new active interval is inserted", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "2026-04-01T00:00:00Z")).To(Succeed())
		Expect(insert(ctx, "asg-2", "tenant-a", "ba-1", "")).To(Succeed())
		var historyRows int
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments where tenant = 'tenant-a'`).
			Scan(&historyRows)).To(Succeed())
		Expect(historyRows).To(Equal(2))
		rejectStatement(ctx, "23514", "", `delete from tenant_billing_assignments where id = 'asg-1'`)
	})

	It("rejects an empty name", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		rejectStatement(ctx, "23514", "", `insert into tenant_billing_assignments
			(id, name, tenant, data) values ('asg-1', '', 'tenant-a', $1::jsonb)`,
			billingAssignmentData("tenant-a", "ba-1", ""))
	})

	It("rejects a billing account reference that does not exist", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		_, err := conn.Exec(ctx, insertBillingAssignment, "asg-1", "tenant-a",
			billingAssignmentData("tenant-a", "no-such-account", ""))
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue(), "expected PostgreSQL rejection: %v", err)
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(Equal("billing account 'no-such-account' does not exist"))
	})

	It("rejects deleting an account referenced by closed assignment history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "asg-1", "tenant-a", "ba-1", "2026-04-01T00:00:00Z")).To(Succeed())
		rejectStatement(ctx, "Z0003", "", `delete from billing_accounts where id = 'ba-1'`)
	})

	It("keeps interval history for a tenant that no longer exists", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		createTenant(ctx, "tenant-gone")
		rejectStatement(ctx, "Z0002", "", insertBillingAssignment, "missing", "missing-tenant",
			billingAssignmentData("missing-tenant", "ba-1", ""))
		Expect(insert(ctx, "asg-1", "tenant-gone", "ba-1", "2026-04-01T00:00:00Z")).To(Succeed())
		rejectStatement(ctx, "Z0002", "", `update tenant_billing_assignments
			set tenant = 'missing-tenant', annotations = jsonb_build_object('osac.openshift.io/tenant', 'missing-tenant')
			where id = 'asg-1'`)
		_, err := conn.Exec(ctx, `update tenants set deletion_timestamp = '2026-03-02Z' where name = 'tenant-gone'`)
		Expect(err).NotTo(HaveOccurred())
		rejectStatement(ctx, "Z0002", "", insertBillingAssignment, "during-delete", "tenant-gone",
			billingAssignmentData("tenant-gone", "ba-1", ""))
		_, err = conn.Exec(ctx, `delete from projects where tenant = 'tenant-gone';
			delete from tenants where name = 'tenant-gone'`)
		Expect(err).NotTo(HaveOccurred())
		var retained int
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments
			where id = 'asg-1' and tenant = 'tenant-gone'`).Scan(&retained)).To(Succeed())
		Expect(retained).To(Equal(1), "physical deletion does not remove assignment history")
		rejectStatement(ctx, "Z0002", "", insertBillingAssignment, "after-delete", "tenant-gone",
			billingAssignmentData("tenant-gone", "ba-1", ""))
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_constraint where contype = 'f'
			and conrelid = 'tenant_billing_assignments'::regclass and confrelid = 'tenants'::regclass`).
			Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "retained history must not reference live tenants")
	})

	It("indexes JSON interval fields and enforces current-interval uniqueness", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		var indexes int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid
			where i.indisvalid and c.relname in ('tenant_billing_assignments_unique_active_tenant',
			'tenant_billing_assignments_unique_active_account', 'tenant_billing_assignments_by_tenant',
			'tenant_billing_assignments_by_account', 'tenant_billing_assignments_by_effective_from',
			'tenant_billing_assignments_by_name')`).Scan(&indexes)).To(Succeed())
		Expect(indexes).To(Equal(6))
		var tenantIndex, accountIndex, effectiveFromIndex string
		Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes
			where indexname = 'tenant_billing_assignments_unique_active_tenant'`).Scan(&tenantIndex)).To(Succeed())
		Expect(tenantIndex).To(ContainSubstring("tenant"))
		Expect(tenantIndex).To(ContainSubstring("effective_to_timestamp"))
		Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes
			where indexname = 'tenant_billing_assignments_unique_active_account'`).Scan(&accountIndex)).To(Succeed())
		Expect(accountIndex).To(ContainSubstring("billing_account"))
		Expect(accountIndex).To(ContainSubstring("effective_to_timestamp"))
		Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes
			where indexname = 'tenant_billing_assignments_by_effective_from'`).Scan(&effectiveFromIndex)).To(Succeed())
		Expect(effectiveFromIndex).To(ContainSubstring("effective_from_timestamp"),
			"the List default order uses the JSON effective_from_timestamp field")
		Expect(effectiveFromIndex).To(ContainSubstring("DESC"))
	})

	It("enforces canonical tenant ownership, valid intervals and immutable history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "current", "tenant-a", "ba-1", "")).To(Succeed())
		Expect(insert(ctx, "closed", "tenant-a", "ba-2", "2026-04-01T00:00:00Z")).To(Succeed())

		rejectStatement(ctx, "23514", "", insertBillingAssignment, "inverted", "tenant-b",
			billingAssignmentData("tenant-b", "ba-1", "2026-02-01T00:00:00Z"))
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set annotations =
			jsonb_build_object('osac.openshift.io/tenant', 'tenant-b') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{spec,billing_account,name}', '"wrong"') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_from_timestamp}', '"2026-01-01T00:00:00Z"') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,message}', '"rewritten history"') where id = 'closed'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set creator = 'invented'
			where id = 'closed'`)

		_, err := conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', '"2026-04-01T00:00:00Z"'), version = version + 1 where id = 'current'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(insert(ctx, "replacement", "tenant-a", "ba-1", "")).To(Succeed())
	})

	It("rejects deletion and invalid JSON interval or tenant values", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
		seed(ctx)
		Expect(insert(ctx, "current", "tenant-a", "ba-1", "")).To(Succeed())
		rejectStatement(ctx, "23514", "", `delete from tenant_billing_assignments where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', 'null') where id = 'current'`)
		rejectStatement(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{spec,tenant}', '"tenant-a"') where id = 'current'`)
	})

	It("keys the active companion on deletion and publishes no changes", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 158)).To(Succeed())
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
