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

var _ = DescribeMigration("Add changes quarantine index", func() {
	It("Creates a valid partial index over quarantined rows", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		var predicate string
		var valid bool
		err := conn.QueryRow(ctx, `
			select pg_get_expr(indpred, indrelid), indisvalid
			from pg_index
			where indexrelid = 'changes_quarantined'::regclass`,
		).Scan(&predicate, &valid)
		Expect(err).ToNot(HaveOccurred())
		Expect(predicate).To(ContainSubstring("quarantined_at"))
		Expect(predicate).To(ContainSubstring("IS NOT NULL"))
		Expect(valid).To(BeTrue())
	})
})
