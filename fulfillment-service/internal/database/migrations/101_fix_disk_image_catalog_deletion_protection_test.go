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
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

// The DescribeMigration harness builds the database at the previous migration (100) before each
// test. Tests that exercise the replaced trigger migrate to 101 first, then insert catalog items
// already in the settled shape. The data-normalization test instead inserts a legacy-shaped row at
// version 100 and migrates to 101 afterwards, so it exercises the migration's field_definitions
// reshape.
var _ = DescribeMigration("Fix disk image catalog deletion protection", func() {
	It("Prevents soft-deleting a disk image referenced by a catalog item in the settled shape", func(ctx context.Context) {
		err := tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
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

		// Real serialized shape: field_definitions is a top-level array (not under spec), the path is
		// the prefix-less 'disk_image', and the default is an id-keyed {"id": ..., "name": ...}
		// reference object (what the server persists after resolving the name; see
		// validateFieldDefinitionsDiskImage). The trigger matches on the id.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-1", "test-tenant",
			`{"field_definitions":[{"path":"disk_image","default":{"id":"di-cat-ref","name":"cat-ref-image"}}]}`,
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

	It("Normalizes a legacy string-default catalog item and then protects the disk image", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"di-legacy", "legacy-image", "test-tenant", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Legacy shape as written before this migration: top-level field_definitions, prefixed
		// 'spec.disk_image' path, and a bare-string default holding the disk image name.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-legacy", "test-tenant",
			`{"field_definitions":[{"path":"spec.disk_image","default":"legacy-image"},{"path":"pull_secret","default":"secret"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		// The migration must have reshaped the disk_image field_definition to path 'disk_image' with an
		// id-keyed {"id": ..., "name": ...} object default (the name resolved to di-legacy's id via the
		// catalog item's own tenant), while leaving the unrelated element untouched.
		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_catalog_items where id = $1`, "cat-legacy",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		fieldDefs := parsed["field_definitions"].([]interface{})
		Expect(fieldDefs).To(HaveLen(2))

		diskImageFd := fieldDefs[0].(map[string]interface{})
		Expect(diskImageFd["path"]).To(Equal("disk_image"))
		diskImageDefault := diskImageFd["default"].(map[string]interface{})
		Expect(diskImageDefault["id"]).To(Equal("di-legacy"))
		Expect(diskImageDefault["name"]).To(Equal("legacy-image"))

		pullSecretFd := fieldDefs[1].(map[string]interface{})
		Expect(pullSecretFd["path"]).To(Equal("pull_secret"))
		Expect(pullSecretFd["default"]).To(Equal("secret"))

		// And the normalized catalog item now triggers deletion protection.
		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-legacy'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("di-legacy"))
	})

	It("Allows soft-deleting a disk image referenced by a catalog item pointing at a different image", func(ctx context.Context) {
		err := tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('di-a', 'image-a', 'test-tenant', '{}'),
			       ('di-b', 'image-b', 'test-tenant', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-b", "test-tenant",
			`{"field_definitions":[{"path":"disk_image","default":{"id":"di-b","name":"image-b"}}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// image-a is unreferenced, so deleting it is allowed even though a catalog item references image-b.
		_, err = conn.Exec(ctx, `
			update disk_images
			set deletion_timestamp = now()
			where id = 'di-a'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a disk image that is not referenced", func(ctx context.Context) {
		err := tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
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

	It("Still prevents soft-deleting a disk image referenced by a compute instance", func(ctx context.Context) {
		// Regression guard: migration 101 re-declares the whole function, so the unchanged
		// compute_instances clause (matched by id) must keep firing.
		err := tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
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

	It("Still prevents soft-deleting a disk image referenced by a compute instance template", func(ctx context.Context) {
		// Regression guard for the unchanged compute_instance_templates clause (matched by id).
		err := tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
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

	It("Backfills the catalog default to the own-tenant image id when a same-name shared image also exists", func(ctx context.Context) {
		// A shared "fedora" and a tenant "fedora" coexist (names are unique only per tenant). The
		// backfill must resolve the catalog item's name-only default to the item's OWN tenant image,
		// mirroring the server's own-tenant-then-shared precedence.
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('di-fedora-shared', 'fedora', 'shared', '{}'),
			       ('di-fedora-tenant', 'fedora', 'test-tenant', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Legacy name-only default authored by the tenant.
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-fedora", "test-tenant",
			`{"field_definitions":[{"path":"disk_image","default":{"name":"fedora"}}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_catalog_items where id = $1`, "cat-fedora",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		diskImageDefault := parsed["field_definitions"].([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
		Expect(diskImageDefault["id"]).To(Equal("di-fedora-tenant"))
		Expect(diskImageDefault["name"]).To(Equal("fedora"))

		// The shared same-name sibling is NOT over-blocked: it can be soft-deleted.
		_, err = conn.Exec(ctx, `
			update disk_images set deletion_timestamp = now() where id = 'di-fedora-shared'`)
		Expect(err).ToNot(HaveOccurred())

		// The resolved own-tenant image is protected.
		_, err = conn.Exec(ctx, `
			update disk_images set deletion_timestamp = now() where id = 'di-fedora-tenant'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("di-fedora-tenant"))
	})

	It("Backfills the catalog default to the shared image id when the tenant has no same-name image", func(ctx context.Context) {
		// Only a shared "ubuntu" exists; the tenant's name-only default must fall back to the shared id.
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('di-ubuntu-shared', 'ubuntu', 'shared', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-ubuntu", "test-tenant",
			`{"field_definitions":[{"path":"disk_image","default":{"name":"ubuntu"}}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_catalog_items where id = $1`, "cat-ubuntu",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		diskImageDefault := parsed["field_definitions"].([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
		Expect(diskImageDefault["id"]).To(Equal("di-ubuntu-shared"))
		Expect(diskImageDefault["name"]).To(Equal("ubuntu"))
	})

	It("Leaves the catalog default name-only when the name resolves to no disk image", func(ctx context.Context) {
		// The referenced image no longer exists: the backfill normalizes the path but leaves the
		// default name-only (no id), best-effort, rather than dropping it.
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"cat-ghost", "test-tenant",
			`{"field_definitions":[{"path":"spec.disk_image","default":"ghost-image"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 101)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_catalog_items where id = $1`, "cat-ghost",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		diskImageFd := parsed["field_definitions"].([]interface{})[0].(map[string]interface{})
		Expect(diskImageFd["path"]).To(Equal("disk_image"))
		diskImageDefault := diskImageFd["default"].(map[string]interface{})
		Expect(diskImageDefault).ToNot(HaveKey("id"))
		Expect(diskImageDefault["name"]).To(Equal("ghost-image"))
	})
})
