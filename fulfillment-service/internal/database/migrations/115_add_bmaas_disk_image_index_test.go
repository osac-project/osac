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

var _ = DescribeMigration("Add BMaaS disk image index", func() {
	It("Creates the active bare metal disk image index", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 115)).To(Succeed())

		var expression, predicate string
		err := conn.QueryRow(ctx, `
			select pg_get_expr(indexprs, indrelid), pg_get_expr(indpred, indrelid)
			from pg_index
			where indexrelid = 'bare_metal_instances_disk_image'::regclass`,
		).Scan(&expression, &predicate)
		Expect(err).ToNot(HaveOccurred())
		Expect(expression).To(ContainSubstring("data"))
		Expect(expression).To(ContainSubstring("spec"))
		Expect(expression).To(ContainSubstring("disk_image"))
		Expect(expression).To(ContainSubstring("id"))
		Expect(predicate).To(ContainSubstring("deletion_timestamp"))
		Expect(predicate).To(ContainSubstring("1970-01-01"))
	})
})
