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

var _ = DescribeMigration("Drop leases tables", func() {
	It("Drops the leases table", func(ctx context.Context) {
		// Verify the table exists before the migration:
		var exists bool
		err := conn.QueryRow(ctx,
			`select exists (select 1 from information_schema.tables where table_name = 'leases')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Run the migration:
		err = tool.Migrate(ctx, 43)
		Expect(err).ToNot(HaveOccurred())

		// Verify the table no longer exists:
		err = conn.QueryRow(ctx,
			`select exists (select 1 from information_schema.tables where table_name = 'leases')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Drops the archived_leases table", func(ctx context.Context) {
		// Verify the table exists before the migration:
		var exists bool
		err := conn.QueryRow(ctx,
			`select exists (select 1 from information_schema.tables where table_name = 'archived_leases')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Run the migration:
		err = tool.Migrate(ctx, 43)
		Expect(err).ToNot(HaveOccurred())

		// Verify the table no longer exists:
		err = conn.QueryRow(ctx,
			`select exists (select 1 from information_schema.tables where table_name = 'archived_leases')`,
		).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
})
