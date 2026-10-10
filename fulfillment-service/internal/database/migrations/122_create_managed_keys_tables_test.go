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

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create managed key tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 122)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}'),
			       ('tenant-b', 'tenant-b', 'tenant-b', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team', 'tenant-a', 'team', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("persists tenant and provider identities with generation metadata", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into managed_keys (id, name, tenant, project, data)
			values ('tenant-key', 'data-key', 'tenant-a', 'team',
			        '{"usage":"MANAGED_KEY_USAGE_ENCRYPT_DECRYPT","versions":[{"generation":"1"}]}'),
			       ('provider-key', 'data-key', 'system', '', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		var tenant, project, generation string
		err = conn.QueryRow(ctx, `
			select tenant, project::text, data->'versions'->0->>'generation'
			from managed_keys where id = 'tenant-key'`).Scan(&tenant, &project, &generation)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant).To(Equal("tenant-a"))
		Expect(project).To(Equal("team"))
		Expect(generation).To(Equal("1"))

		var count int
		err = conn.QueryRow(ctx, `select count(*) from active_managed_keys`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))
	})

	DescribeTable("rejects invalid ownership and names",
		func(ctx context.Context, tenant, project, name, code string) {
			_, err := conn.Exec(ctx, `
				insert into managed_keys (id, name, tenant, project, data)
				values ('key', $1, $2, $3, '{}')`, name, tenant, project)
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue())
			Expect(pgErr.Code).To(Equal(code))
		},
		Entry("shared tenant", "shared", "", "data-key", "23514"),
		Entry("unknown tenant", "missing", "", "data-key", "23503"),
		Entry("unknown project", "tenant-a", "missing", "data-key", "23503"),
		Entry("another tenant's project", "tenant-b", "team", "data-key", "23503"),
		Entry("empty name", "tenant-a", "", "", "23514"),
	)

	It("scopes unique names to tenant and project, including soft-deleted rows", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into managed_keys (id, name, tenant, project, data)
			values ('key-a', 'data-key', 'tenant-a', '', '{}'),
			       ('key-b', 'data-key', 'tenant-b', '', '{}'),
			       ('key-team', 'data-key', 'tenant-a', 'team', '{}');
			update managed_keys set deletion_timestamp = now() where id = 'key-a'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into managed_keys (id, name, tenant, data)
			values ('duplicate', 'data-key', 'tenant-a', '{}')`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	DescribeTable("keeps identity and ownership immutable",
		func(ctx context.Context, column, value string) {
			_, err := conn.Exec(ctx, `
				insert into managed_keys (id, name, tenant, data)
				values ('key', 'data-key', 'tenant-a', '{}')`)
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, fmt.Sprintf(`update managed_keys set %s = $1 where id = 'key'`, column), value)
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue())
			Expect(pgErr.Code).To(Equal("Z0001"))
		},
		Entry("id", "id", "other-key"),
		Entry("name", "name", "other-name"),
		Entry("tenant", "tenant", "tenant-b"),
		Entry("project", "project", "team"),
	)

	It("removes active membership on soft deletion and cascades on physical deletion", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into managed_keys (id, name, tenant, data)
			values ('soft-key', 'soft-key', 'tenant-a', '{}'),
			       ('physical-key', 'physical-key', 'tenant-a', '{}');
			update managed_keys set deletion_timestamp = now() where id = 'soft-key';
			delete from managed_keys where id = 'physical-key'`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `select count(*) from active_managed_keys`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
		err = conn.QueryRow(ctx, `select count(*) from managed_keys where id = 'soft-key'`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("stores archived identity and generation metadata", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_managed_keys
			  (id, name, tenant, project, creation_timestamp, deletion_timestamp, data)
			values ('key', 'data-key', 'tenant-a', 'team', now(), now(), '{"versions":[{"generation":"1"}]}')`)
		Expect(err).ToNot(HaveOccurred())

		var generation string
		err = conn.QueryRow(ctx, `
			select data->'versions'->0->>'generation' from archived_managed_keys where id = 'key'`).Scan(&generation)
		Expect(err).ToNot(HaveOccurred())
		Expect(generation).To(Equal("1"))
	})

	It("enqueues created, updated, signaled, and deleted rows", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into managed_keys (id, name, tenant, data)
			values ('key', 'data-key', 'tenant-a', '{}');
			update managed_keys set data = '{"usage":"MANAGED_KEY_USAGE_ENCRYPT_DECRYPT"}' where id = 'key'`)
		Expect(err).ToNot(HaveOccurred())

		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { _ = tx.Rollback(ctx) })
		_, err = tx.Exec(ctx, `set local osac.signal = 'on'; update managed_keys set data = data where id = 'key'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.Commit(ctx)).To(Succeed())
		_, err = conn.Exec(ctx, `delete from managed_keys where id = 'key'`)
		Expect(err).ToNot(HaveOccurred())

		var operations []string
		err = conn.QueryRow(ctx, `
			select array_agg(op order by id) from changes where "table" = 'managed_keys' and data->>'id' = 'key'`).Scan(&operations)
		Expect(err).ToNot(HaveOccurred())
		Expect(operations).To(Equal([]string{"INSERT", "UPDATE", "SIGNAL", "DELETE"}))
	})
})
