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
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create volumes tables", func() {
	DescribeTable(
		"Creates the expected tables",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 94)
			Expect(err).ToNot(HaveOccurred())

			quotedTable := pgx.Identifier{table}.Sanitize()

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"test-id", "system", `{}`,
			)
			Expect(err).ToNot(HaveOccurred())

			var count int
			err = conn.QueryRow(ctx,
				fmt.Sprintf(`select count(*) from %s where id = $1`, quotedTable),
				"test-id",
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1))
		},
		Entry("volumes", "volumes"),
	)

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, tenant, data) values ($1, $2, $3)`,
			"bad-tenant-id", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces name uniqueness per tenant", func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-1", "my-volume", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-2", "my-volume", "system", `{}`,
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	It("Allows same name in different tenants", func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, data) values ($1, $2, $3)`,
			"tenant-2", "tenant-2", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-3", "shared-name", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-4", "shared-name", "tenant-2", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows same name after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-5", "reusable-name", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update volumes set deletion_timestamp = now() where id = $1`,
			"id-5",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"id-6", "reusable-name", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Enforces immutability of id, name, tenant, and project", func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into volumes (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"immutable-id", "immutable-vol", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update volumes set id = $1 where id = $2`,
			"changed-id", "immutable-id",
		)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update volumes set name = $1 where id = $2`,
			"changed-name", "immutable-id",
		)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update volumes set tenant = $1 where id = $2`,
			"other-tenant", "immutable-id",
		)
		Expect(err).To(HaveOccurred())
	})
})
