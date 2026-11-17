/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Rename billing account external id", func() {
	// DescribeMigration clones the real version-154 predecessor for each spec.
	BeforeEach(func(ctx context.Context) {
		var version int
		var dirty bool
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(154))
		Expect(dirty).To(BeFalse())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data) values
				('tenant', 'tenant', 'tenant', '{}'), ('other-tenant', 'other-tenant', 'other-tenant', '{}');
			insert into billing_accounts
				(id, provider_key, provider_account_reference, display_name, base_currency,
				 correlation_id, state, status_observed_at) values
				('plain', 'm360', 'native-plain', 'Plain account', 'USD',
				 'ext-1', 'ACTIVE', '2026-01-01 01:02:03.000001Z'),
				('padded', 'm360', 'native-padded', 'Padded account', 'EUR',
				 ' ext-1 ', 'INACTIVE', '2026-01-02 02:03:04.000002Z'),
				('absent', 'second-provider', 'native-absent', 'Absent account', 'GBP',
				 null, 'UNKNOWN', '2026-01-03 03:04:05.000003Z'),
				('empty', 'second-provider', 'native-empty', 'Empty account', 'JPY',
				 '', 'ACTIVE', '2026-01-04 04:05:06.000004Z'),
				('whitespace', 'third-provider', 'native-whitespace', 'Whitespace account', 'CAD',
				 E' \t ', 'INACTIVE', '2026-01-05 05:06:07.000005Z');
			insert into tenant_billing_assignments
				(id, name, tenant_id, billing_account_id, effective_from, effective_to,
				 connection_state, message, creation_timestamp, creator) values
				('closed', 'same-name', 'tenant', 'plain', '2026-01-01 00:00:00.000001Z',
				 '2026-02-01 00:00:00.000002Z', 'PENDING', 'retained history',
				 '2025-12-31 00:00:00.000003Z', 'original-creator'),
				('current', 'same-name', 'tenant', 'plain', '2026-02-01 00:00:00.000002Z',
				 null, 'CONNECTED', 'current link', '2026-02-01 00:00:00.000004Z', 'current-creator');
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	assignments := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(to_jsonb(a) order by id)::text
			from tenant_billing_assignments a`).Scan(&result)).To(Succeed())
		return result
	}

	It("preserves all raw tokens, other account values and current plus closed links on forward upgrade", func(ctx context.Context) {
		var beforeAccounts, beforeTokens string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(to_jsonb(b) - 'correlation_id' order by id)::text,
			jsonb_object_agg(id, correlation_id)::text from billing_accounts b`).
			Scan(&beforeAccounts, &beforeTokens)).To(Succeed())
		Expect(beforeTokens).To(MatchJSON(`{
			"plain":"ext-1","padded":" ext-1 ","absent":null,"empty":"","whitespace":" \t "
		}`))
		beforeAssignments := assignments(ctx)
		Expect(tool.Migrate(ctx, 155)).To(Succeed())

		var afterAccounts, afterTokens string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(to_jsonb(b) - 'external_billing_account_id' order by id)::text,
			jsonb_object_agg(id, external_billing_account_id)::text from billing_accounts b`).
			Scan(&afterAccounts, &afterTokens)).To(Succeed())
		Expect(afterAccounts).To(Equal(beforeAccounts))
		Expect(afterTokens).To(Equal(beforeTokens))
		Expect(assignments(ctx)).To(Equal(beforeAssignments))
		var current, closed int
		Expect(conn.QueryRow(ctx, `select count(*) filter (where a.effective_to is null),
			count(*) filter (where a.effective_to is not null)
			from tenant_billing_assignments a join billing_accounts b on b.id = a.billing_account_id
			join tenants t on t.name = a.tenant_id`).Scan(&current, &closed)).To(Succeed())
		Expect(current).To(Equal(1))
		Expect(closed).To(Equal(1))

		var columns []string
		Expect(conn.QueryRow(ctx, `select array_agg(column_name::text order by column_name)
			from information_schema.columns where table_schema = 'public' and table_name = 'billing_accounts'
			and column_name in ('correlation_id', 'external_billing_account_id')`).Scan(&columns)).To(Succeed())
		Expect(columns).To(Equal([]string{"external_billing_account_id"}))
		var dataType, nullable string
		Expect(conn.QueryRow(ctx, `select data_type, is_nullable from information_schema.columns
			where table_schema = 'public' and table_name = 'billing_accounts'
			and column_name = 'external_billing_account_id'`).Scan(&dataType, &nullable)).To(Succeed())
		Expect(dataType).To(Equal("text"))
		Expect(nullable).To(Equal("YES"))

		// Read live structured catalog facts, including the renamed index's target column.
		var indexes string
		Expect(conn.QueryRow(ctx, `select jsonb_object_agg(c.relname, jsonb_build_object(
			'unique', i.indisunique, 'valid', i.indisvalid, 'predicate', pg_get_expr(i.indpred, i.indrelid),
			'columns', (select jsonb_agg(a.attname order by k.ordinality)
				from unnest(i.indkey) with ordinality k(attnum, ordinality)
				join pg_attribute a on a.attrelid = i.indrelid and a.attnum = k.attnum)))::text
			from pg_index i join pg_class c on c.oid = i.indexrelid
			where i.indrelid in ('billing_accounts'::regclass, 'tenant_billing_assignments'::regclass)
			and c.relname in ('billing_accounts_unique_provider_account', 'billing_accounts_unique_correlation_id',
				'tenant_billing_assignments_unique_active_tenant', 'tenant_billing_assignments_unique_active_account',
				'tenant_billing_assignments_by_tenant', 'tenant_billing_assignments_by_account',
				'tenant_billing_assignments_by_effective_from')`).Scan(&indexes)).To(Succeed())
		Expect(indexes).To(MatchJSON(`{
			"billing_accounts_unique_provider_account": {
				"unique":true,"valid":true,"predicate":null,"columns":["provider_key","provider_account_reference"]},
			"billing_accounts_unique_correlation_id": {
				"unique":true,"valid":true,"predicate":"(external_billing_account_id IS NOT NULL)",
				"columns":["external_billing_account_id"]},
			"tenant_billing_assignments_unique_active_tenant": {
				"unique":true,"valid":true,"predicate":"(effective_to IS NULL)","columns":["tenant_id"]},
			"tenant_billing_assignments_unique_active_account": {
				"unique":true,"valid":true,"predicate":"(effective_to IS NULL)","columns":["billing_account_id"]},
			"tenant_billing_assignments_by_tenant": {
				"unique":false,"valid":true,"predicate":null,"columns":["tenant_id"]},
			"tenant_billing_assignments_by_account": {
				"unique":false,"valid":true,"predicate":null,"columns":["billing_account_id"]},
			"tenant_billing_assignments_by_effective_from": {
				"unique":false,"valid":true,"predicate":null,"columns":["effective_from"]}
		}`))
	})

	DescribeTable("enforces each current-interval index after forward upgrade and recovers for commit",
		func(ctx context.Context, tenantID, accountID, constraint string) {
			before := assignments(ctx)
			var beforeTenants int
			Expect(conn.QueryRow(ctx, `select count(*) from tenants`).Scan(&beforeTenants)).To(Succeed())
			Expect(tool.Migrate(ctx, 155)).To(Succeed())
			tx, err := conn.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) { _ = tx.Rollback(ctx) })
			for _, operation := range []string{"insert", "update"} {
				sp, err := tx.Begin(ctx)
				Expect(err).ToNot(HaveOccurred())
				id := operation + "-candidate"
				if operation == "insert" {
					_, err = sp.Exec(ctx, `insert into tenant_billing_assignments
						(id, name, tenant_id, billing_account_id, effective_from, effective_to)
						values ($1, $1, $2, $3, '2026-03-01 00:00:00.000005Z', null)`, id, tenantID, accountID)
				} else {
					// Start with a valid independent current interval, then change exactly one occupied key.
					_, err = sp.Exec(ctx, `insert into tenant_billing_assignments
						(id, name, tenant_id, billing_account_id, effective_from, effective_to)
						values ($1, $1, 'other-tenant', 'padded', '2026-03-01 00:00:00.000006Z', null)`, id)
					Expect(err).ToNot(HaveOccurred())
					_, err = sp.Exec(ctx, `update tenant_billing_assignments
						set tenant_id = $2, billing_account_id = $3 where id = $1`, id, tenantID, accountID)
				}
				var pgErr *pgconn.PgError
				Expect(errors.As(err, &pgErr)).To(BeTrue(), "%s must hit %s", operation, constraint)
				Expect(pgErr.Code).To(Equal("23505"))
				Expect(pgErr.ConstraintName).To(Equal(constraint))
				Expect(sp.Rollback(ctx)).To(Succeed())
				var tenants int
				Expect(tx.QueryRow(ctx, `select count(*) from tenants`).Scan(&tenants)).To(Succeed())
				Expect(tenants).To(Equal(beforeTenants), "unrelated SQL must work after each recovered conflict")
			}
			_, err = tx.Exec(ctx, `insert into tenants (id, name, tenant, data)
				values ('commit-probe', 'commit-probe', 'commit-probe', '{}')`)
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.Commit(ctx)).To(Succeed())
			var committed bool
			Expect(conn.QueryRow(ctx, `select exists(select 1 from tenants where id = 'commit-probe')`).
				Scan(&committed)).To(Succeed())
			Expect(committed).To(BeTrue())
			Expect(assignments(ctx)).To(Equal(before), "both current and same-name closed history must remain exact")
		},
		Entry("same tenant with a different valid account", "tenant", "padded",
			"tenant_billing_assignments_unique_active_tenant"),
		Entry("different valid tenant with the same account", "other-tenant", "plain",
			"tenant_billing_assignments_unique_active_account"),
	)

	It("retains named raw uniqueness, multiple NULLs and a usable outer transaction after savepoint conflicts", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 155)).To(Succeed())
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { _ = tx.Rollback(ctx) })
		for _, raw := range []string{"ext-1", " ext-1 ", "", " \t "} {
			sp, err := tx.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = sp.Exec(ctx, `insert into billing_accounts
				(id, provider_key, provider_account_reference, external_billing_account_id)
				values ('duplicate', 'm360', 'native-duplicate', $1)`, raw)
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue(), "raw duplicate %q", raw)
			Expect(pgErr.Code).To(Equal("23505"))
			Expect(pgErr.ConstraintName).To(Equal("billing_accounts_unique_correlation_id"))
			Expect(sp.Rollback(ctx)).To(Succeed())
		}
		// These raw strings differ from every existing token; no trim/canonicalization is permitted.
		_, err = tx.Exec(ctx, `insert into billing_accounts
			(id, provider_key, provider_account_reference, external_billing_account_id) values
			('null-2', 'm360', 'native-null-2', null), ('null-3', 'm360', 'native-null-3', null),
			('spaces', 'm360', 'native-spaces', '  '), ('trailing', 'm360', 'native-trailing', 'ext-1 ')`)
		Expect(err).ToNot(HaveOccurred())
		var nulls int
		Expect(tx.QueryRow(ctx, `select count(*) from billing_accounts
			where external_billing_account_id is null`).Scan(&nulls)).To(Succeed())
		Expect(nulls).To(Equal(3))
		Expect(tx.Rollback(ctx)).To(Succeed())
		var accounts int
		Expect(conn.QueryRow(ctx, `select count(*) from billing_accounts`).Scan(&accounts)).To(Succeed())
		Expect(accounts).To(Equal(5), "ordinary transaction rollback remains supported; there is no inverse migration")
	})

	It("keeps the account foreign key protecting both current and closed history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 155)).To(Succeed())
		before := assignments(ctx)
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { _ = tx.Rollback(ctx) })
		_, err = tx.Exec(ctx, `delete from billing_accounts where id = 'plain'`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23503"))
		Expect(pgErr.TableName).To(Equal("tenant_billing_assignments"))
		Expect(tx.Rollback(ctx)).To(Succeed())
		Expect(assignments(ctx)).To(Equal(before))
	})
})
