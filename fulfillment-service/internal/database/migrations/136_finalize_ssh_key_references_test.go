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
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
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

	seedSecret := func(ctx context.Context, id string) {
		GinkgoHelper()
		_, err := conn.Exec(ctx, `
			insert into secrets (id, name, tenant, data)
			values ($1, $1, 'ssh-finalize-tenant', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteSecret := func(ctx context.Context, id string) {
		GinkgoHelper()
		_, err := conn.Exec(ctx, `update secrets set deletion_timestamp = now() where id = $1`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertCluster := func(ctx context.Context, id, data string) error {
		GinkgoHelper()
		_, err := conn.Exec(ctx, `
			insert into clusters (id, name, tenant, data)
			values ($1, $1, 'ssh-finalize-tenant', $2::jsonb)`, id, data)
		return err
	}

	updateCluster := func(ctx context.Context, id, data string) error {
		GinkgoHelper()
		_, err := conn.Exec(ctx, `
			update clusters set data = $2::jsonb where id = $1`, id, data)
		return err
	}

	expectZ0002 := func(err error) {
		GinkgoHelper()
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
	}

	It("removes legacy inline SSH-key JSON from stored rows", func(ctx context.Context) {
		insertTenant(ctx)
		seedSecret(ctx, "fin-bmi-key")

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

		Expect(tool.Migrate(ctx, 136)).To(Succeed())

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

	It("rejects a cluster insert referencing a missing Secret", func(ctx context.Context) {
		insertTenant(ctx)
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		expectZ0002(insertCluster(ctx, "fin-cl-missing",
			`{"spec":{"ssh_key":{"id":"missing-secret"}}}`))
	})

	It("rejects a cluster insert referencing another tenant's Secret", func(ctx context.Context) {
		insertTenant(ctx)
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('ssh-finalize-other-tenant', 'ssh-finalize-other-tenant', 'ssh-finalize-other-tenant', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into secrets (id, name, tenant, data)
			values ('fin-foreign-key', 'fin-foreign-key', 'ssh-finalize-other-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		expectZ0002(insertCluster(ctx, "fin-cl-foreign",
			`{"spec":{"ssh_key":{"id":"fin-foreign-key"}}}`))
	})

	It("rejects changing to a missing Secret and accepts changing to a live one", func(ctx context.Context) {
		insertTenant(ctx)
		seedSecret(ctx, "fin-live-key")
		Expect(tool.Migrate(ctx, 136)).To(Succeed())

		Expect(insertCluster(ctx, "fin-cl-change",
			`{"spec":{"ssh_key":{"id":"fin-live-key"}}}`)).To(Succeed())

		expectZ0002(updateCluster(ctx, "fin-cl-change",
			`{"spec":{"ssh_key":{"id":"other-missing"}}}`))

		seedSecret(ctx, "fin-new-key")
		Expect(updateCluster(ctx, "fin-cl-change",
			`{"spec":{"ssh_key":{"id":"fin-new-key"}}}`)).To(Succeed())
	})

	It("allows same-reference updates and clearing after Secret deletion", func(ctx context.Context) {
		insertTenant(ctx)
		seedSecret(ctx, "fin-stale-key")
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		Expect(insertCluster(ctx, "fin-cl-stale",
			`{"spec":{"ssh_key":{"id":"fin-stale-key"}}}`)).To(Succeed())
		deleteSecret(ctx, "fin-stale-key")

		Expect(updateCluster(ctx, "fin-cl-stale",
			`{"spec":{"ssh_key":{"id":"fin-stale-key"},"status":{"state":"CLUSTER_STATE_READY"}}}`)).To(Succeed())
		Expect(updateCluster(ctx, "fin-cl-stale",
			`{"spec":{}}`)).To(Succeed())
	})

	It("allows changing from a deleted Secret to a live replacement", func(ctx context.Context) {
		insertTenant(ctx)
		seedSecret(ctx, "fin-old-key")
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		Expect(insertCluster(ctx, "fin-cl-replace",
			`{"spec":{"ssh_key":{"id":"fin-old-key"}}}`)).To(Succeed())
		deleteSecret(ctx, "fin-old-key")

		seedSecret(ctx, "fin-replacement-key")
		Expect(updateCluster(ctx, "fin-cl-replace",
			`{"spec":{"ssh_key":{"id":"fin-replacement-key"}}}`)).To(Succeed())
	})

	It("allows deleting a Secret referenced by an active cluster", func(ctx context.Context) {
		insertTenant(ctx)
		seedSecret(ctx, "fin-referenced-key")
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		Expect(insertCluster(ctx, "fin-cl-allowed",
			`{"spec":{"ssh_key":{"id":"fin-referenced-key"}}}`)).To(Succeed())
		deleteSecret(ctx, "fin-referenced-key")
	})
})
