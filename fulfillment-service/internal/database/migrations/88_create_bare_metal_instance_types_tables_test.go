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

var _ = DescribeMigration("Create bare metal instance types tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 88)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the bare_metal_instance_types table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"test-id", "gpu-large", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from bare_metal_instance_types
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the archived_bare_metal_instance_types table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_bare_metal_instance_types
				(id, tenant, creation_timestamp, deletion_timestamp, data)
			values ($1, $2, now(), now(), $3)`,
			"test-id", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from archived_bare_metal_instance_types
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Rejects invalid tenant reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"bad-tenant-id", "test-type", "no-such-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects invalid project reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, project, data)
			values ($1, $2, $3, $4, $5)`,
			"bad-project-id", "test-project-type", "shared", "no-such-project", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects empty name", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"empty-name-id", "", "shared", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects omitted name", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, tenant, data)
			values ($1, $2, $3)`,
			"no-name-id", "shared", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces name uniqueness", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"id-1", "gpu-large", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"id-2", "gpu-large", "shared", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Enforces immutability of tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"imm-id", "imm-type", "shared", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update bare_metal_instance_types
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
