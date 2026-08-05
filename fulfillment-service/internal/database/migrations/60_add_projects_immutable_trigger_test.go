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

var _ = DescribeMigration("Add projects immutable trigger", func() {
	insertTenant := func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Adds an update trigger to the projects table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_immutable_columns' and
				event_object_table = 'projects' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents updating the name field", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into projects (id, name, tenant, creator, data)
			 values ('project-1', 'original-name', 'test-tenant', 'user1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update projects set name = 'new-name' where id = 'project-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
		Expect(pgErr.Message).To(ContainSubstring("column 'name'"))
		Expect(pgErr.Message).To(ContainSubstring("immutable"))
	})

	It("Prevents updating the tenant field", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into projects (id, name, tenant, creator, data)
			 values ('project-2', 'my-project', 'test-tenant', 'user1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update projects set tenant = 'other-tenant' where id = 'project-2'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
		Expect(pgErr.Message).To(ContainSubstring("column 'tenant'"))
		Expect(pgErr.Message).To(ContainSubstring("immutable"))
	})

	It("Prevents updating the creator field", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into projects (id, name, tenant, creator, data)
			 values ('project-3', 'my-project', 'test-tenant', 'original-user', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update projects set creator = 'attacker-user' where id = 'project-3'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
		Expect(pgErr.Message).To(ContainSubstring("column 'creator'"))
		Expect(pgErr.Message).To(ContainSubstring("immutable"))
	})

	It("Allows updating other fields", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into projects (id, name, tenant, creator, data)
			 values ('project-4', 'my-project', 'test-tenant', 'user1', '{"spec": {"title": "Old Title"}}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update projects set data = '{"spec": {"title": "New Title"}}'::jsonb where id = 'project-4'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows updating when immutable fields remain unchanged", func(ctx context.Context) {
		err := tool.Migrate(ctx, 60)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into projects (id, name, tenant, creator, data)
			 values ('project-5', 'my-project', 'test-tenant', 'user1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update projects
			 set name = 'my-project', tenant = 'test-tenant', creator = 'user1',
			     data = '{"spec": {"title": "Updated"}}'::jsonb
			 where id = 'project-5'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
