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

var _ = DescribeMigration("Add catalog item ref triggers", func() {
	// Cluster catalog item triggers:

	It("Creates the 'check_cluster_catalog_item_not_in_use' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_cluster_catalog_item_not_in_use' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the cluster_catalog_items table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_cluster_catalog_item_not_in_use' and
				event_object_table = 'cluster_catalog_items' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the index on clusters catalog_item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_indexes
			where
				tablename = 'clusters' and
				indexname = 'clusters_catalog_item'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents soft-deleting a cluster catalog item that is in use by a cluster", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing that catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cci-1"))
		Expect(pgErr.Message).To(ContainSubstring("cluster-1"))
	})

	It("Prevents soft-deleting a cluster catalog item when a cluster references it by name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item with a name:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, name, tenant, data)
			 values ('cci-byname-1', 'my-cci-name', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing the catalog item by name:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-byname-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"my-cci-name"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-byname-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cci-byname-1"))
		Expect(pgErr.Message).To(ContainSubstring("cluster-byname-1"))
	})

	It("Allows soft-deleting a cluster catalog item that is not in use", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item with no clusters:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-2', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-2'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a tenant-scoped cluster catalog item when only a different-tenant cluster references one with the same name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create two tenants:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-a', 'cci-tenant-a', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-b', 'cci-tenant-b', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item in tenant-a:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-tenant-1a', 'cci-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item in tenant-b:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-tenant-1b', 'cci-tenant-b', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster in tenant-b referencing its own catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-tenant-1', 'cci-tenant-b', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-tenant-1b"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the catalog item in tenant-a — should succeed because the
		// cluster in tenant-b references a different catalog item:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-tenant-1a'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents soft-deleting a global cluster catalog item when a cluster in any tenant references it", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-c', 'cci-tenant-c', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a shared cluster catalog item (tenant='shared'):
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-global-1', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster in tenant-c referencing the shared catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-global-1', 'cci-tenant-c', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-global-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the global catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-global-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cci-global-1"))
		Expect(pgErr.Message).To(ContainSubstring("cluster-global-1"))
	})

	It("Allows soft-deleting a cluster catalog item when the referencing cluster is already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a soft-deleted cluster referencing the catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, deletion_timestamp, data)
			 values ('cluster-2', 'test-tenant', now(), $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-3"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed since the cluster is already deleted:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-3'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the 'check_cluster_catalog_item_ref' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_cluster_catalog_item_ref' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the clusters table for catalog item ref on INSERT", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_cluster_catalog_item_ref_on_insert' and
				event_object_table = 'clusters' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the clusters table for catalog item ref on UPDATE", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_cluster_catalog_item_ref_on_update' and
				event_object_table = 'clusters' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents creating a cluster referencing a non-existent cluster catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a cluster referencing a catalog item that doesn't exist:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"no-such-cci"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-cci"))
	})

	It("Prevents creating a cluster referencing a soft-deleted cluster catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create and soft-delete a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a cluster referencing the deleted catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-deleted"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cci-deleted"))
	})

	It("Allows creating a cluster with no catalog item reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster with no catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-3', 'test-tenant', '{"spec":{}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a cluster with a valid catalog item reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-valid', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing the valid catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-4', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-valid"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a cluster that references a global cluster catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-d', 'cci-tenant-d', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a shared cluster catalog item (tenant='shared'):
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-global-ref-1', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster in tenant-d referencing the shared catalog item — should succeed:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-global-ref-1', 'cci-tenant-d', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-global-ref-1"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents creating a cluster that references a cluster catalog item in a different tenant", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create two tenants:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-e', 'cci-tenant-e', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cci-tenant-f', 'cci-tenant-f', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item in tenant-e:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-other-tenant-1', 'cci-tenant-e', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a cluster in tenant-f referencing the catalog item in tenant-e — should fail:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-other-tenant-1', 'cci-tenant-f', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-other-tenant-1"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cci-other-tenant-1"))
	})

	It("Prevents updating a cluster to reference a non-existent cluster catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-upd-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing the valid catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-upd-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-upd-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the cluster to reference a non-existent catalog item:
		_, err = conn.Exec(ctx,
			`update clusters set data = $1::jsonb where id = 'cluster-upd-1'`,
			`{"spec":{"catalog_item":"no-such-cci"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-cci"))
	})

	It("Prevents updating a cluster to reference a soft-deleted cluster catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create two cluster catalog items:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-upd-2a', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-upd-2b', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing the first catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-upd-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-upd-2a"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the second catalog item:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-upd-2b'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the cluster to reference the soft-deleted catalog item:
		_, err = conn.Exec(ctx,
			`update clusters set data = $1::jsonb where id = 'cluster-upd-2'`,
			`{"spec":{"catalog_item":"cci-upd-2b"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cci-upd-2b"))
	})

	It("Prevents updating a cluster when its referenced catalog item has been soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-upd-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing the catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-upd-3', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-upd-3"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the catalog item (must first remove the reference to avoid parent trigger):
		_, err = conn.Exec(ctx,
			`update clusters set data = '{"spec":{}}'::jsonb where id = 'cluster-upd-3'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-upd-3'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the cluster back to reference the now-deleted catalog item:
		_, err = conn.Exec(ctx,
			`update clusters set data = $1::jsonb where id = 'cluster-upd-3'`,
			`{"spec":{"catalog_item":"cci-upd-3"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cci-upd-3"))
	})

	// Compute instance catalog item triggers:

	It("Creates the 'check_ci_catalog_item_not_in_use' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_ci_catalog_item_not_in_use' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the compute_instance_catalog_items table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_ci_catalog_item_not_in_use' and
				event_object_table = 'compute_instance_catalog_items' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the index on compute_instances catalog_item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_indexes
			where
				tablename = 'compute_instances' and
				indexname = 'compute_instances_catalog_item'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents soft-deleting a compute instance catalog item that is in use by a compute instance", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing that catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cici-1"))
		Expect(pgErr.Message).To(ContainSubstring("ci-1"))
	})

	It("Prevents soft-deleting a compute instance catalog item when a compute instance references it by name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item with a name:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, name, tenant, data)
			 values ('cici-byname-1', 'my-cici-name', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing the catalog item by name:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-byname-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"my-cici-name"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-byname-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cici-byname-1"))
		Expect(pgErr.Message).To(ContainSubstring("ci-byname-1"))
	})

	It("Allows soft-deleting a compute instance catalog item that is not in use", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item with no compute instances:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-2', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-2'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a tenant-scoped compute instance catalog item when only a different-tenant compute instance references one with the same name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create two tenants:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-a', 'cici-tenant-a', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-b', 'cici-tenant-b', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item in tenant-a:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-tenant-1a', 'cici-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item in tenant-b:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-tenant-1b', 'cici-tenant-b', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance in tenant-b referencing its own catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-tenant-1', 'cici-tenant-b', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-tenant-1b"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the catalog item in tenant-a — should succeed because the
		// compute instance in tenant-b references a different catalog item:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-tenant-1a'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents soft-deleting a global compute instance catalog item when a compute instance in any tenant references it", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-c', 'cici-tenant-c', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a shared compute instance catalog item (tenant='shared'):
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-global-1', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance in tenant-c referencing the shared catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-global-1', 'cici-tenant-c', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-global-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the global catalog item — should fail:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-global-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cici-global-1"))
		Expect(pgErr.Message).To(ContainSubstring("ci-global-1"))
	})

	It("Allows soft-deleting a compute instance catalog item when the referencing compute instance is already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a soft-deleted compute instance referencing the catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, deletion_timestamp, data)
			 values ('ci-2', 'test-tenant', now(), $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-3"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed since the compute instance is already deleted:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-3'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the 'check_ci_catalog_item_ref' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_ci_catalog_item_ref' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the compute_instances table for catalog item ref on INSERT", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_ci_catalog_item_ref_on_insert' and
				event_object_table = 'compute_instances' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the compute_instances table for catalog item ref on UPDATE", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_ci_catalog_item_ref_on_update' and
				event_object_table = 'compute_instances' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents creating a compute instance referencing a non-existent compute instance catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing a catalog item that doesn't exist:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"no-such-cici"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-cici"))
	})

	It("Prevents creating a compute instance referencing a soft-deleted compute instance catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create and soft-delete a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing the deleted catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-deleted"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cici-deleted"))
	})

	It("Allows creating a compute instance with no catalog item reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance with no catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-3', 'test-tenant', '{"spec":{}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a compute instance with a valid catalog item reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-valid', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing the valid catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-4', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-valid"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a compute instance that references a global compute instance catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-d', 'cici-tenant-d', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a shared compute instance catalog item (tenant='shared'):
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-global-ref-1', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance in tenant-d referencing the shared catalog item — should succeed:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-global-ref-1', 'cici-tenant-d', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-global-ref-1"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents creating a compute instance that references a compute instance catalog item in a different tenant", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create two tenants:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-e', 'cici-tenant-e', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('cici-tenant-f', 'cici-tenant-f', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item in tenant-e:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-other-tenant-1', 'cici-tenant-e', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance in tenant-f referencing the catalog item in tenant-e — should fail:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-other-tenant-1', 'cici-tenant-f', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-other-tenant-1"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cici-other-tenant-1"))
	})

	It("Prevents updating a compute instance to reference a non-existent compute instance catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-upd-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing the valid catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-upd-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-upd-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the compute instance to reference a non-existent catalog item:
		_, err = conn.Exec(ctx,
			`update compute_instances set data = $1::jsonb where id = 'ci-upd-1'`,
			`{"spec":{"catalog_item":"no-such-cici"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-cici"))
	})

	It("Prevents updating a compute instance to reference a soft-deleted compute instance catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create two compute instance catalog items:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-upd-2a', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-upd-2b', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing the first catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-upd-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-upd-2a"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the second catalog item:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-upd-2b'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the compute instance to reference the soft-deleted catalog item:
		_, err = conn.Exec(ctx,
			`update compute_instances set data = $1::jsonb where id = 'ci-upd-2'`,
			`{"spec":{"catalog_item":"cici-upd-2b"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cici-upd-2b"))
	})

	It("Prevents updating a compute instance when its referenced catalog item has been soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 59)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-upd-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing the catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-upd-3', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-upd-3"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete the catalog item (must first remove the reference to avoid parent trigger):
		_, err = conn.Exec(ctx,
			`update compute_instances set data = '{"spec":{}}'::jsonb where id = 'ci-upd-3'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-upd-3'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the compute instance back to reference the now-deleted catalog item:
		_, err = conn.Exec(ctx,
			`update compute_instances set data = $1::jsonb where id = 'ci-upd-3'`,
			`{"spec":{"catalog_item":"cici-upd-3"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("cici-upd-3"))
	})
})
