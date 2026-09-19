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

var _ = DescribeMigration("Create SSH key tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 116)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}'),
			       ('tenant-b', 'tenant-b', 'tenant-b', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertKey := func(ctx context.Context, id, name, tenant string) {
		_, err := conn.Exec(ctx,
			`insert into ssh_keys (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			id, name, tenant, `{"spec":{"public_key":"fixture"}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	It("creates active and archived SSH key tables", func(ctx context.Context) {
		insertKey(ctx, "key-active", "my-key", "tenant-a")

		var activeCount int
		err := conn.QueryRow(ctx, `select count(*) from active_ssh_keys where id = $1`, "key-active").Scan(&activeCount)
		Expect(err).ToNot(HaveOccurred())
		Expect(activeCount).To(Equal(1))

		_, err = conn.Exec(ctx, `
			insert into archived_ssh_keys
				(id, name, creation_timestamp, deletion_timestamp, tenant, data)
			values ($1, $2, now(), now(), $3, $4::jsonb)`,
			"key-archived", "archived-key", "tenant-a", `{}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enforces tenant-wide uniqueness, including soft-deleted rows", func(ctx context.Context) {
		insertKey(ctx, "key-old", "same-name", "tenant-a")
		_, err := conn.Exec(ctx, `update ssh_keys set deletion_timestamp = now() where id = $1`, "key-old")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into ssh_keys (id, name, tenant, data) values ($1, $2, $3, '{}'::jsonb)`,
			"key-new", "same-name", "tenant-a")
		Expect(err).To(HaveOccurred())

		insertKey(ctx, "key-other-tenant", "same-name", "tenant-b")
	})

	It("rejects a missing tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into ssh_keys (id, name, tenant, data) values ($1, $2, $3, '{}'::jsonb)`,
			"key-invalid-tenant", "my-key", "missing-tenant")
		Expect(err).To(HaveOccurred())
	})

	It("enforces immutable metadata and tenant-only project scope", func(ctx context.Context) {
		insertKey(ctx, "key-immutable", "immutable-key", "tenant-a")

		_, err := conn.Exec(ctx, `update ssh_keys set name = $1 where id = $2`, "renamed", "key-immutable")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))

		_, err = conn.Exec(ctx, `
			insert into ssh_keys (id, name, tenant, project, data)
			values ($1, $2, $3, 'project-a'::ltree, '{}'::jsonb)`,
			"key-project", "project-key", "tenant-a")
		Expect(err).To(HaveOccurred())
	})

	It("removes soft-deleted keys from the active companion table", func(ctx context.Context) {
		insertKey(ctx, "key-delete", "delete-key", "tenant-a")

		_, err := conn.Exec(ctx, `update ssh_keys set deletion_timestamp = now() where id = $1`, "key-delete")
		Expect(err).ToNot(HaveOccurred())

		var activeCount int
		err = conn.QueryRow(ctx, `select count(*) from active_ssh_keys where id = $1`, "key-delete").Scan(&activeCount)
		Expect(err).ToNot(HaveOccurred())
		Expect(activeCount).To(Equal(0))
	})
})
