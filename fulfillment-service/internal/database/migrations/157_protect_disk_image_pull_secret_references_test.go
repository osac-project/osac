/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Protect DiskImage pull_secret Secret references", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 157)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertSecret := func(ctx context.Context, id, backend string) {
		_, err := conn.Exec(ctx,
			`insert into secrets (id, name, tenant, data) values ($1, $1, 'test-tenant', $2::jsonb)`,
			id, `{"backend":"`+backend+`"}`)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteSecret := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx, `update secrets set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectInUse := func(err error) {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
	}

	It("creates the disk_images_pull_secret index", func(ctx context.Context) {
		var exists bool
		err := conn.QueryRow(ctx, `
			select exists (
				select 1
				from pg_indexes
				where schemaname = current_schema()
				  and indexname = 'disk_images_pull_secret'
			)`).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("rejects deletion when an active DiskImage references the Secret", func(ctx context.Context) {
		insertSecret(ctx, "di-pull-secret", "SECRET_BACKEND_VAULT")
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('dependent-disk-image', 'dependent-disk-image', 'test-tenant',
			        '{"spec":{"pull_secret":{"id":"di-pull-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		expectInUse(deleteSecret(ctx, "di-pull-secret"))
	})

	It("rejects DiskImage references to missing or deleted Secrets", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('missing-secret-image', 'missing-secret-image', 'test-tenant',
			        '{"spec":{"pull_secret":{"id":"missing-secret"}}}')`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))

		insertSecret(ctx, "deleted-di-secret", "SECRET_BACKEND_VAULT")
		Expect(deleteSecret(ctx, "deleted-di-secret")).To(Succeed())
		_, err = conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('deleted-secret-image', 'deleted-secret-image', 'test-tenant',
			        '{"spec":{"pull_secret":{"id":"deleted-di-secret"}}}')`)
		Expect(err).To(HaveOccurred())
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
	})

	It("allows deletion when its sole DiskImage dependent is soft-deleted", func(ctx context.Context) {
		insertSecret(ctx, "di-pull-secret", "SECRET_BACKEND_VAULT")
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('dependent-disk-image', 'dependent-disk-image', 'test-tenant',
			        '{"spec":{"pull_secret":{"id":"di-pull-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update disk_images set deletion_timestamp = now() where id = 'dependent-disk-image'`)
		Expect(err).ToNot(HaveOccurred())

		Expect(deleteSecret(ctx, "di-pull-secret")).ToNot(HaveOccurred())
	})

	It("rejects deletion of a HUB-backed Secret referenced by an active DiskImage", func(ctx context.Context) {
		insertSecret(ctx, "di-hub-secret", "SECRET_BACKEND_HUB")
		_, err := conn.Exec(ctx, `
			insert into disk_images (id, name, tenant, data)
			values ('hub-secret-image', 'hub-secret-image', 'test-tenant',
			        '{"spec":{"pull_secret":{"id":"di-hub-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(deleteSecret(ctx, "di-hub-secret"))
	})

	It("rejects deletion when an active catalog policy references the Secret", func(ctx context.Context) {
		insertSecret(ctx, "catalog-pull-secret", "SECRET_BACKEND_HUB")
		_, err := conn.Exec(ctx, `
			insert into cluster_catalog_items (id, name, tenant, data)
			values (
				'catalog-pull-policy',
				'catalog-pull-policy',
				'test-tenant',
				'{"fields":{"pull_secret_secret":{"locked":{"id":"catalog-pull-secret"}}}}'
			)`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(deleteSecret(ctx, "catalog-pull-secret"))
	})
})
