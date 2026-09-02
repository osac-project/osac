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

var _ = DescribeMigration("Add instance type index on templates", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 108)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the partial index on compute_instance_templates instance_type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 109)
		Expect(err).ToNot(HaveOccurred())

		var indexDef string
		err = conn.QueryRow(ctx, `
			select
				indexdef
			from
				pg_indexes
			where
				tablename = 'compute_instance_templates' and
				indexname = 'compute_instance_templates_instance_type'
		`).Scan(&indexDef)
		Expect(err).ToNot(HaveOccurred())
		Expect(indexDef).To(ContainSubstring("((data -> 'spec_defaults'::text) -> 'instance_type'::text) ->> 'id'::text"))
		Expect(indexDef).To(ContainSubstring("WHERE (deletion_timestamp = '1970-01-01 00:00:00+00'::timestamp with time zone)"))
	})
})
