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

var _ = DescribeMigration("Remove unbindable built-in roles", func() {
	It("keeps only the bindable built-in roles", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 139)).To(Succeed())

		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from roles`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(5))
		Expect(conn.QueryRow(ctx, `select count(*) from active_roles`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(5))
		Expect(conn.QueryRow(ctx, `select count(*) from roles
			where name in ('cloud-provider-admin', 'tenant-admin', 'tenant-idp-manager')`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(3))
	})

	It("preserves roles outside the original seed entries", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into roles (id, name, tenant, data)
			values ('custom-role', 'custom-role', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 139)).To(Succeed())

		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from roles where id = 'custom-role'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
})
