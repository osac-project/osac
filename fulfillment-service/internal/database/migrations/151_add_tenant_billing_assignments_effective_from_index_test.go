/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add tenant billing assignments effective_from index", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 150)).To(Succeed())
	})

	It("Creates the index backing the assignments List default order", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 151)).To(Succeed())

		var indexDef string
		err := conn.QueryRow(ctx, `
			select indexdef
			from pg_indexes
			where schemaname = current_schema()
			  and tablename = 'tenant_billing_assignments'
			  and indexname = 'tenant_billing_assignments_by_effective_from'
		`).Scan(&indexDef)
		Expect(err).ToNot(HaveOccurred())
		Expect(indexDef).To(ContainSubstring("tenant_billing_assignments_by_effective_from"))
		Expect(indexDef).To(ContainSubstring("USING btree (effective_from DESC)"))
	})
})
