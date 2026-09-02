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

var _ = DescribeMigration("Create add-on operators tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the add_on_operators table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-1", "gpu-operator", "shared", `{"title":"GPU Operator"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `
			select name
			from add_on_operators
			where id = $1`,
			"ao-1",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("gpu-operator"))
	})

	It("Creates the archived_add_on_operators table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_add_on_operators
				(id, tenant, creation_timestamp, deletion_timestamp, data)
			values ($1, $2, now(), now(), $3)`,
			"ao-archived", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from archived_add_on_operators
			where id = $1`,
			"ao-archived",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Enforces unique name per tenant for active records", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-1", "gpu-operator", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-2", "gpu-operator", "shared", `{}`,
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
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-a", "gpu-operator", "tenant-a", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-b", "gpu-operator", "tenant-b", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows name reuse after soft-delete", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-old", "gpu-operator", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update add_on_operators
			set deletion_timestamp = now()
			where id = 'ao-old'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-new", "gpu-operator", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-bad-tenant", "test-operator", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces immutability of tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-imm", "imm-operator", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update add_on_operators
			set tenant = $1
			where id = $2`,
			"other-tenant", "ao-imm",
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
	})

	It("Allows soft-deleting an add-on operator that is not referenced", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"ao-unused", "unused-operator", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update add_on_operators
			set deletion_timestamp = now()
			where id = 'ao-unused'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
