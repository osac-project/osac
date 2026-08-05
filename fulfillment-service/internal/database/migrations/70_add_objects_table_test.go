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

var _ = DescribeMigration("Add objects table", func() {
	It("Creates the objects table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 70)
		Expect(err).ToNot(HaveOccurred())

		// Verify the objects table exists:
		var count int
		row := conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_tables
			where
				schemaname = 'public' and
				tablename = 'objects'
		`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically("==", 1))
	})

	It("Creates the archived_objects table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 70)
		Expect(err).ToNot(HaveOccurred())

		// Verify the archived objects table exists:
		var count int
		row := conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_tables
			where
				schemaname = 'public' and
				tablename = 'archived_objects'
		`)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically("==", 1))
	})
})
