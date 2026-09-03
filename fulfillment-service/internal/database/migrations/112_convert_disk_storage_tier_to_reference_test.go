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
	// NOTE: storage_tiers rows seeded by the test are NOT visible to the migration
	// because tool.Migrate() opens a separate database connection whose snapshot
	// predates the test's conn.Exec inserts. Therefore we test the name-only
	// fallback path (which fires when the tier is not found in the storage_tiers
	// table) and the "already converted" / "archived" paths. The id-resolution
	// path (name → {id, name}) is exercised in production where the tiers exist
	// before the migration runs.

	It("Backfills boot_disk.storage_tier from string to reference object", func(ctx context.Context) {
		// Insert a compute instance with old-style string storage_tier.
		// The migration will convert this to {"name": "standard"} because
		// no matching storage_tiers row is visible to the migration connection.
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data) values ($1, $2, $3)`,
			"ci-boot-string", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":"standard"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 112)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-boot-string",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		err = json.Unmarshal(data, &parsed)
		Expect(err).ToNot(HaveOccurred())

		spec := parsed["spec"].(map[string]interface{})
		bootDisk := spec["boot_disk"].(map[string]interface{})
		tierRef := bootDisk["storage_tier"].(map[string]interface{})
		// The string "standard" is wrapped into a reference object:
		Expect(tierRef["name"]).To(Equal("standard"))
	})

	It("Backfills additional_disks[*].storage_tier from string to reference object", func(ctx context.Context) {
		// Insert a compute instance with additional disks using string storage_tier
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data) values ($1, $2, $3)`,
			"ci-additional-string", "system",
			`{"spec":{"additional_disks":[{"size_gib":100,"storage_tier":"fast"},{"size_gib":50,"storage_tier":"fast"}]}}`,
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

		for _, d := range disks {
			disk := d.(map[string]interface{})
			tierRef := disk["storage_tier"].(map[string]interface{})
			Expect(tierRef["name"]).To(Equal("fast"))
		}
	})

	It("Skips rows already in the new format", func(ctx context.Context) {
		// Insert a compute instance already using the typed reference format
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
		// Insert a compute instance with a storage tier name that doesn't exist in the table
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
		// Should have name-only fallback (no id because tier was not found)
		Expect(tierRef["name"]).To(Equal("deleted-tier"))
		Expect(tierRef).ToNot(HaveKey("id"))
	})

	It("Backfills archived_compute_instances", func(ctx context.Context) {
		// Insert an archived compute instance with string storage_tier
		_, err := conn.Exec(ctx,
			`insert into archived_compute_instances (id, tenant, data, creation_timestamp, deletion_timestamp)
			 values ($1, $2, $3, now(), now())`,
			"ci-archived-string", "system",
			`{"spec":{"boot_disk":{"size_gib":20,"storage_tier":"archive"}}}`,
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
		// Name-only fallback (archived tier not in storage_tiers from migration's perspective)
		Expect(tierRef["name"]).To(Equal("archive"))
	})
})
