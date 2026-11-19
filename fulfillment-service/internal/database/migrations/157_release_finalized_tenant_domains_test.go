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
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = DescribeMigration("Release finalized tenant domains", func() {
	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, finalizers, data) values
				('live', 'live', 'live', '{}', '{"spec":{"domains":["live.example.com"]}}'),
				('pending', 'pending', 'pending', '{cleanup}', '{"spec":{"domains":["pending.example.com"]}}'),
				('complete', 'complete', 'complete', '{}', '{"spec":{"domains":["complete.example.com"]}}');
			update tenants set deletion_timestamp = now() where id in ('pending', 'complete');
			insert into billing_accounts (id, provider_key, provider_account_reference)
				values ('account', 'm360', 'provider-account');
			insert into tenant_billing_assignments (id, name, tenant_id, billing_account_id, effective_to)
				values ('history', 'history', 'complete', 'account', now());
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	owners := func(ctx context.Context) []string {
		GinkgoHelper()
		rows, err := conn.Query(ctx, `select domain || ':' || tenant from tenant_domains order by domain`)
		Expect(err).ToNot(HaveOccurred())
		result, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())
		return result
	}
	function := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select prosrc from pg_proc where oid = 'materialize_tenant_domains()'::regprocedure`).
			Scan(&result)).To(Succeed())
		return result
	}
	history := func(ctx context.Context) string {
		GinkgoHelper()
		var result string
		Expect(conn.QueryRow(ctx, `select jsonb_build_array(
			(select jsonb_agg(t order by id) from tenants t),
			(select jsonb_agg(t order by id) from tenant_billing_assignments t)
		)::text`).Scan(&result)).To(Succeed())
		return result
	}
	It("repairs only completed tombstones and preserves tenant text and closed billing history", func(ctx context.Context) {
		before := history(ctx)
		Expect(owners(ctx)).To(HaveLen(3))
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		Expect(owners(ctx)).To(Equal([]string{"live.example.com:live", "pending.example.com:pending"}))
		Expect(history(ctx)).To(Equal(before))
	})

	It("times out behind a tenant writer without changing reservations, history, or the trigger", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		beforeFunction, beforeOwners, beforeHistory := function(ctx), owners(ctx), history(ctx)

		// Tool.Migrate opens its own connection and the driver doesn't use the caller's context.
		// Bound it independently so a regression fails while the writer still holds its lock.
		migrationURL, err := url.Parse(tool.URL())
		Expect(err).NotTo(HaveOccurred())
		params := migrationURL.Query()
		params.Set("connect_timeout", "5")
		params.Set("statement_timeout", "15000")
		params.Set("x-statement-timeout", "20000")
		migrationURL.RawQuery = params.Encode()
		boundedTool, err := database.NewTool().SetLogger(logger).SetURL(migrationURL.String()).Build()
		Expect(err).NotTo(HaveOccurred())

		writer, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := writer.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		// A successful UPDATE already holds ROW EXCLUSIVE until rollback; no polling or waiter is needed.
		tag, err := writer.Exec(ctx, `update tenants set data = data where id = 'live'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tag.RowsAffected()).To(Equal(int64(1)))

		err = boundedTool.Migrate(ctx, 157)
		var migrationErr migratedb.Error
		Expect(errors.As(err, &migrationErr)).To(BeTrue(), "expected a PostgreSQL migration error, got %v", err)
		var pgErr *pgconn.PgError
		Expect(errors.As(migrationErr.OrigErr, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("55P03"))
		Expect(pgErr.Message).To(ContainSubstring("Drain tenant writers"))
		Expect(pgErr.Detail).To(ContainSubstring("reset the dirty migration version"))
		Expect(writer.Rollback(ctx)).To(Succeed())

		var version int
		var dirty bool
		Expect(conn.QueryRow(ctx, `select version, dirty from schema_migrations`).Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(157))
		Expect(dirty).To(BeTrue())
		Expect(function(ctx)).To(Equal(beforeFunction))
		Expect(owners(ctx)).To(Equal(beforeOwners))
		Expect(history(ctx)).To(Equal(beforeHistory))
	})

	It("holds reservations through cleanup, rolls back release, and never re-reserves a completed tombstone", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, data)
			values ('collision', 'collision', 'collision', '{"spec":{"domains":["pending.example.com"]}}')`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))

		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := tx.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		_, err = tx.Exec(ctx, `update tenants set finalizers = '{}' where id = 'pending'`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from tenant_domains where tenant = 'pending'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(tx.Rollback(ctx)).To(Succeed())
		Expect(owners(ctx)).To(ContainElement("pending.example.com:pending"))

		_, err = conn.Exec(ctx, `
			update tenants set finalizers = '{}' where id = 'pending';
			insert into tenants (id, name, tenant, data)
				values ('replacement', 'replacement', 'replacement', '{"spec":{"domains":["pending.example.com"]}}');
			update tenants set data = jsonb_set(data, '{status}', '{"phase":"deleted"}') where id = 'pending';
			update tenants set deletion_timestamp = deletion_timestamp where id = 'pending';
			update tenants set finalizers = '{late-private-update}' where id = 'pending';
			update tenants set data = data where id = 'pending';
			update tenants set finalizers = '{}' where id = 'pending';
		`)
		Expect(err).ToNot(HaveOccurred())
		Expect(owners(ctx)).To(Equal([]string{"live.example.com:live", "pending.example.com:replacement"}))
	})

	It("retains the forward materializer and never restores completed reservations", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		beforeFunction, beforeOwners, beforeHistory := function(ctx), owners(ctx), history(ctx)
		Expect(beforeFunction).To(ContainSubstring("cardinality(new.finalizers)"))
		_, err := conn.Exec(ctx, `update tenants set data = data where id = 'complete'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(function(ctx)).To(Equal(beforeFunction))
		Expect(owners(ctx)).To(Equal(beforeOwners))
		Expect(history(ctx)).To(Equal(beforeHistory))
	})

	It("rolls back a failed reservation transaction after domain reuse without changing ownership, history or trigger", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, data)
			values ('replacement', 'replacement', 'replacement', '{"spec":{"domains":["complete.example.com"]}}')`)
		Expect(err).ToNot(HaveOccurred())
		beforeFunction, beforeOwners, beforeHistory := function(ctx), owners(ctx), history(ctx)
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := tx.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		_, err = tx.Exec(ctx, `update tenants set finalizers = '{}' where id = 'pending';
			insert into tenants (id, name, tenant, data)
			values ('blocked', 'blocked', 'blocked', '{"spec":{"domains":["live.example.com"]}}')`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(tx.Rollback(ctx)).To(Succeed())
		Expect(function(ctx)).To(Equal(beforeFunction))
		Expect(owners(ctx)).To(Equal(beforeOwners))
		Expect(history(ctx)).To(Equal(beforeHistory))
	})
})
