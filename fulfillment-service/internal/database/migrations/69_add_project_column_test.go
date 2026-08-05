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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add project column", func() {
	BeforeEach(func(ctx context.Context) {
		// Create a tenant, as otherwise projects can't be created:
		_, err := conn.Exec(ctx, `
			insert into tenants (
				id,
				tenant,
				name,
				data
			)
			values (
				'my-tenant',
				'my-tenant',
				'my-tenant',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects duplicate tenant and name pairs in the projects table", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Create a project:
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'my-project',
				'my-project',
				'{}'
			)
		`)

		// Try to create another project, with the same name but a different identifier, and verify that it
		// fails:
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'456',
				'my-tenant',
				'my-project',
				'my-project',
				'{}'
			)
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("projects_pkey"))
	})

	It("Rejects rows in with a non-existent project", func(ctx context.Context) {
		// Runt the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Try to create an objec with a project that doesn't exist:
		_, err = conn.Exec(ctx, `
			insert into clusters (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'no.such.project',
				'my-cluster',
				'{}'
			)
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("project_fk"))
	})

	It("Rejects updates to immutable columns of the projects table", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Create a project:
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'my-project',
				'my-project',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the tenant column:
		_, err = conn.Exec(ctx, `
			update projects set tenant = 'your-tenant' where id = '123'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))

		// Try to update the project column:
		_, err = conn.Exec(ctx, `
			update projects set project = 'your-project' where id = '123'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))

		// Try to update the name column:
		_, err = conn.Exec(ctx, `
			update projects set name = 'your-project' where id = '123'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("Rejects updates to immutable columns of object tables", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Create a object in the default project::
		_, err = conn.Exec(ctx, `
			insert into clusters (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'',
				'my-cluster',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())

		// Try to update the tenant column:
		_, err = conn.Exec(ctx, `
			update clusters set tenant = 'your-tenant' where id = '123'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))

		// Try to update the project column:
		_, err = conn.Exec(ctx, `
			update clusters set project = 'your-project' where id = '123'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("Automatically creates the empty project for a tenant", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Check that the empty project was created:
		var count int
		row := conn.QueryRow(ctx, `
			select count(*) from projects where tenant = 'my-tenant' and name = ''
		`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically("==", 1))
	})

	It("Rejects creation of a project with a non-existent parent project", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Create a project:
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'my-parent',
				'my-project',
				'{}'
			)
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parent_project_fk"))
	})

	It("Rejects deletion of a project that has children", func(ctx context.Context) {
		// Run the migrations:
		err := tool.Migrate(ctx, 69)
		Expect(err).ToNot(HaveOccurred())

		// Create a parent project:
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'123',
				'my-tenant',
				'',
				'my-parent',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())

		// Create a child project:
		_, err = conn.Exec(ctx, `
			insert into projects (
				id,
				tenant,
				project,
				name,
				data
			)
			values (
				'456',
				'my-tenant',
				'my-parent',
				'my-child',
				'{}'
			)
		`)
		Expect(err).ToNot(HaveOccurred())

		// Try to delete the parent project:
		_, err = conn.Exec(ctx, `
			delete from projects where name = 'my-parent'
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parent_project_fk"))
	})
})
