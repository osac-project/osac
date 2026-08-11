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

var _ = DescribeMigration("Create disk images tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 99)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the disk_images table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-1", "fedora-41", "shared", `{"spec":{"source_type":1,"source_ref":"quay.io/containerdisks/fedora:41"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `
			select name
			from disk_images
			where id = $1`,
			"di-1",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("fedora-41"))
	})

	It("Creates the archived_disk_images table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_disk_images
				(id, tenant, creation_timestamp, deletion_timestamp, data)
			values ($1, $2, now(), now(), $3)`,
			"di-archived", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from archived_disk_images
			where id = $1`,
			"di-archived",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Enforces unique name per tenant for active records", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-1", "fedora-41", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-2", "fedora-41", "shared", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Allows same name in different tenants", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant-b', 'tenant-b', 'tenant-b', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-a", "fedora-41", "tenant-a", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-b", "fedora-41", "tenant-b", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows name reuse after soft-delete", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-old", "fedora-41", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-old'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-new", "fedora-41", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-bad-tenant", "test-image", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces immutability of tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-imm", "imm-image", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set tenant = $1
			where id = $2`,
			"other-tenant", "di-imm",
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
	})

	It("Prevents soft-deleting a disk image referenced by a compute instance", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-in-use", "in-use-image", "test-tenant", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instances (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"ci-1", "test-tenant", `{"spec":{"disk_image":{"id":"di-in-use"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-in-use'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("di-in-use"))
	})

	It("Prevents soft-deleting a disk image referenced by a compute instance template", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-tmpl-ref", "tmpl-ref-image", "test-tenant", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_templates (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"tmpl-1", "test-tenant", `{"spec_defaults":{"disk_image":{"id":"di-tmpl-ref"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-tmpl-ref'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("di-tmpl-ref"))
	})

	It("Prevents soft-deleting a disk image referenced by a catalog item", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-cat-ref", "cat-ref-image", "test-tenant", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-1", "test-tenant",
			`{"spec":{"field_definitions":[{"path":"spec.disk_image","default_value":"di-cat-ref"}]}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-cat-ref'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("di-cat-ref"))
	})

	It("Allows soft-deleting a disk image that is not referenced", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-unused", "unused-image", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-unused'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a disk image when the referencing compute instance is already deleted", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-deleted-ref", "deleted-ref-image", "test-tenant", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instances (id, tenant, deletion_timestamp, data)
			values ($1, $2, now(), $3::jsonb)`,
			"ci-deleted", "test-tenant", `{"spec":{"disk_image":{"id":"di-deleted-ref"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-deleted-ref'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
