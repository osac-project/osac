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

var _ = DescribeMigration("Drop public IP tables", func() {
	It("Drops all public IP tables", func(ctx context.Context) {
		err := tool.Migrate(ctx, 84)
		Expect(err).ToNot(HaveOccurred())

		tables := []string{
			"public_ip_attachments",
			"archived_public_ip_attachments",
			"public_ips",
			"archived_public_ips",
			"public_ip_pools",
			"archived_public_ip_pools",
		}
		for _, table := range tables {
			var exists bool
			err := conn.QueryRow(ctx,
				`select exists (
					select from information_schema.tables
					where table_schema = 'public' and table_name = $1
				)`, table).Scan(&exists)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeFalse(), "table %s should not exist after migration", table)
		}
	})
})
