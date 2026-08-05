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

var _ = DescribeMigration("Create instance types tables", func() {
	It("Creates the instance_types table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 51)
		Expect(err).ToNot(HaveOccurred())

		// Verify the instance_types table exists:
		var exists bool
		row := conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'instance_types'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Can insert and query an instance type", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 51)
		Expect(err).ToNot(HaveOccurred())

		// Insert a test instance type:
		_, err = conn.Exec(
			ctx,
			`insert into instance_types (id, name, creator, tenant, data)
			 values ('standard-4-16', 'standard-4-16', 'admin', 'system', '{"cores":4,"memory_gib":16}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Query it back:
		var name, creator, tenant string
		row := conn.QueryRow(
			ctx,
			`select name, creator, tenant from instance_types where id = 'standard-4-16'`,
		)
		err = row.Scan(&name, &creator, &tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("standard-4-16"))
		Expect(creator).To(Equal("admin"))
		Expect(tenant).To(Equal("system"))
	})
})
