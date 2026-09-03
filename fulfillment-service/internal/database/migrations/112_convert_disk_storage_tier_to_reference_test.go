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

var _ = DescribeMigration("Convert disk storage_tier to reference", func() {
	It("Resolves boot_disk.storage_tier name to {id, name} via active storage tier", func(ctx context.Context) {
		// Seed an active storage tier that the migration's name→id join will resolve.
		// The migration predicate is: st.name = <legacy_string> AND st.deletion_timestamp = 'epoch'.
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ($1, $2, $3, $4::jsonb)`,
			"st-standard-id", "standard", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert a compute instance with old-style bare-string storage_tier.
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ($1, $2, $3::jsonb)`,
			"ci-boot-resolve", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":"standard"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-boot-resolve",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		spec := parsed["spec"].(map[string]interface{})
		bootDisk := spec["boot_disk"].(map[string]interface{})
		tierRef := bootDisk["storage_tier"].(map[string]interface{})
		Expect(tierRef["id"]).To(Equal("st-standard-id"))
		Expect(tierRef["name"]).To(Equal("standard"))
	})

	It("Backfills additional_disks[*].storage_tier from string to reference object preserving order", func(ctx context.Context) {
		// Seed storage tiers for the additional disks to resolve against.
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('st-fast-id', 'fast', 'system', '{}'::jsonb),
			        ('st-archive-id', 'archive', 'system', '{}'::jsonb)`,
		)
		Expect(err).ToNot(HaveOccurred())

		// The two disks have DIFFERENT sizes (100, 50) and DIFFERENT tier names ("fast", "archive")
		// so we can assert the migration's WITH ORDINALITY preserves their original order.
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data) values ($1, $2, $3)`,
			"ci-additional-string", "system",
			`{"spec":{"additional_disks":[{"size_gib":100,"storage_tier":"fast"},{"size_gib":50,"storage_tier":"archive"}]}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-additional-string",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(data, &parsed)
		Expect(err).ToNot(HaveOccurred())

		spec := parsed["spec"].(map[string]interface{})
		disks := spec["additional_disks"].([]interface{})
		Expect(disks).To(HaveLen(2))

		// Assert disk order is preserved and ids are resolved:
		disk0 := disks[0].(map[string]interface{})
		Expect(disk0["size_gib"]).To(BeNumerically("==", 100))
		tier0 := disk0["storage_tier"].(map[string]interface{})
		Expect(tier0["id"]).To(Equal("st-fast-id"))
		Expect(tier0["name"]).To(Equal("fast"))

		disk1 := disks[1].(map[string]interface{})
		Expect(disk1["size_gib"]).To(BeNumerically("==", 50))
		tier1 := disk1["storage_tier"].(map[string]interface{})
		Expect(tier1["id"]).To(Equal("st-archive-id"))
		Expect(tier1["name"]).To(Equal("archive"))
	})

	It("Skips rows already in the new format", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data) values ($1, $2, $3)`,
			"ci-already-ref", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":{"id":"st-existing","name":"existing"}}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-already-ref",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(data, &parsed)
		Expect(err).ToNot(HaveOccurred())

		spec := parsed["spec"].(map[string]interface{})
		bootDisk := spec["boot_disk"].(map[string]interface{})
		tierRef := bootDisk["storage_tier"].(map[string]interface{})
		Expect(tierRef["id"]).To(Equal("st-existing"))
		Expect(tierRef["name"]).To(Equal("existing"))
	})

	It("Handles missing storage tier gracefully with name-only fallback", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data) values ($1, $2, $3)`,
			"ci-orphan-tier", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":"deleted-tier"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-orphan-tier",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(data, &parsed)
		Expect(err).ToNot(HaveOccurred())

		spec := parsed["spec"].(map[string]interface{})
		bootDisk := spec["boot_disk"].(map[string]interface{})
		tierRef := bootDisk["storage_tier"].(map[string]interface{})
		Expect(tierRef["name"]).To(Equal("deleted-tier"))
		Expect(tierRef).ToNot(HaveKey("id"))
	})

	It("Backfills archived_compute_instances with resolved storage tier", func(ctx context.Context) {
		// Seed an active storage tier for the archived row to resolve against.
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ($1, $2, $3, $4::jsonb)`,
			"st-archive-id", "archive-tier", "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into archived_compute_instances (id, tenant, data, creation_timestamp, deletion_timestamp)
			 values ($1, $2, $3, now(), now())`,
			"ci-archived-string", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":"archive-tier"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from archived_compute_instances where id = $1`, "ci-archived-string",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(data, &parsed)
		Expect(err).ToNot(HaveOccurred())

		spec := parsed["spec"].(map[string]interface{})
		bootDisk := spec["boot_disk"].(map[string]interface{})
		tierRef := bootDisk["storage_tier"].(map[string]interface{})
		Expect(tierRef["id"]).To(Equal("st-archive-id"))
		Expect(tierRef["name"]).To(Equal("archive-tier"))
	})
})
