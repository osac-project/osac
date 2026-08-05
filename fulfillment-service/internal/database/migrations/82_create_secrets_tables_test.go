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

var _ = DescribeMigration("Create secrets tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 82)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the secrets table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, tenant, data)
			values ($1, $2, $3)`,
			"test-id", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from secrets
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the archived_secrets table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_secrets
				(id, tenant, creation_timestamp, deletion_timestamp, data)
			values ($1, $2, now(), now(), $3)`,
			"test-id", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from archived_secrets
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, tenant, data)
			values ($1, $2, $3)`,
			"bad-tenant-id", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects invalid project reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, tenant, project, data)
			values ($1, $2, $3, $4)`,
			"bad-project-id", "system", "no-such-project", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces immutability of tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, tenant, data)
			values ($1, $2, $3)`,
			"imm-id", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update secrets
			set tenant = $1
			where id = $2`,
			"other-tenant", "imm-id",
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
	})
})
