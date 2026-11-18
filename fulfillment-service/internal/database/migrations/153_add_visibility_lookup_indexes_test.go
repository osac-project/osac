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

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add visibility lookup indexes", func() {
	// Exercise the historical archive-based lookup this migration indexed. The current resolver requires
	// the identity store introduced in migration 157, which does not exist in this version-specific fixture.
	const livenessQuery = `
		select claimed.name
		from unnest($1::text[]) with ordinality as claimed(name, position)
		left join tenants current on current.name = claimed.name
		where current.deletion_timestamp = 'epoch'
		   or (current.id is null and not exists (
		       select 1 from archived_tenants archived where archived.name = claimed.name
		   ))
		order by claimed.position
	`
	// This is the membership lookup from auth.DefaultTenancyLogic.DetermineVisibility.
	const membershipQuery = `select tenant, project from project_membership_subjects
		where tenant = any($1) and "user" = $2`
	type membership struct {
		Tenant  string
		Project string
	}

	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, data) values
				('tenant-a', 'tenant-a', 'tenant-a', '{}'), ('tenant-b', 'tenant-b', 'tenant-b', '{}'),
				('revived', 'revived', 'revived', '{}'), ('deleting', 'deleting', 'deleting', '{}');
			update tenants set deletion_timestamp = now() where id = 'deleting';
			insert into archived_tenants (id, name, data, creation_timestamp, deletion_timestamp) values
				('old-1', 'archived', '{}', now(), now()),
				('old-2', 'archived', '{}', now(), now()),
				('old-revived', 'revived', '{}', now(), now());
			insert into projects (id, name, tenant, project, data) values
				('project-a', 'project-a', 'tenant-a', '', '{}'),
				('project-b', 'project-b', 'tenant-b', '', '{}');
			insert into project_memberships (id, name, tenant, project, data) values
				('membership-a', 'membership-a', 'tenant-a', 'project-a',
				 '{"spec":{"users":["target-user","other-user"]}}'),
				('membership-b', 'membership-b', 'tenant-b', 'project-b',
				 '{"spec":{"users":["target-user"]}}');
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("indexes existing rows without changing visibility or data", func(ctx context.Context) {
		checkVisibility := func() {
			rows, err := conn.Query(ctx, livenessQuery, []string{
				"archived", "tenant-a", "deleting", "missing", "revived", "tenant-b",
			})
			Expect(err).ToNot(HaveOccurred())
			live, err := pgx.CollectRows(rows, pgx.RowTo[string])
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(Equal([]string{"tenant-a", "missing", "revived", "tenant-b"}))
			rows, err = conn.Query(ctx, livenessQuery, []string{
				"revived", "archived", "tenant-b", "revived", "missing", "deleting", "tenant-b",
			})
			Expect(err).ToNot(HaveOccurred())
			live, err = pgx.CollectRows(rows, pgx.RowTo[string])
			Expect(err).ToNot(HaveOccurred())
			Expect(live).To(Equal([]string{"revived", "tenant-b", "revived", "missing", "tenant-b"}))
			rows, err = conn.Query(ctx, membershipQuery, []string{"tenant-a"}, "target-user")
			Expect(err).ToNot(HaveOccurred())
			memberships, err := pgx.CollectRows(rows, pgx.RowToStructByPos[membership])
			Expect(err).ToNot(HaveOccurred())
			Expect(memberships).To(Equal([]membership{{Tenant: "tenant-a", Project: "project-a"}}))
		}
		indexes := func() []string {
			rows, err := conn.Query(ctx, `select indexdef from pg_indexes
				where schemaname = current_schema() order by indexname`)
			Expect(err).ToNot(HaveOccurred())
			result, err := pgx.CollectRows(rows, pgx.RowTo[string])
			Expect(err).ToNot(HaveOccurred())
			return result
		}
		// Snapshot complete rows, including the duplicate archive names and materialized subjects.
		data := func() string {
			var result string
			Expect(conn.QueryRow(ctx, `select jsonb_build_array(
				(select jsonb_agg(t order by id) from tenants t),
				(select jsonb_agg(t order by id) from archived_tenants t),
				(select jsonb_agg(t order by id) from project_memberships t),
				(select jsonb_agg(t order by tenant, project, "user") from project_membership_subjects t)
			)::text`).Scan(&result)).To(Succeed())
			return result
		}
		originalIndexes, originalData := indexes(), data()
		checkVisibility()
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		Expect(indexes()).To(ConsistOf(append(originalIndexes,
			"CREATE INDEX archived_tenants_by_name ON public.archived_tenants USING btree (name)",
			"CREATE INDEX project_membership_subjects_by_tenant_user ON public.project_membership_subjects "+
				"USING btree (tenant, \"user\")",
		)))
		Expect(data()).To(Equal(originalData))
		checkVisibility()
	})

	It("uses the indexes for analyzed tenant liveness and membership lookups", func(ctx context.Context) {
		// Use enough history and subjects to make selective lookups worthwhile to the default planner.
		// Do not disable sequential scans or assert elapsed times.
		_, err := conn.Exec(ctx, `
			insert into archived_tenants (id, name, data, creation_timestamp, deletion_timestamp)
				select 'history-' || i, 'history-' || i, '{}', now(), now()
				from generate_series(1, 205000) i;
			update project_memberships set data = jsonb_build_object('spec', jsonb_build_object('users',
				(select jsonb_agg('bulk-user-' || i) from generate_series(1, 20000) i)
				|| '["target-user"]'::jsonb)) where id = 'membership-a';
		`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		_, err = conn.Exec(ctx, `analyze tenants; analyze archived_tenants; analyze project_membership_subjects`)
		Expect(err).ToNot(HaveOccurred())

		explain := func(query string, args ...any) string {
			rows, err := conn.Query(ctx, "explain (analyze, buffers) "+query, args...)
			Expect(err).ToNot(HaveOccurred())
			lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
			Expect(err).ToNot(HaveOccurred())
			plan := strings.Join(lines, "\n")
			GinkgoWriter.Println(plan)
			return plan
		}
		// Explain the same full historical query used by the visibility assertions, not just its archive probe.
		plan := explain(livenessQuery, []string{"history-100000", "missing", "tenant-a"})
		Expect(plan).To(ContainSubstring("archived_tenants_by_name"))
		plan = explain(membershipQuery, []string{"tenant-a"}, "target-user")
		Expect(plan).To(ContainSubstring("project_membership_subjects_by_tenant_user"))
		Expect(plan).To(ContainSubstring("Index Cond:"))
	})
})
