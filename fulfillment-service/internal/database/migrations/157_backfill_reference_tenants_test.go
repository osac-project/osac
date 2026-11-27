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
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Backfill full-reference tenant selectors", func() {
	insertCluster := func(ctx context.Context, data string) {
		_, err := conn.Exec(ctx, `insert into clusters (id, name, tenant, data)
			values ('selector-cluster', 'selector-cluster', 'system', $1::jsonb)`, data)
		Expect(err).ToNot(HaveOccurred())
	}

	It("rewrites resource, archive, generic-object, and change data", func(ctx context.Context) {
		insertCluster(ctx, `{
			"spec": {
				"version": {"id":"version-id","shared":true},
				"template": {"name":"template","tenant":"shared","shared":true},
				"template_parameters": {
					"raw": {"id":"parameter-id","shared":true},
					"nested": {"template":{"id":"parameter-template-id","shared":true}}
				},
				"add_on_operators": [
					{"name":"legacy-shared","shared":true},
					{"name":"owner-default","shared":false}
				],
				"node_sets": {
					"worker": {"baremetal_instance_type":{"id":"worker-type","tenant":"system","shared":false}},
					"default": {"baremetal_instance_type":{"id":"default-type","shared":true}}
				}
			},
			"custom": {"shared":true},
			"custom_reference_shaped": {"id":"business-id","shared":true},
			"custom_default": {"default":{"id":"business-default-id","shared":true}}
		}`)
		_, err := conn.Exec(ctx, `insert into archived_clusters (id, creation_timestamp, deletion_timestamp, tenant, data)
			values ('selector-archived-cluster', now(), now(), 'system', '{"spec":{"version":{"id":"archived-version","shared":true}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into objects (id, tenant, data)
			values ('selector-object', 'system', '{"reference":{"id":"object-reference","shared":true}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into archived_objects (id, creation_timestamp, deletion_timestamp, tenant, data)
			values ('selector-archived-object', now(), now(), 'system', '{"reference":{"id":"archived-object-reference","shared":true}}')`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 157)).To(Succeed())

		const expected = `{
			"spec": {
				"version": {"id":"version-id","tenant":"shared"},
				"template": {"name":"template","tenant":"shared"},
				"template_parameters": {
					"raw": {"id":"parameter-id","shared":true},
					"nested": {"template":{"id":"parameter-template-id","shared":true}}
				},
				"add_on_operators": [
					{"name":"legacy-shared","tenant":"shared"},
					{"name":"owner-default"}
				],
				"node_sets": {
					"worker": {"baremetal_instance_type":{"id":"worker-type","tenant":"system"}},
					"default": {"baremetal_instance_type":{"id":"default-type","tenant":"shared"}}
				}
			},
			"custom": {"shared":true},
			"custom_reference_shaped": {"id":"business-id","shared":true},
			"custom_default": {"default":{"id":"business-default-id","shared":true}}
		}`
		var resourceData string
		Expect(conn.QueryRow(ctx, `select data::text from clusters where id = 'selector-cluster'`).Scan(&resourceData)).To(Succeed())
		Expect(resourceData).To(MatchJSON(expected))
		for _, table := range []struct {
			name     string
			id       string
			expected string
		}{
			{
				name:     "archived_clusters",
				id:       "selector-archived-cluster",
				expected: `{"spec":{"version":{"id":"archived-version","tenant":"shared"}}}`,
			},
			{
				name:     "objects",
				id:       "selector-object",
				expected: `{"reference":{"id":"object-reference","tenant":"shared"}}`,
			},
			{
				name:     "archived_objects",
				id:       "selector-archived-object",
				expected: `{"reference":{"id":"archived-object-reference","tenant":"shared"}}`,
			},
		} {
			var migratedData string
			query := `select data::text from ` + table.name + ` where id = $1`
			Expect(conn.QueryRow(ctx, query, table.id).Scan(&migratedData)).To(Succeed())
			Expect(migratedData).To(MatchJSON(table.expected))
		}

		var changeData string
		Expect(conn.QueryRow(ctx, `select (data->'data')::text from changes where data->>'id' = 'selector-cluster' limit 1`).Scan(&changeData)).To(Succeed())
		Expect(changeData).To(MatchJSON(expected))
	})

	It("rewrites catalog node sets and default role references", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into cluster_catalog_items (id, name, tenant, data)
			values ('selector-catalog', 'selector-catalog', 'shared', '{"fields":{"node_sets":{"locked":{"items":{
				"shared":{"baremetal_instance_type":{"name":"catalog-type","shared":true}},
				"default":{"baremetal_instance_type":{"name":"default-type","shared":true}}
			}}}}}')`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 157)).To(Succeed())

		var catalogData string
		Expect(conn.QueryRow(ctx, `select (data #> '{fields,node_sets,locked,items}')::text
			from cluster_catalog_items where id = 'selector-catalog'`).Scan(&catalogData)).To(Succeed())
		Expect(catalogData).To(MatchJSON(`{
			"shared":{"baremetal_instance_type":{"name":"catalog-type","tenant":"shared"}},
			"default":{"baremetal_instance_type":{"name":"default-type","tenant":"shared"}}
		}`))
		var roleData string
		Expect(conn.QueryRow(ctx, `select (data #> '{spec,default_role}')::text
			from tenants where id = 'shared'`).Scan(&roleData)).To(Succeed())
		Expect(roleData).To(MatchJSON(`{
			"id":"019dd9f6-36cf-77bc-8041-c6a6a415c185",
			"name":"tenant-user",
			"tenant":"shared"
		}`))
	})

	It("includes every resource and archive table in the backfill schema", func(ctx context.Context) {
		var uncoveredTables string
		err := conn.QueryRow(ctx, `
			select coalesce(string_agg(c.relname, ', ' order by c.relname), '')
			from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where n.nspname = 'public'
			  and c.relkind in ('r', 'p')
			  and (
				c.relname like 'archived_%'
				or (
				  c.relname not like 'active_%'
				  and c.relname not in (
					'changes', 'installation_settings', 'notifications', 'project_membership_subjects',
					'schema_migrations', 'storage_tier_backends', 'tenant_domains'
				  )
				)
			  )
			  and (
				not exists (
				  select 1 from pg_catalog.pg_attribute a
				  where a.attrelid = c.oid and a.attname = 'data'
					and a.atttypid = 'jsonb'::regtype and not a.attisdropped
				)
				or not exists (
				  select 1 from pg_catalog.pg_attribute a
				  where a.attrelid = c.oid and a.attname = 'tenant' and not a.attisdropped
				)
			  )`,
		).Scan(&uncoveredTables)
		Expect(err).To(Succeed())
		Expect(uncoveredTables).To(BeEmpty(), "resource tables skipped by selector migration: %s", uncoveredTables)

		var changeDataType string
		err = conn.QueryRow(ctx, `
			select format_type(a.atttypid, a.atttypmod)
			from pg_catalog.pg_attribute a
			where a.attrelid = 'public.changes'::regclass and a.attname = 'data' and not a.attisdropped`,
		).Scan(&changeDataType)
		Expect(err).To(Succeed())
		Expect(changeDataType).To(Equal("jsonb"), "change snapshots must remain eligible for selector migration")
	})

	DescribeTable("rejects selectors that conflict with stored ownership",
		func(ctx context.Context, data, message string) {
			insertCluster(ctx, data)
			err := tool.Migrate(ctx, 157)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring(message))
			Expect(err.Error()).To(ContainSubstring(`public.changes:$.data.spec.version`))
			Expect(err.Error()).NotTo(ContainSubstring(`version-id`))
			Expect(err.Error()).NotTo(ContainSubstring(`tenant-a`))
		},
		Entry("shared=true with another explicit tenant",
			`{"spec":{"version":{"id":"version-id","tenant":"tenant-a","shared":true}}}`,
			`shared=true conflicts with tenant`,
		),
		Entry("shared=false with a non-owner tenant",
			`{"spec":{"version":{"id":"version-id","tenant":"tenant-a","shared":false}}}`,
			`does not match owning tenant`,
		),
	)
})
