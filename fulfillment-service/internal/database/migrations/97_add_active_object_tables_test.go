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
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add active object tables", func() {
	// objectTables returns all tables that have a deletion_timestamp column, excluding active_* and
	// archived_* companion tables. These are the object tables that should each have an active_*
	// companion table and a materialize_active_objects trigger.
	objectTables := func(ctx context.Context) []string {
		rows, err := conn.Query(ctx, `
			select c.relname
			from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			join pg_catalog.pg_attribute a on a.attrelid = c.oid
			where n.nspname = 'public'
			  and c.relkind = 'r'
			  and a.attname = 'deletion_timestamp'
			  and c.relname not like 'active_%'
			  and c.relname not like 'archived_%'
			order by c.relname
		`)
		Expect(err).ToNot(HaveOccurred())
		tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())
		return tables
	}

	It("Creates the materialize_active_objects function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'materialize_active_objects'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates an active_* companion table for every object table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		for _, table := range objectTables(ctx) {
			activeTable := "active_" + table
			var exists bool
			err = conn.QueryRow(ctx, `
				select exists (
					select 1
					from pg_catalog.pg_class c
					join pg_catalog.pg_namespace n on n.oid = c.relnamespace
					where n.nspname = 'public'
					  and c.relkind = 'r'
					  and c.relname = $1
				)
			`, activeTable).Scan(&exists)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeTrue(), "expected active companion table %q for object table %q", activeTable, table)
		}
	})

	It("Attaches the materialize_active_objects trigger to every object table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		for _, table := range objectTables(ctx) {
			var count int
			err = conn.QueryRow(ctx, `
				select count(*)
				from information_schema.triggers
				where trigger_name = 'materialize_active_objects'
				  and event_object_table = $1
			`, table).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(
				BeNumerically(">=", 1),
				"expected materialize_active_objects trigger on table %q", table,
			)
		}
	})

	It("Inserts active objects into the companion table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, data)
			 values ('obj-1', 't-1', 'p-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-1')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Does not insert soft-deleted objects into the companion table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, deletion_timestamp, data)
			 values ('obj-deleted', 't-1', 'p-1', now(), '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-deleted')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Removes objects from the companion table on soft deletion", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, data)
			 values ('obj-2', 't-1', 'p-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-2')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		_, err = conn.Exec(ctx,
			`update objects set deletion_timestamp = now() where id = 'obj-2'`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-2')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Re-adds objects to the companion table when un-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, deletion_timestamp, data)
			 values ('obj-3', 't-1', 'p-1', now(), '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-3')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())

		_, err = conn.Exec(ctx,
			`update objects set deletion_timestamp = 'epoch' where id = 'obj-3'`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-3')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Rejects foreign key references to soft-deleted objects", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, data)
			 values ('obj-active', 't-1', 'p-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, deletion_timestamp, data)
			 values ('obj-deleted', 't-1', 'p-1', now(), '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			create table fk_test (
				id text primary key,
				ref_id text not null references active_objects (id)
			)
		`)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = conn.Exec(ctx, `drop table if exists fk_test`)
		})

		_, err = conn.Exec(ctx,
			`insert into fk_test (id, ref_id) values ('test-1', 'obj-active')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into fk_test (id, ref_id) values ('test-2', 'obj-deleted')`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23503"))
		Expect(pgErr.Detail).To(ContainSubstring("active_objects"))
	})

	It("Backfills active tables with existing active rows", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, data)
			 values ('pre-active', 't-1', 'p-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, deletion_timestamp, data)
			 values ('pre-deleted', 't-1', 'p-1', now(), '{}')`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		var activeCount int
		err = conn.QueryRow(ctx,
			`select count(*) from active_objects`).Scan(&activeCount)
		Expect(err).ToNot(HaveOccurred())
		Expect(activeCount).To(Equal(1))

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'pre-active')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'pre-deleted')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Cascades physical row deletion to the companion table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			 values ('p-1', 't-1', 'p-1', 'p-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into objects (id, tenant, project, data)
			 values ('obj-phys', 't-1', 'p-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var exists bool
		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-phys')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		_, err = conn.Exec(ctx, `delete from objects where id = 'obj-phys'`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx,
			`select exists (select 1 from active_objects where id = 'obj-phys')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Creates active companion tables for all known object tables", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		expectedTables := []string{
			"bare_metal_instance_catalog_items",
			"bare_metal_instance_templates",
			"bare_metal_instance_types",
			"bare_metal_instances",
			"cluster_catalog_items",
			"cluster_templates",
			"cluster_versions",
			"clusters",
			"compute_instance_catalog_items",
			"compute_instance_templates",
			"compute_instances",
			"external_ip_attachments",
			"external_ip_pools",
			"external_ips",
			"host_types",
			"hubs",
			"identity_providers",
			"instance_types",
			"nat_gateways",
			"network_classes",
			"objects",
			"project_memberships",
			"projects",
			"role_bindings",
			"roles",
			"secrets",
			"security_groups",
			"storage_backends",
			"storage_tiers",
			"subnets",
			"tenants",
			"users",
			"virtual_networks",
		}

		for _, table := range expectedTables {
			activeTable := "active_" + table

			var tableExists bool
			err = conn.QueryRow(ctx, `
				select exists (
					select 1
					from pg_catalog.pg_class c
					join pg_catalog.pg_namespace n on n.oid = c.relnamespace
					where n.nspname = 'public' and c.relkind = 'r' and c.relname = $1
				)
			`, activeTable).Scan(&tableExists)
			Expect(err).ToNot(HaveOccurred())
			Expect(tableExists).To(BeTrue(),
				fmt.Sprintf("missing active companion table %q for %q", activeTable, table))

			var triggerCount int
			err = conn.QueryRow(ctx, `
				select count(*)
				from information_schema.triggers
				where trigger_name = 'materialize_active_objects'
				  and event_object_table = $1
			`, table).Scan(&triggerCount)
			Expect(err).ToNot(HaveOccurred())
			Expect(triggerCount).To(BeNumerically(">=", 1),
				fmt.Sprintf("missing materialize_active_objects trigger on %q", table))
		}

		discoveredTables := objectTables(ctx)
		for _, table := range discoveredTables {
			activeTable := "active_" + table
			var exists bool
			err = conn.QueryRow(ctx, `
				select exists (
					select 1
					from pg_catalog.pg_class c
					join pg_catalog.pg_namespace n on n.oid = c.relnamespace
					where n.nspname = 'public' and c.relkind = 'r' and c.relname = $1
				)
			`, activeTable).Scan(&exists)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeTrue(),
				fmt.Sprintf(
					"object table %q (discovered via deletion_timestamp column) has no "+
						"corresponding active companion table %q — add it to migration 97",
					table, activeTable,
				))
		}
	})
})
