/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Finalize SSH-key references", func() {
	insertTenant := func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('ssh-finalize-tenant', 'ssh-finalize-tenant', 'ssh-finalize-tenant', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	}

	It("removes legacy inline SSH-key JSON from stored rows", func(ctx context.Context) {
		insertTenant(ctx)

		_, err := conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('fin-bmi', 'fin-bmi', 'ssh-finalize-tenant',
			        '{"spec":{"ssh_public_key":"ssh-ed25519 legacy","ssh_key":{"id":"fin-bmi-key"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into clusters (id, name, tenant, data)
			values ('fin-cl-legacy', 'fin-cl-legacy', 'ssh-finalize-tenant',
			        '{"spec":{"ssh_public_key":"ssh-ed25519 legacy"}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into bare_metal_instance_catalog_items (id, name, tenant, data)
			values ('fin-bmi-cat', 'fin-bmi-cat', 'ssh-finalize-tenant',
			        '{"fields":{"ssh_public_key":{"locked":"ssh-ed25519 legacy"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into cluster_catalog_items (id, name, tenant, data)
			values ('fin-cl-cat', 'fin-cl-cat', 'ssh-finalize-tenant',
			        '{"fields":{"ssh_public_key":{"locked":"ssh-ed25519 legacy"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 139)).To(Succeed())

		var value string
		err = conn.QueryRow(ctx, `select coalesce(data->'spec'->>'ssh_public_key', '') from bare_metal_instances where id = 'fin-bmi'`).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(BeEmpty())
		err = conn.QueryRow(ctx, `select coalesce(data->'spec'->'ssh_key'->>'id', '') from bare_metal_instances where id = 'fin-bmi'`).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal("fin-bmi-key"))

		err = conn.QueryRow(ctx, `select coalesce(data->'spec'->>'ssh_public_key', '') from clusters where id = 'fin-cl-legacy'`).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(BeEmpty())

		err = conn.QueryRow(ctx, `select coalesce(data->'fields'->>'ssh_public_key', '') from bare_metal_instance_catalog_items where id = 'fin-bmi-cat'`).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(BeEmpty())

		err = conn.QueryRow(ctx, `select coalesce(data->'fields'->>'ssh_public_key', '') from cluster_catalog_items where id = 'fin-cl-cat'`).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(BeEmpty())
	})
})
