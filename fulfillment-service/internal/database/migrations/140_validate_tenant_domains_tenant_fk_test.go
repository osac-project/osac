/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Validate tenant domains tenant reference", func() {
	It("validates the tenant foreign key without changing its definition", func(ctx context.Context) {
		var validated bool
		err := conn.QueryRow(ctx, `
			select convalidated
			from pg_constraint
			where conname = 'tenant_domains_tenant_fk'
		`).Scan(&validated)
		Expect(err).ToNot(HaveOccurred())
		Expect(validated).To(BeFalse())

		Expect(tool.Migrate(ctx, 140)).To(Succeed())

		var definition string
		err = conn.QueryRow(ctx, `
			select pg_get_constraintdef(oid), convalidated
			from pg_constraint
			where conname = 'tenant_domains_tenant_fk'
		`).Scan(&definition, &validated)
		Expect(err).ToNot(HaveOccurred())
		Expect(validated).To(BeTrue())
		Expect(definition).To(ContainSubstring("FOREIGN KEY (tenant) REFERENCES tenants(name) ON DELETE CASCADE"))
	})
})
