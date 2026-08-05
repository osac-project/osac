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
	"fmt"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create storage backends tables", func() {
	It("Creates the storage_backends table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 62)
		Expect(err).ToNot(HaveOccurred())

		table := pgx.Identifier{"storage_backends"}.Sanitize()

		_, err = conn.Exec(ctx,
			fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, table),
			"test-id", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx,
			fmt.Sprintf(`select count(*) from %s where id = $1`, table),
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 62)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_backends (id, tenant, data) values ($1, $2, $3)`,
			"bad-tenant-id", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces name uniqueness", func(ctx context.Context) {
		err := tool.Migrate(ctx, 62)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-1", "my-backend", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-2", "my-backend", "system", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces immutability of id, name, and tenant", func(ctx context.Context) {
		err := tool.Migrate(ctx, 62)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"immutable-id", "immutable-name", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update storage_backends set name = $1 where id = $2`,
			"changed-name", "immutable-id",
		)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update storage_backends set tenant = $1 where id = $2`,
			"other-tenant", "immutable-id",
		)
		Expect(err).To(HaveOccurred())
	})
})
