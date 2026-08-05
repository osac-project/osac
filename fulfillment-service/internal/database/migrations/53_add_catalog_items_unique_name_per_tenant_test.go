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
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add catalog items unique name per tenant", func() {
	DescribeTable(
		"Enforces name uniqueness per tenant",
		func(ctx context.Context, table, name1, tenant1, name2, tenant2 string, shouldFail bool) {
			err := tool.Migrate(ctx, 53)
			Expect(err).ToNot(HaveOccurred())

			var insertSQL string
			switch table {
			case "cluster_catalog_items":
				insertSQL = `insert into cluster_catalog_items (id, name, creator, tenant, data)
					values ($1, $2, 'admin', $3, '{}')`
			case "compute_instance_catalog_items":
				insertSQL = `insert into compute_instance_catalog_items (id, name, creator, tenant, data)
					values ($1, $2, 'admin', $3, '{}')`
			default:
				Fail(fmt.Sprintf("unexpected table: %s", table))
			}

			_, err = conn.Exec(ctx, insertSQL, "id-1", name1, tenant1)
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, insertSQL, "id-2", name2, tenant2)
			if shouldFail {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unique_name_per_tenant"))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
		Entry("Rejects duplicate cluster catalog item name within same tenant",
			"cluster_catalog_items", "dev-sandbox", "system", "dev-sandbox", "system", true),
		Entry("Allows same cluster catalog item name across different tenants",
			"cluster_catalog_items", "dev-sandbox", "system", "dev-sandbox", "shared", false),
		Entry("Allows duplicate empty names within same tenant for cluster catalog items",
			"cluster_catalog_items", "", "system", "", "system", false),
		Entry("Rejects duplicate compute instance catalog item name within same tenant",
			"compute_instance_catalog_items", "gpu-large", "system", "gpu-large", "system", true),
		Entry("Allows same compute instance catalog item name across different tenants",
			"compute_instance_catalog_items", "gpu-large", "system", "gpu-large", "shared", false),
		Entry("Allows duplicate empty names within same tenant for compute instance catalog items",
			"compute_instance_catalog_items", "", "system", "", "system", false),
	)

	It("Allows reuse of name after soft-delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 53)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, name, creator, tenant, data)
			 values ($1, $2, 'admin', $3, '{}')`,
			"id-1", "dev-sandbox", "system")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = $1`,
			"id-1")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, name, creator, tenant, data)
			 values ($1, $2, 'admin', $3, '{}')`,
			"id-2", "dev-sandbox", "system")
		Expect(err).ToNot(HaveOccurred())
	})
})
