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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Protect StorageTier Volume references", func() {
	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into storage_tiers (id, name, tenant, data)
			values ('tier', 'tier', 'shared', '{}'), ('other-tier', 'other-tier', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	expectInUse := func(ctx context.Context) {
		GinkgoHelper()
		_, err := conn.Exec(ctx, `update storage_tiers set deletion_timestamp = now() where id = 'tier'`)
		var pgError *pgconn.PgError
		Expect(errors.As(err, &pgError)).To(BeTrue())
		Expect(pgError.Code).To(Equal("Z0003"))
		Expect(pgError.Message).To(ContainSubstring("in use"))
		var active bool
		err = conn.QueryRow(ctx, `select deletion_timestamp = 'epoch' from storage_tiers where id = 'tier'`).Scan(&active)
		Expect(err).ToNot(HaveOccurred())
		Expect(active).To(BeTrue())
	}

	DescribeTable("protects pre-existing active Volumes regardless of provisioning state",
		func(ctx context.Context, state string) {
			data := fmt.Sprintf(`{"spec":{"storage_tier":"tier"},"status":{"state":%q}}`, state)
			_, err := conn.Exec(ctx, `insert into volumes (id, name, tenant, data)
				values ('volume', 'volume', 'system', $1::jsonb)`, data)
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Migrate(ctx, 149)).To(Succeed())

			expectInUse(ctx)
			_, err = conn.Exec(ctx, `update storage_tiers set data = '{"spec":{"description":"updated"}}' where id = 'tier'`)
			Expect(err).ToNot(HaveOccurred())
		},
		Entry("unspecified", "VOLUME_STATE_UNSPECIFIED"),
		Entry("creating", "VOLUME_STATE_CREATING"),
		Entry("available", "VOLUME_STATE_AVAILABLE"),
		Entry("failed", "VOLUME_STATE_FAILED"),
		Entry("deleting without a deletion timestamp", "VOLUME_STATE_DELETING"),
		Entry("deleted without a deletion timestamp", "VOLUME_STATE_DELETED"),
	)

	DescribeTable("allows deletion without an active matching Volume",
		func(ctx context.Context, table, tier string, deleted bool) {
			data := fmt.Sprintf(`{"spec":{"storage_tier":%q}}`, tier)
			_, err := conn.Exec(ctx, fmt.Sprintf(`insert into %s
				(id, name, tenant, creation_timestamp, deletion_timestamp, data)
				values ('volume', 'volume', 'system', now(),
					case when $1 then now() else 'epoch'::timestamptz end, $2::jsonb)`, table), deleted, data)
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.Migrate(ctx, 149)).To(Succeed())

			_, err = conn.Exec(ctx, `update storage_tiers set deletion_timestamp = now() where id = 'tier'`)
			Expect(err).ToNot(HaveOccurred())
		},
		Entry("another tier's active Volume", "volumes", "other-tier", false),
		Entry("Volume without a tier reference", "volumes", "", false),
		Entry("soft-deleted Volume", "volumes", "tier", true),
		Entry("archived Volume", "archived_volumes", "tier", true),
	)

	It("allows deletion only after every active Volume reference is removed", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into volumes (id, name, tenant, data)
			values ('first', 'first', 'system', '{"spec":{"storage_tier":"tier"}}'),
				('second', 'second', 'shared', '{"spec":{"storage_tier":"tier"}}')`)
		Expect(err).ToNot(HaveOccurred())
		expectInUse(ctx)
		_, err = conn.Exec(ctx, `update volumes set deletion_timestamp = now() where id = 'first'`)
		Expect(err).ToNot(HaveOccurred())
		expectInUse(ctx)
		_, err = conn.Exec(ctx, `update volumes set deletion_timestamp = now() where id = 'second'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update storage_tiers set deletion_timestamp = now() where id = 'tier'`)
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("preserves other StorageTier reference protections",
		func(ctx context.Context, table, data string) {
			Expect(tool.Migrate(ctx, 149)).To(Succeed())
			_, err := conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data)
				values ('consumer', 'consumer', 'system', $1::jsonb)`, table), data)
			Expect(err).ToNot(HaveOccurred())
			expectInUse(ctx)
		},
		Entry("compute boot disk", "compute_instances", `{"spec":{"boot_disk":{"storage_tier":{"id":"tier"}}}}`),
		Entry("compute additional disk", "compute_instances", `{"spec":{"additional_disks":{"items":[{"storage_tier":{"id":"tier"}}]}}}`),
		Entry("template boot disk", "compute_instance_templates", `{"spec_defaults":{"boot_disk":{"storage_tier":{"id":"tier"}}}}`),
		Entry("template additional disk", "compute_instance_templates", `{"spec_defaults":{"additional_disks":[{"storage_tier":{"id":"tier"}}]}}`),
		Entry("catalog locked boot disk", "compute_instance_catalog_items", `{"fields":{"boot_disk":{"storage_tier":{"locked":{"id":"tier"}}}}}`),
		Entry("catalog default boot disk", "compute_instance_catalog_items", `{"fields":{"boot_disk":{"storage_tier":{"editable":{"default_value":{"id":"tier"}}}}}}`),
		Entry("catalog locked additional disk", "compute_instance_catalog_items", `{"fields":{"additional_disks":{"locked":{"items":[{"storage_tier":{"id":"tier"}}]}}}}`),
		Entry("catalog default additional disk", "compute_instance_catalog_items", `{"fields":{"additional_disks":{"editable":{"default_value":{"items":[{"storage_tier":{"id":"tier"}}]}}}}}`),
	)
})
