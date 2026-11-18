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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var invalidBillingTimestamps = []string{
	"2026-02-01 00:00:00.000002Z",
	"2026-02-01t00:00:00.000002z",
	"2026-02-01T00:00:00.000002+00:00",
	"2026-02-01T00:00:00.000002",
	"2026-02-01T00:00:00.000002Z\n",
	"2026-02-01T00:00:00.0000020Z",
	"2026-01-31T24:00:00Z",
	"2026-01-31T23:59:60Z",
	"2026-02-29T00:00:00Z",
	"0000-01-01T00:00:00Z",
	"10000-01-01T00:00:00Z",
	"infinity",
}

var _ = DescribeMigration("Normalize billing objects", func() {
	accountName := func(id string) string {
		sum := sha256.Sum256([]byte(id))
		return "ba-" + hex.EncodeToString(sum[:])[:60]
	}
	BeforeEach(func(ctx context.Context) {
		var version int
		Expect(conn.QueryRow(ctx, `select version from schema_migrations`).Scan(&version)).To(Succeed())
		Expect(version).To(Equal(155))
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data) values
				('tenant', 'tenant', 'tenant', '{}'), ('other-tenant', 'other-tenant', 'other-tenant', '{}');
			insert into billing_accounts
				(id, provider_key, provider_account_reference, display_name, base_currency,
				 external_billing_account_id, state, status_observed_at) values
				('plain', 'm360', 'native-plain', 'Label ü', 'USD', 'ext-1', 'ACTIVE', '2026-01-01 01:02:03.000001Z'),
				('padded', 'm360', 'native-padded', 'Padded', 'EUR', ' ext-1 ', 'INACTIVE', '2026-01-02 02:03:04.000002Z'),
				('absent', 'second', 'native-absent', '', '', null, 'UNKNOWN', '2026-01-03 03:04:05.000003Z'),
				('empty', 'second', 'native-empty', 'Empty', 'JPY', '', 'ACTIVE', '2026-01-04 04:05:06.000004Z'),
				('whitespace', 'third', 'native-whitespace', 'Whitespace', 'CAD', E' \t ', 'INACTIVE', '2026-01-05 05:06:07.000005Z');
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

	legacyAccounts := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(jsonb_build_array(id, provider_key,
			provider_account_reference, display_name, base_currency, external_billing_account_id,
			state, status_observed_at) order by id)::text from billing_accounts`).Scan(&result)).To(Succeed())
		return result
	}
	legacyAssignments := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(jsonb_build_array(id, name, tenant_id, billing_account_id,
			effective_from, effective_to, connection_state, message, creation_timestamp, creator) order by id)::text
			from tenant_billing_assignments`).Scan(&result)).To(Succeed())
		return result
	}
	// This deliberately writes the standard DAO column set, not the legacy business columns.
	// Full protobuf/GenericDAO round trips are owned by the integrating DAO suite.
	accountData := func(id string, token *string) string {
		status := map[string]any{
			"provider_key": "m360", "provider_account_reference": "native-" + id,
			"display_name": "New label", "base_currency": "USD", "state": "BILLING_ACCOUNT_STATE_ACTIVE",
			"state_observation_timestamp": "2026-03-01T01:02:03.000004Z",
		}
		if token != nil {
			status["external_billing_account_id"] = *token
		}
		data, err := json.Marshal(map[string]any{"spec": map[string]any{}, "status": status})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	assignmentData := func(tenant, account, end string) string {
		status := map[string]any{"connection_state": "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING",
			"effective_from_timestamp": "2026-03-01T00:00:00.000005Z", "message": "pending"}
		if end != "" {
			status["effective_to_timestamp"] = end
		}
		data, err := json.Marshal(map[string]any{"spec": map[string]any{
			"tenant":          map[string]string{"id": tenant, "name": tenant},
			"billing_account": map[string]string{"id": account, "name": accountName(account)},
		}, "status": status})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	// Each expected failure runs in a real savepoint and leaves its outer transaction usable.
	reject := func(ctx context.Context, code, constraint, query string, args ...any) {
		GinkgoHelper()
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tx.Rollback(ctx) }()
		sp, err := tx.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = sp.Exec(ctx, query, args...)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue(), "expected PostgreSQL rejection: %v", err)
		Expect(pgErr.Code).To(Equal(code))
		if constraint != "" {
			Expect(pgErr.ConstraintName).To(Equal(constraint))
		}
		Expect(sp.Rollback(ctx)).To(Succeed())
		var one int
		Expect(tx.QueryRow(ctx, `select 1`).Scan(&one)).To(Succeed())
		Expect(tx.Commit(ctx)).To(Succeed())
	}
	const insertAccount = `insert into billing_accounts (id, name, tenant, creator, data)
		values ($1, $2, 'system', 'system', $3::jsonb)`
	const insertAssignment = `insert into tenant_billing_assignments
		(id, name, tenant, creator, annotations, data)
		values ($1, 'same-name', $2, 'request-principal', jsonb_build_object('osac.openshift.io/tenant', $2::text), $3::jsonb)`

	It("preserves every predecessor value and backfills truthful canonical metadata and payloads", func(ctx context.Context) {
		accounts, assignments := legacyAccounts(ctx), legacyAssignments(ctx)
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		Expect(legacyAccounts(ctx)).To(Equal(accounts))
		Expect(legacyAssignments(ctx)).To(Equal(assignments))
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from billing_accounts where tenant = 'system' and project = ''::ltree
			and creation_timestamp = 'epoch' and creator = '' and deletion_timestamp = 'epoch'
			and version = 0 and finalizers = '{}' and labels = '{}' and annotations = '{}'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(5))
		for _, id := range []string{"plain", "padded", "absent", "empty", "whitespace"} {
			var name string
			Expect(conn.QueryRow(ctx, `select name from billing_accounts where id = $1`, id).Scan(&name)).To(Succeed())
			Expect(name).To(Equal(accountName(id)))
		}
		var data string
		Expect(conn.QueryRow(ctx, `select data::text from billing_accounts where id = 'plain'`).Scan(&data)).To(Succeed())
		Expect(data).To(MatchJSON(`{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-plain",
			"display_name":"Label ü","base_currency":"USD","external_billing_account_id":"ext-1",
			"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T01:02:03.000001Z"}}`))
		var tokens string
		Expect(conn.QueryRow(ctx, `select jsonb_object_agg(id, jsonb_build_array(
			data->'status' ? 'external_billing_account_id', data#>'{status,external_billing_account_id}'))::text
			from billing_accounts`).Scan(&tokens)).To(Succeed())
		Expect(tokens).To(MatchJSON(`{"plain":[true,"ext-1"],"padded":[true," ext-1 "],
			"absent":[false,null],"empty":[true,""],"whitespace":[true," \t "]}`))
		Expect(conn.QueryRow(ctx, `select data::text from tenant_billing_assignments where id = 'current'`).Scan(&data)).To(Succeed())
		Expect(data).To(MatchJSON(`{"spec":{"tenant":{"id":"tenant","name":"tenant"},
			"billing_account":{"id":"plain","name":"` + accountName("plain") + `"}},
			"status":{"connection_state":"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED",
			"effective_from_timestamp":"2026-02-01T00:00:00.000002Z","message":"current link"}}`))
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments where tenant = tenant_id
			and project = ''::ltree and annotations = jsonb_build_object('osac.openshift.io/tenant', tenant_id)
			and version = 0 and deletion_timestamp = 'epoch' and finalizers = '{}' and labels = '{}'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
	})

	It("projects standard-column writes one way, preserves raw tokens and owns new creation defaults", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		for _, id := range []string{"new", "é/immutable-id"} {
			_, err := conn.Exec(ctx, insertAccount, id, accountName(id), accountData(id, nil))
			Expect(err).ToNot(HaveOccurred())
		}
		var created time.Time
		var version int
		var projection string
		Expect(conn.QueryRow(ctx, `select creation_timestamp, version, provider_account_reference from billing_accounts
			where id = 'new'`).Scan(&created, &version, &projection)).To(Succeed())
		Expect(created.After(time.Unix(0, 0))).To(BeTrue())
		Expect(version).To(BeZero())
		Expect(projection).To(Equal("native-new"))
		_, err := conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Changed"'),
			version = version + 1 where id = 'new'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select display_name, version from billing_accounts where id = 'new'`).Scan(&projection, &version)).To(Succeed())
		Expect(projection).To(Equal("Changed"))
		Expect(version).To(Equal(1))
		reject(ctx, "23514", "", `update billing_accounts set display_name = 'second truth' where id = 'new'`)
		reject(ctx, "23514", "", `update billing_accounts set data = jsonb_set(data,
			'{status,provider_account_reference}', '"different"') where id = 'new'`)
		reject(ctx, "23514", "", insertAccount, "wrong-name", "label-derived", accountData("wrong-name", nil))
		reject(ctx, "23502", "", `update billing_accounts set labels = null where id = 'new'`)
	})

	It("retains the legacy raw uniqueness constraint identity for every present token and permits multiple absent tokens", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		for _, token := range []string{"ext-1", " ext-1 ", "", " \t "} {
			reject(ctx, "23505", "billing_accounts_unique_correlation_id", insertAccount,
				"duplicate", accountName("duplicate"), accountData("duplicate", &token))
		}
		reject(ctx, "23505", "billing_accounts_unique_correlation_id", `update billing_accounts set data = jsonb_set(data,
			'{status,external_billing_account_id}', '"ext-1"') where id = 'absent'`)
		reject(ctx, "23505", "billing_accounts_unique_provider_account", insertAccount,
			"duplicate-native", accountName("duplicate-native"), accountData("plain", nil))
		spaces, trailing := "  ", "ext-1 "
		for i, token := range []*string{nil, nil, &spaces, &trailing} {
			id := []string{"null-2", "null-3", "spaces", "trailing"}[i]
			_, err := conn.Exec(ctx, insertAccount, id, accountName(id), accountData(id, token))
			Expect(err).ToNot(HaveOccurred())
		}
		reject(ctx, "23514", "", `update billing_accounts set data = data #- '{status,external_billing_account_id}' where id = 'plain'`)
		reject(ctx, "23514", "", `update billing_accounts set data = jsonb_set(data,
			'{status,external_billing_account_id}', '"rotated"') where id = 'plain'`)
	})

	It("retains both current-interval indexes, account and live-history tenant foreign keys, and same-name history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		reject(ctx, "23505", "tenant_billing_assignments_unique_active_tenant", insertAssignment,
			"duplicate-tenant", "tenant", assignmentData("tenant", "padded", ""))
		reject(ctx, "23505", "tenant_billing_assignments_unique_active_account", insertAssignment,
			"duplicate-account", "other-tenant", assignmentData("other-tenant", "plain", ""))
		_, err := conn.Exec(ctx, insertAssignment, "another-closed", "tenant",
			assignmentData("tenant", "plain", "2026-03-01T00:00:00.000005Z"))
		Expect(err).ToNot(HaveOccurred(), "same name and a zero-length closed interval are legal")
		reject(ctx, "23503", "tenant_billing_assignments_billing_account_id_fkey", `delete from billing_accounts where id = 'plain'`)
		// Other pre-existing dependents (including the tenant's root project) may reject deletion first.
		reject(ctx, "23503", "", `delete from tenants where name = 'tenant'`)
		var indexes int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid
			where i.indisvalid and c.relname in ('billing_accounts_unique_provider_account',
			'billing_accounts_unique_correlation_id', 'tenant_billing_assignments_unique_active_tenant',
			'tenant_billing_assignments_unique_active_account', 'tenant_billing_assignments_by_tenant',
			'tenant_billing_assignments_by_account', 'tenant_billing_assignments_by_effective_from')`).Scan(&indexes)).To(Succeed())
		Expect(indexes).To(Equal(7))
		var tenantFKs int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_constraint where conrelid = 'tenant_billing_assignments'::regclass
			and confrelid = 'tenants'::regclass and contype = 'f'`).Scan(&tenantFKs)).To(Succeed())
		Expect(tenantFKs).To(Equal(1), "do not add a second metadata.tenant live FK before the deletion-identity cutover")
		var tenantFK string
		Expect(conn.QueryRow(ctx, `select conname from pg_constraint where conrelid = 'tenant_billing_assignments'::regclass
			and confrelid = 'tenants'::regclass and contype = 'f'`).Scan(&tenantFK)).To(Succeed())
		Expect(tenantFK).To(Equal("tenant_billing_assignments_tenant_id_fkey"))
		var requiredColumns int
		Expect(conn.QueryRow(ctx, `select count(*) from information_schema.columns where table_schema = 'public'
			and table_name in ('billing_accounts', 'tenant_billing_assignments') and is_nullable = 'NO'
			and column_name in ('id', 'name', 'creation_timestamp', 'deletion_timestamp', 'finalizers', 'creator',
			'tenant', 'project', 'labels', 'annotations', 'data', 'version')`).Scan(&requiredColumns)).To(Succeed())
		Expect(requiredColumns).To(Equal(24))
	})

	It("enforces canonical owner/reference tuples, valid intervals and immutable closed history", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		reject(ctx, "23514", "", insertAssignment, "inverted", "other-tenant",
			assignmentData("other-tenant", "padded", "2026-02-01T00:00:00Z"))
		reject(ctx, "23514", "", insertAssignment, "wrong-owner", "other-tenant", assignmentData("tenant", "padded", ""))
		reject(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{spec,billing_account,name}', '"wrong"') where id = 'current'`)
		reject(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_from_timestamp}', '"2026-01-01T00:00:00Z"') where id = 'current'`)
		reject(ctx, "23514", "", `update tenant_billing_assignments set message = 'projection only' where id = 'current'`)
		reject(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,message}', '"rewritten history"') where id = 'closed'`)
		reject(ctx, "23514", "", `update tenant_billing_assignments set creator = 'invented' where id = 'closed'`)
		_, err := conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', '"2026-03-01T00:00:00.000005Z"'), version = version + 1 where id = 'current'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, insertAssignment, "replacement", "tenant", assignmentData("tenant", "padded", ""))
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects historical deletion and noncanonical JSON field types without converting their values", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		reject(ctx, "23514", "", `delete from tenant_billing_assignments where id = 'closed'`)
		reject(ctx, "23514", "", `delete from tenant_billing_assignments where id = 'current'`)
		for _, value := range []string{`1`, `"ACTIVE"`, `null`} {
			reject(ctx, "23514", "", `update billing_accounts set data = jsonb_set(data, '{status,state}', $1::jsonb)
				where id = 'absent'`, value)
		}
		reject(ctx, "23514", "", `update billing_accounts set data = jsonb_set(data,
			'{status,external_billing_account_id}', 'null') where id = 'absent'`)
		reject(ctx, "23514", "", `update tenant_billing_assignments set data = jsonb_set(data,
			'{status,effective_to_timestamp}', 'null') where id = 'current'`)
	})

	It("rejects noncanonical or inexact interval endpoints on insert and update", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		const insert = `insert into tenant_billing_assignments (id, name, tenant, creator, annotations, data)
			values ('timestamp-check', 'same-name', 'other-tenant', 'request-principal',
				'{"osac.openshift.io/tenant":"other-tenant"}', jsonb_set($1::jsonb, $2::text[], to_jsonb($3::text)))`
		const update = `update tenant_billing_assignments set data = jsonb_set(data, $1::text[], to_jsonb($2::text))
			where id = 'current'`
		for _, endpoint := range []string{"effective_from_timestamp", "effective_to_timestamp"} {
			path := []string{"status", endpoint}
			// If rounding were allowed this timestamp would match the open interval's start on update.
			reject(ctx, "23514", "", update, path, "2026-02-01T00:00:00.000002001Z")
			for _, stamp := range invalidBillingTimestamps {
				By("rejecting " + endpoint + " = " + stamp)
				reject(ctx, "23514", "", insert, assignmentData("other-tenant", "padded", "9999-12-31T23:59:59.999999Z"), path, stamp)
				reject(ctx, "23514", "", update, path, stamp)
			}
		}
		// A sub-microsecond inversion would round to a legal zero-length interval.
		reject(ctx, "23514", "", insert, assignmentData("other-tenant", "padded", "2026-03-01T00:00:00.000005Z"),
			[]string{"status", "effective_from_timestamp"}, "2026-03-01T00:00:00.000005001Z")
		reject(ctx, "23514", "", insert, assignmentData("other-tenant", "padded", "2026-03-01T00:00:00.000004999Z"),
			[]string{"status", "effective_from_timestamp"}, "2026-03-01T00:00:00.000005Z")
		reject(ctx, "23514", "tenant_billing_assignments_valid_interval", update,
			[]string{"status", "effective_to_timestamp"}, "2026-02-01T00:00:00.000001Z")
	})

	It("projects exactly representable interval endpoints without rewriting JSON", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		_, err := conn.Exec(ctx, insertAssignment, "exact", "other-tenant", assignmentData("other-tenant", "padded", ""))
		Expect(err).NotTo(HaveOccurred())
		var open bool
		Expect(conn.QueryRow(ctx, `select effective_to is null from tenant_billing_assignments where id = 'exact'`).Scan(&open)).To(Succeed())
		Expect(open).To(BeTrue())
		_, err = conn.Exec(ctx, `update tenant_billing_assignments set data = jsonb_set(
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

	It("uses lifecycle-active companions including closed history, never interval-currentness", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		var ids []string
		Expect(conn.QueryRow(ctx, `select array_agg(id order by id) from active_tenant_billing_assignments`).Scan(&ids)).To(Succeed())
		Expect(ids).To(Equal([]string{"closed", "current"}))
		_, err := conn.Exec(ctx, `update billing_accounts set deletion_timestamp = clock_timestamp() where id = 'padded'`)
		Expect(err).ToNot(HaveOccurred())
		var active bool
		Expect(conn.QueryRow(ctx, `select exists(select 1 from active_billing_accounts where id = 'padded')`).Scan(&active)).To(Succeed())
		Expect(active).To(BeFalse())
	})

	It("keeps billing writes unpublished while an existing nonbilling enqueue trigger commits and rolls back", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		var before, after int
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&before)).To(Succeed())
		_, err := conn.Exec(ctx, insertAccount, "unpublished", accountName("unpublished"), accountData("unpublished", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Update"')
			where id = 'unpublished'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, insertAssignment, "unpublished-assignment", "other-tenant", assignmentData("other-tenant", "padded", ""))
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before))
		var triggers int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_trigger t join pg_proc p on p.oid = t.tgfoid
			where t.tgrelid in ('billing_accounts'::regclass, 'tenant_billing_assignments'::regclass)
			and p.proname = 'enqueue_change' and not t.tgisinternal`).Scan(&triggers)).To(Succeed())
		Expect(triggers).To(BeZero())
		_, err = conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data)
			values ('enqueue-control', 'enqueue-control', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before + 1))
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `insert into service_accounts (id, name, tenant, data)
			values ('rollback-control', 'rollback-control', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.Rollback(ctx)).To(Succeed())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before + 1))
		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})

	DescribeTable("fails rather than fabricating unrepresentable predecessor data", func(ctx context.Context, query string) {
		_, err := conn.Exec(ctx, query)
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, 156)).ToNot(Succeed())
	},
		Entry("unknown provider state", `update billing_accounts set state = 'RAW_PROVIDER_STATE' where id = 'absent'`),
		Entry("empty OSAC identity", `update billing_accounts set id = '' where id = 'absent'`),
		Entry("inverted historical interval", `update tenant_billing_assignments set effective_to = effective_from - interval '1 second' where id = 'closed'`),
		Entry("unknown connection state", `update tenant_billing_assignments set connection_state = 'RAW' where id = 'closed'`),
	)
})
