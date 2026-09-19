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

var _ = DescribeMigration("Add instance type catalog deletion protection", func() {
	It("Prevents soft-deleting an instance type referenced by a catalog item field definition", func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-cat-ref", "it-cat-ref", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Real serialized shape: field_definitions is a top-level array (not under spec), the path
		// is 'spec.instance_type', and the default is a bare string holding the instance type name.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-1", "test-tenant",
			`{"field_definitions":[{"path":"spec.instance_type","default":"it-cat-ref"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-cat-ref'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("it-cat-ref"))
	})

	It("Allows soft-deleting an instance type not referenced by any catalog item", func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-unused", "it-unused", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-unused'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting an instance type when a catalog item references a different instance type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ('it-a', 'it-a', 'shared', '{}'),
			       ('it-b', 'it-b', 'shared', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-b", "test-tenant",
			`{"field_definitions":[{"path":"spec.instance_type","default":"it-b"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// it-a is unreferenced, so deleting it is allowed.
		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-a'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Still prevents soft-deleting an instance type referenced by a compute instance", func(ctx context.Context) {
		// Regression guard: migration 113 re-declares the whole function, so the unchanged
		// compute_instances clause must keep firing.
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-in-use", "it-in-use", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instances (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"ci-1", "test-tenant", `{"spec":{"instance_type":{"id":"it-in-use"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-in-use'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("it-in-use"))
	})

	It("Still prevents soft-deleting an instance type referenced by a compute instance template", func(ctx context.Context) {
		// Regression guard for the unchanged compute_instance_templates clause.
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-tmpl-ref", "it-tmpl-ref", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_templates (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"tmpl-1", "test-tenant", `{"spec_defaults":{"instance_type":{"id":"it-tmpl-ref"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-tmpl-ref'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("it-tmpl-ref"))
	})

	It("Handles catalog items with null or missing field_definitions gracefully", func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-null-fd", "it-null-fd", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Catalog item with no field_definitions key at all.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-no-fd", "test-tenant", `{"spec":{}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Catalog item with field_definitions set to null.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-null-fd", "test-tenant", `{"field_definitions":null}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Deletion should succeed: no catalog item actually references this instance type.
		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-null-fd'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Ignores already-deleted catalog items when checking references", func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-deleted-cat", "it-deleted-cat", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Catalog item that references the instance type but is itself already soft-deleted.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data, deletion_timestamp)
			values ($1, $2, $3::jsonb, now())`,
			"cat-deleted", "test-tenant",
			`{"field_definitions":[{"path":"spec.instance_type","default":"it-deleted-cat"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Deletion should succeed: the referencing catalog item is already deleted.
		_, err = conn.Exec(ctx, `
			update instance_types
			set deletion_timestamp = now()
			where id = 'it-deleted-cat'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
