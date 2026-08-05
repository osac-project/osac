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

var _ = DescribeMigration("Create projects tables", func() {
	It("Creates the projects table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 47)
		Expect(err).ToNot(HaveOccurred())

		// Verify the projects table exists:
		var exists bool
		row := conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'projects'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Verify the archived_projects table exists:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'archived_projects'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Can insert and query a project", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 47)
		Expect(err).ToNot(HaveOccurred())

		// Insert a test project:
		_, err = conn.Exec(
			ctx,
			`insert into projects (id, name, creator, tenant, data)
			 values ('test-id', 'test-project', 'test-creator', 'test-tenant', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Query it back:
		var name, creator, tenant string
		row := conn.QueryRow(
			ctx,
			`select name, creator, tenant from projects where id = 'test-id'`,
		)
		err = row.Scan(&name, &creator, &tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("test-project"))
		Expect(creator).To(Equal("test-creator"))
		Expect(tenant).To(Equal("test-tenant"))
	})
})
