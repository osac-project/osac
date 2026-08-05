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

var _ = DescribeMigration("Add tenant foreign key", func() {
	It("Rejects rows with a non-existent tenant", func(ctx context.Context) {
		err := tool.Migrate(ctx, 49)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into clusters (
				id,
				name,
				tenant,
				data
			)
			values (
				'123',
				'my-cluster',
				'my-tenant',
				'{}'
			)
		`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant_fk"))
	})
})
