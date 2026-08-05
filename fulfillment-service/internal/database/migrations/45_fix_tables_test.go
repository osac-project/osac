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

var _ = DescribeMigration("Fix tables", func() {
	It("Renames creators to creator in archived_public_ip_attachments", func(ctx context.Context) {
		// Insert using the old plural column names that exist before this migration:
		_, err := conn.Exec(
			ctx,
			`insert into archived_public_ip_attachments
			 (id, creation_timestamp, deletion_timestamp, creators, tenants, data)
			 values ('a1', now(), now(), '{user-a}', '{tenant-a}', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 45)
		Expect(err).ToNot(HaveOccurred())

		var creator string
		err = conn.QueryRow(ctx, `select creator from archived_public_ip_attachments where id = 'a1'`).
			Scan(&creator)
		Expect(err).ToNot(HaveOccurred())
		Expect(creator).To(Equal("user-a"))
	})

	It("Renames tenants to tenant in archived_public_ip_attachments", func(ctx context.Context) {
		// Insert using the old plural column names that exist before this migration:
		_, err := conn.Exec(
			ctx,
			`insert into archived_public_ip_attachments
			 (id, creation_timestamp, deletion_timestamp, creators, tenants, data)
			 values ('a2', now(), now(), '{user-b}', '{my-tenant}', '{}')`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 45)
		Expect(err).ToNot(HaveOccurred())

		var tenant string
		err = conn.QueryRow(ctx, `select tenant from archived_public_ip_attachments where id = 'a2'`).
			Scan(&tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant).To(Equal("my-tenant"))
	})

	It("Drops finalizers from archived_organizations", func(ctx context.Context) {
		err := tool.Migrate(ctx, 45)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(
			ctx,
			`select count(*) from information_schema.columns
			 where table_name = 'archived_organizations' and column_name = 'finalizers'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Drops finalizers from archived_users", func(ctx context.Context) {
		err := tool.Migrate(ctx, 45)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(
			ctx,
			`select count(*) from information_schema.columns
			 where table_name = 'archived_users' and column_name = 'finalizers'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})
})
