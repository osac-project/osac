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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Fix tenant domains project reference", func() {
	BeforeEach(func(ctx context.Context) {
		data, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"domains": []string{"example.com"},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', $1)
		`, data)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows deleting the root project and cascades tenant domain helpers", func(ctx context.Context) {
		err := tool.Migrate(ctx, 139)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `select count(*) from tenant_domains where tenant = 'my-tenant'`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		_, err = conn.Exec(ctx, `delete from projects where tenant = 'my-tenant' and name = ''`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `delete from tenants where tenant = 'my-tenant'`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx, `select count(*) from tenant_domains where tenant = 'my-tenant'`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
	})
})
