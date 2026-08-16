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

var _ = DescribeMigration("Update materialized constraints to active tables", func() {
	It("Changes storage_tier_backends FK to reference active_storage_tiers", func(ctx context.Context) {
		err := tool.Migrate(ctx, 98)
		Expect(err).ToNot(HaveOccurred())

		var refTable string
		err = conn.QueryRow(ctx, `
			select ccu.table_name
			from information_schema.table_constraints tc
			join information_schema.constraint_column_usage ccu
			  on ccu.constraint_name = tc.constraint_name
			  and ccu.table_schema = tc.table_schema
			where tc.table_name = 'storage_tier_backends'
			  and tc.constraint_name = 'storage_tier_backends_storage_tier_id_fkey'
			  and tc.constraint_type = 'FOREIGN KEY'
		`).Scan(&refTable)
		Expect(err).ToNot(HaveOccurred())
		Expect(refTable).To(Equal("active_storage_tiers"))
	})

	It("Cascades deletion from active_storage_tiers to storage_tier_backends", func(ctx context.Context) {
		err := tool.Migrate(ctx, 98)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('t-1', 't-1', 't-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data)
			 values ('sb-1', 'sb-1', 't-1', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-1', 'tier-1', 't-1', '{"spec":{"backends":[{"backend_id":"sb-1"}]}}')`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx,
			`select count(*) from storage_tier_backends where storage_tier_id = 'tier-1'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		_, err = conn.Exec(ctx,
			`update storage_tiers set deletion_timestamp = now() where id = 'tier-1'`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx,
			`select count(*) from storage_tier_backends where storage_tier_id = 'tier-1'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})
})
