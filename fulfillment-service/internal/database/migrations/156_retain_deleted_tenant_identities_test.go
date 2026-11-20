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
	"net/url"
	"time"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = DescribeMigration("Retain deleted tenant identities", func() {
	const archive = `insert into archived_tenants
		(id, name, tenant, creation_timestamp, deletion_timestamp, archival_timestamp, data)
		values ($1, $2, $2, $3::timestamptz, $4::timestamptz, $5::timestamptz, '{"spec":{},"status":{}}')`
	addArchive := func(ctx context.Context, id, name, created, deleted, finalized string) {
		GinkgoHelper()
		_, err := conn.Exec(ctx, archive, id, name, created, deleted, finalized)
		Expect(err).NotTo(HaveOccurred())
	}
	BeforeEach(func(ctx context.Context) {
		var version int
		Expect(conn.QueryRow(ctx, `select version from schema_migrations`).Scan(&version)).To(Succeed())
		Expect(version).To(Equal(155))
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, deletion_timestamp, finalizers, data) values
			 ('pending', 'pending', 'pending', '2026-01-01Z', '2026-03-01Z', '{cleanup}', '{}'),
			 ('empty', 'empty', 'empty', '2026-01-01Z', '2026-03-01Z', '{}', '{}'),
			 ('active', 'active', 'active', '2026-03-01Z', 'epoch', '{}', '{}'),
			 ('reused', 'reused', 'reused', '2026-03-01Z', 'epoch', '{}', '{}'),
			 ('reused-deleting', 'reused-deleting', 'reused-deleting', '2026-03-01Z', '2026-04-01Z', '{}', '{}');
			insert into billing_accounts (id, name, tenant, data) values
			 ('account', 'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60), 'system',
			  '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native",
			    "external_billing_account_id":" raw-token ","state":"BILLING_ACCOUNT_STATE_ACTIVE",
			    "state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id, name, tenant, annotations, data)
			select t, 'same-name', t, jsonb_build_object('osac.openshift.io/tenant', t),
			 jsonb_build_object('spec', jsonb_build_object('tenant', jsonb_build_object('id', t, 'name', t),
			  'billing_account', jsonb_build_object('id', 'account', 'name',
			   'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60))),
			  'status', jsonb_build_object('connection_state', 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING',
			   'effective_from_timestamp', '2025-01-01T00:00:00Z',
			   'effective_to_timestamp', '2025-02-01T00:00:00Z', 'message', 'unchanged'))
			from unnest(array['pending','empty','reused','reused-deleting']) t;
		`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into tenant_billing_assignments (id, name, tenant, annotations, data)
		 select 'current', name, tenant, annotations, data #- '{status,effective_to_timestamp}'
		 from tenant_billing_assignments where id = 'pending'`)
		Expect(err).NotTo(HaveOccurred())
		addArchive(ctx, "legacy-id", "gone", "2025-01-01Z", "2025-02-01Z", "2025-02-02Z")
		addArchive(ctx, "old-id", "repeated", "2024-01-01Z", "2024-02-01Z", "2024-02-02Z")
		addArchive(ctx, "new-id", "repeated", "2025-01-01Z", "2025-02-01Z", "2025-02-02Z")
		for _, name := range []string{"reused", "reused-deleting"} {
			addArchive(ctx, "legacy-"+name, name, "2025-01-01Z", "2025-02-01Z", "2025-02-02Z")
		}
	})
	// Whole-row snapshots include canonical JSON, raw tokens, metadata and all closed history.
	snapshot := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_build_array(
		 (select jsonb_agg(to_jsonb(t) order by name) from tenants t),
		 (select jsonb_agg(to_jsonb(t) order by name, creation_timestamp) from archived_tenants t),
		 (select jsonb_agg(to_jsonb(t) order by id) from billing_accounts t),
		 (select jsonb_agg(to_jsonb(t) order by id) from tenant_billing_assignments t))::text`).Scan(&result)).To(Succeed())
		return result
	}
	foreignKeys := func(ctx context.Context) int {
		GinkgoHelper()
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_constraint where contype = 'f'
		 and conrelid = 'tenant_billing_assignments'::regclass and confrelid = 'tenants'::regclass`).Scan(&count)).To(Succeed())
		return count
	}
	It("backfills recorded archives and pending live deletions without changing history or raw tokens", func(ctx context.Context) {
		before := snapshot(ctx)
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		Expect(snapshot(ctx)).To(Equal(before))
		Expect(foreignKeys(ctx)).To(BeZero(), "retained history never carries a live tenant foreign key")
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_agg(jsonb_build_array(tenant_name,
		 to_char(tenant_created_at at time zone 'UTC', 'YYYY-MM-DD'),
		 to_char(deletion_timestamp at time zone 'UTC', 'YYYY-MM-DD'), deleted_by,
		 to_char(finalized_at at time zone 'UTC', 'YYYY-MM-DD'), billing_retained) order by tenant_name)::text
		 from deleted_tenant_identities`).Scan(&result)).To(Succeed())
		Expect(result).To(MatchJSON(`[
		 ["empty","2026-01-01","2026-03-01",null,null,true],
		 ["gone","2025-01-01","2025-02-01",null,"2025-02-02",false],
		 ["pending","2026-01-01","2026-03-01",null,null,true],
		 ["repeated","2025-01-01","2025-02-01",null,"2025-02-02",false],
		 ["reused","2025-01-01","2025-02-01",null,"2025-02-02",false],
		 ["reused-deleting","2026-03-01","2026-04-01",null,null,true]]`))
		var goneMarker bool
		Expect(conn.QueryRow(ctx, `select exists(select 1 from deleted_tenant_identities
			where tenant_name = 'gone')`).Scan(&goneMarker)).To(Succeed())
		Expect(goneMarker).To(BeTrue(), "a pre-upgrade archive-only deletion is reserved")
		var reusedLive bool
		Expect(conn.QueryRow(ctx, `select exists(select 1 from tenants
			where name = 'reused' and deletion_timestamp = 'epoch')`).Scan(&reusedLive)).To(Succeed())
		Expect(reusedLive).To(BeTrue(), "backfill must not replace a valid same-name live incarnation")
		var indexes int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid
		 where i.indisvalid and c.relname in ('billing_accounts_unique_provider_account',
		 'billing_accounts_unique_external_billing_account_id', 'tenant_billing_assignments_unique_active_tenant',
		 'tenant_billing_assignments_unique_active_account', 'tenant_billing_assignments_by_tenant',
		 'tenant_billing_assignments_by_account', 'tenant_billing_assignments_by_effective_from')`).Scan(&indexes)).To(Succeed())
		Expect(indexes).To(Equal(7))
		var accountFKs int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_constraint where contype = 'f'
		 and conrelid = 'tenant_billing_assignments'::regclass and confrelid = 'billing_accounts'::regclass
		 and convalidated`).Scan(&accountFKs)).To(Succeed())
		Expect(accountFKs).To(Equal(1))
		var actor *string
		var finalized *time.Time
		Expect(conn.QueryRow(ctx, `select deleted_by, finalized_at from deleted_tenant_identities
		 where tenant_name = 'empty'`).Scan(&actor, &finalized)).To(Succeed())
		Expect(actor).To(BeNil())
		Expect(finalized).To(BeNil(), "empty finalizers do not prove physical removal")
	})
	It("allows closing retained history after physical removal without weakening the account foreign key", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		_, err := conn.Exec(ctx, `delete from projects where tenant = 'pending';
		 delete from tenants where name = 'pending';
		 update tenant_billing_assignments set data = jsonb_set(data, '{status,effective_to_timestamp}',
		 '"2026-03-01T00:00:00Z"'), version = version + 1 where id = 'current'`)
		Expect(err).NotTo(HaveOccurred(), "there is no UPDATE live-tenant guard on immutable billing history")
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments
		 where tenant_id = 'pending' and effective_to is not null`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		_, err = conn.Exec(ctx, `delete from billing_accounts where id = 'account'`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue(),
			"retained history still references its original billing account, got %v", err)
		Expect(pgErr.Code).To(Equal("23503"), "foreign_key_violation, not an unrelated delete failure")
		Expect(pgErr.ConstraintName).To(Equal("tenant_billing_assignments_billing_account_id_fkey"))
	})
	It("times out behind a tenant writer without changing the predecessor schema or rows", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		before, fks := snapshot(ctx), foreignKeys(ctx)

		// Tool.Migrate opens its own connection and the driver doesn't use the caller's context.
		// Bound it independently so a regression fails while the reader still holds its lock.
		migrationURL, err := url.Parse(tool.URL())
		Expect(err).NotTo(HaveOccurred())
		params := migrationURL.Query()
		params.Set("connect_timeout", "5")
		params.Set("statement_timeout", "15000")
		params.Set("x-statement-timeout", "20000")
		migrationURL.RawQuery = params.Encode()
		boundedTool, err := database.NewTool().SetLogger(logger).SetURL(migrationURL.String()).Build()
		Expect(err).NotTo(HaveOccurred())

		reader, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := reader.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		// ROW EXCLUSIVE is the lock a concurrent tenant writer holds; it conflicts with the migration's
		// SHARE ROW EXCLUSIVE.
		_, err = reader.Exec(ctx, `lock table tenants in row exclusive mode`)
		Expect(err).NotTo(HaveOccurred())

		err = boundedTool.Migrate(ctx, 156)
		var migrationErr migratedb.Error
		Expect(errors.As(err, &migrationErr)).To(BeTrue(), "expected a PostgreSQL migration error, got %v", err)
		var pgErr *pgconn.PgError
		Expect(errors.As(migrationErr.OrigErr, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("55P03"))
		Expect(pgErr.Message).To(ContainSubstring("Drain tenant writers"))
		Expect(pgErr.Detail).To(ContainSubstring("After confirming rollback"))
		Expect(pgErr.Detail).To(ContainSubstring("reset the dirty migration version to 155"))
		Expect(reader.Rollback(ctx)).To(Succeed())

		var version int
		var dirty, absent bool
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(156))
		Expect(dirty).To(BeTrue())
		Expect(conn.QueryRow(ctx, `select to_regclass('deleted_tenant_identities') is null`).Scan(&absent)).To(Succeed())
		Expect(absent).To(BeTrue())
		Expect(foreignKeys(ctx)).To(Equal(fks))
		Expect(snapshot(ctx)).To(Equal(before))
	})
	DescribeTable("rejects unprovable predecessor evidence atomically", func(ctx context.Context, corrupt string) {
		_, err := conn.Exec(ctx, corrupt)
		Expect(err).NotTo(HaveOccurred())
		before := snapshot(ctx)
		fks := foreignKeys(ctx)
		Expect(tool.Migrate(ctx, 156)).NotTo(Succeed())
		Expect(snapshot(ctx)).To(Equal(before))
		Expect(foreignKeys(ctx)).To(Equal(fks))
		var absent bool
		Expect(conn.QueryRow(ctx, `select to_regclass('deleted_tenant_identities') is null`).Scan(&absent)).To(Succeed())
		Expect(absent).To(BeTrue())
	},
		Entry("archive transaction clock predates deletion", `update archived_tenants set archival_timestamp = '2025-01-31Z' where name = 'gone'`),
		Entry("inverted creation", `update archived_tenants set creation_timestamp = '2025-03-01Z' where name = 'gone'`),
		Entry("infinite finalization", `update archived_tenants set archival_timestamp = 'infinity' where name = 'gone'`),
		Entry("unrepresentable creation", `update archived_tenants set creation_timestamp = '0001-01-01 BC' where name = 'gone'`),
		Entry("unknown epoch creation", `update archived_tenants set creation_timestamp = 'epoch' where name = 'gone'`),
		Entry("unknown epoch deletion", `update archived_tenants set deletion_timestamp = 'epoch' where name = 'gone'`),
		Entry("empty archive name", `update archived_tenants set name = '' where name = 'gone'`),
		Entry("duplicate incarnation", `insert into archived_tenants select * from archived_tenants where name = 'gone'`),
		Entry("tied succession", `update archived_tenants set creation_timestamp = '2024-02-02Z' where id = 'new-id'`),
		Entry("overlap", `update archived_tenants set creation_timestamp = '2024-02-01Z' where id = 'new-id'`),
		Entry("live overlaps archive", `update archived_tenants set archival_timestamp = '2026-03-01Z' where name = 'reused'`),
		Entry("inverted live deletion", `update tenants set deletion_timestamp = '2025-01-01Z' where name = 'pending'`),
		Entry("infinite live creation", `update tenants set creation_timestamp = 'infinity' where name = 'active'`),
	)
})
