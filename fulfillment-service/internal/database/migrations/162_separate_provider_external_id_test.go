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
	"errors"
	"net/url"
	"time"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
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
		Expect(tool.Migrate(ctx, 162)).To(Succeed())
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

	It("validates observation timestamps while preserving nanoseconds in JSON", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 162)).To(Succeed())
		const insert = `insert into billing_accounts (id, name, tenant, data)
			values ($1, 'ba-' || left(encode(sha256(convert_to($1, 'UTF8')), 'hex'), 60), 'system',
				jsonb_build_object('status', jsonb_build_object('provider_key', 'm360', 'provider_account_reference', $1,
					'state_observation_timestamp', $2::text)))`
		const update = `update billing_accounts set data = jsonb_set(data, '{status,state_observation_timestamp}', to_jsonb($1::text))
			where id = 'observation'`
		check := func(stamp, projected string) {
			GinkgoHelper()
			var stored string
			var matches bool
			Expect(conn.QueryRow(ctx, `select data#>>'{status,state_observation_timestamp}', status_observed_at = $1::text::timestamptz
				from billing_accounts where id = 'observation'`, projected).Scan(&stored, &matches)).To(Succeed())
			Expect(stored).To(Equal(stamp))
			Expect(matches).To(BeTrue(), "expected mirror %s for %s", projected, stamp)
		}
		rejectTimestamp := func(err error) {
			GinkgoHelper()
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue(), "expected PostgreSQL rejection: %v", err)
			Expect(pgErr.Code).To(Equal("23514"))
			Expect(pgErr.Message).To(Equal("invalid protobuf billing timestamp"))
		}
		_, err := conn.Exec(ctx, insert, "observation", "0001-01-01T00:00:00Z")
		Expect(err).NotTo(HaveOccurred())
		check("0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z")
		for _, stamp := range invalidBillingTimestamps {
			By("rejecting observation " + stamp)
			_, err = conn.Exec(ctx, insert, "invalid", stamp)
			rejectTimestamp(err)
			_, err = conn.Exec(ctx, update, stamp)
			rejectTimestamp(err)
		}
		check("0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z")
		for _, stamp := range []string{"2028-02-29T00:00:00.012Z", "2028-02-29T00:00:00.012345Z"} {
			_, err = conn.Exec(ctx, update, stamp)
			Expect(err).NotTo(HaveOccurred())
			check(stamp, stamp)
		}
		// Two observations 50 ns apart keep their distinct JSON values and the same SQL mirror.
		for _, stamp := range []string{"9999-12-31T23:59:59.999999949Z", "9999-12-31T23:59:59.999999999Z"} {
			_, err = conn.Exec(ctx, update, stamp)
			Expect(err).NotTo(HaveOccurred())
			check(stamp, "9999-12-31T23:59:59.999999Z")
		}
	})

	It("bounds its lock wait, rolls back cleanly and retries after a confirmed dirty reset", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, err := conn.Exec(ctx, `
			insert into billing_accounts (id, name, tenant, data)
				values ('discovered', 'ba-' || left(encode(sha256(convert_to('discovered', 'UTF8')), 'hex'), 60),
				 'system',
				 jsonb_build_object('status', jsonb_build_object('provider_key','m360',
				  'provider_account_reference','discovered','external_billing_account_id','discovered',
				  'state_observation_timestamp','2026-01-01T00:00:00Z')));`)
		Expect(err).NotTo(HaveOccurred())
		snapshot := func(ctx context.Context) string {
			GinkgoHelper()
			var result string
			Expect(conn.QueryRow(ctx, `select jsonb_build_array(
				pg_get_functiondef('project_billing_account()'::regprocedure),
				(select tgenabled from pg_trigger where tgname = 'project_billing_account'),
				(select jsonb_agg(jsonb_build_array(id, external_billing_account_id, data->'status') order by id)
				 from billing_accounts))::text`).Scan(&result)).To(Succeed())
			return result
		}
		before := snapshot(ctx)

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
		// ROW EXCLUSIVE is the lock a concurrent writer holds; it conflicts with the migration's SHARE ROW EXCLUSIVE.
		_, err = reader.Exec(ctx, `lock table billing_accounts in row exclusive mode`)
		Expect(err).NotTo(HaveOccurred())

		err = boundedTool.Migrate(ctx, 162)
		var migrationErr migratedb.Error
		Expect(errors.As(err, &migrationErr)).To(BeTrue(), "expected a PostgreSQL migration error, got %v", err)
		var pgErr *pgconn.PgError
		Expect(errors.As(migrationErr.OrigErr, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("55P03"))
		Expect(pgErr.Message).To(ContainSubstring("Clear conflicting"))
		Expect(pgErr.Detail).To(ContainSubstring("After confirming the rollback"))
		Expect(pgErr.Detail).To(ContainSubstring("reset the dirty migration version to 161"))
		Expect(reader.Rollback(ctx)).To(Succeed())

		var version int
		var dirty bool
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(162), "the failed migration must not record 162 as applied")
		Expect(dirty).To(BeTrue())
		Expect(snapshot(ctx)).To(Equal(before), "the failed migration must not publish any partial change")

		// The diagnostic reset is only valid because the failed transaction rolled back; apply it and retry.
		_, err = conn.Exec(ctx, `update schema_migrations set version = 161, dirty = false`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 162)).To(Succeed())
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(162))
		Expect(dirty).To(BeFalse())
		var token *string
		Expect(conn.QueryRow(ctx, `select external_billing_account_id from billing_accounts
			where id = 'discovered'`).Scan(&token)).To(Succeed())
		Expect(token).To(BeNil(), "the retry releases the discovery-only reservation")
	})
})
