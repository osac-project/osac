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

var _ = DescribeMigration("Add projects path column", func() {
	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rewrites nested project names to leaf labels and populates path", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values
				('parent-id', 'my-tenant', '', 'parent', 'system', '{}'),
				('child-id', 'my-tenant', 'parent', 'parent.child', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		var parentName, parentPath, childName, childPath, childProject string
		err = conn.QueryRow(ctx,
			`select name::text, path::text from projects where id = 'parent-id'`,
		).Scan(&parentName, &parentPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(parentName).To(Equal("parent"))
		Expect(parentPath).To(Equal("parent"))

		err = conn.QueryRow(ctx,
			`select name::text, project::text, path::text from projects where id = 'child-id'`,
		).Scan(&childName, &childProject, &childPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(childName).To(Equal("child"))
		Expect(childProject).To(Equal("parent"))
		Expect(childPath).To(Equal("parent.child"))
	})

	It("Keeps the root project empty name and path", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		var name, path string
		err = conn.QueryRow(ctx, `
			select name::text, path::text
			from projects
			where tenant = 'my-tenant' and name = ''
		`).Scan(&name, &path)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(""))
		Expect(path).To(Equal(""))
	})

	It("Rejects duplicate leaf names under the same parent", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values ('a-id', 'my-tenant', '', 'dup', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values ('b-id', 'my-tenant', '', 'dup', 'system', '{}')
		`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("projects_pkey"))
	})

	It("Allows the same leaf name under different parents", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values
				('p1-id', 'my-tenant', '', 'p1', 'system', '{}'),
				('p2-id', 'my-tenant', '', 'p2', 'system', '{}'),
				('c1-id', 'my-tenant', 'p1', 'child', 'system', '{}'),
				('c2-id', 'my-tenant', 'p2', 'child', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		var path1, path2 string
		err = conn.QueryRow(ctx, `select path::text from projects where id = 'c1-id'`).Scan(&path1)
		Expect(err).ToNot(HaveOccurred())
		err = conn.QueryRow(ctx, `select path::text from projects where id = 'c2-id'`).Scan(&path2)
		Expect(err).ToNot(HaveOccurred())
		Expect(path1).To(Equal("p1.child"))
		Expect(path2).To(Equal("p2.child"))
	})

	It("Retargets resource project foreign keys to path", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values
				('parent-id', 'my-tenant', '', 'parent', 'system', '{}'),
				('child-id', 'my-tenant', 'parent', 'child', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into volumes (id, tenant, project, name, data)
			values ('volume-id', 'my-tenant', 'parent.child', 'my-volume', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into volumes (id, tenant, project, name, data)
			values ('bad-volume', 'my-tenant', 'missing.project', 'my-volume-2', '{}')
		`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.ConstraintName).To(Equal("volumes_project_fk"))
	})

	It("Converts the name column to text", func(ctx context.Context) {
		err := tool.Migrate(ctx, 97)
		Expect(err).ToNot(HaveOccurred())

		var nameType string
		err = conn.QueryRow(ctx, `
			select format_type(a.atttypid, a.atttypmod)
			from pg_attribute a
			where a.attrelid = 'projects'::regclass and a.attname = 'name'
		`).Scan(&nameType)
		Expect(err).ToNot(HaveOccurred())
		Expect(nameType).To(Equal("text"))
	})
})
