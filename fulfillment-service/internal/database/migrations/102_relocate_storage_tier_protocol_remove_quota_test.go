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

var _ = DescribeMigration("Remove storage tier protocol and quota", func() {
	It("Relocates protocol to spec and strips protocol/quota_gib from backends for a row with one backend association", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-1', 'tier-1', 'system', $1::jsonb)`,
			`{"spec":{"backends":[{"backend_id":"sb-1","protocol":"STORAGE_PROTOCOL_NFS","quota_gib":500,"max_read_bandwidth_mbs":100}]}}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var backendID string
		var maxReadBandwidth int
		err = conn.QueryRow(ctx,
			`select data->'spec'->'backends'->0->>'backend_id', (data->'spec'->'backends'->0->>'max_read_bandwidth_mbs')::int
			 from storage_tiers where id = 'tier-1'`).Scan(&backendID, &maxReadBandwidth)
		Expect(err).ToNot(HaveOccurred())
		Expect(backendID).To(Equal("sb-1"))
		Expect(maxReadBandwidth).To(Equal(100))

		var backendHasProtocol, backendHasQuota bool
		var specProtocol string
		err = conn.QueryRow(ctx,
			`select (data->'spec'->'backends'->0) ? 'protocol', (data->'spec'->'backends'->0) ? 'quota_gib', data->'spec'->>'protocol'
			 from storage_tiers where id = 'tier-1'`).Scan(&backendHasProtocol, &backendHasQuota, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(backendHasProtocol).To(BeFalse())
		Expect(backendHasQuota).To(BeFalse())
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_NFS"))
	})

	It("Relocates a BLOCK protocol to spec for an active row (the local LVMS tier upgrade shape)", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-block', 'tier-block', 'system', $1::jsonb)`,
			`{"spec":{"backends":[{"backend_id":"sb-block","protocol":"STORAGE_PROTOCOL_BLOCK"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var backendHasProtocol bool
		var specProtocol string
		err = conn.QueryRow(ctx,
			`select (data->'spec'->'backends'->0) ? 'protocol', data->'spec'->>'protocol'
			 from storage_tiers where id = 'tier-block'`).Scan(&backendHasProtocol, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(backendHasProtocol).To(BeFalse())
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_BLOCK"))
	})

	It("Backfills UNSPECIFIED protocol for a row with no backend associations", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-empty', 'tier-empty', 'system', $1::jsonb)`,
			`{"spec":{"backends":[]}}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var backends, specProtocol string
		err = conn.QueryRow(ctx,
			`select (data->'spec'->'backends')::text, data->'spec'->>'protocol'
			 from storage_tiers where id = 'tier-empty'`).Scan(&backends, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(backends).To(Equal("[]"))
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_UNSPECIFIED"))
	})

	It("Relocates protocol to spec and strips protocol/quota_gib from an archived row already in the post-migration-77 nested shape", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into archived_storage_tiers (id, tenant, data, creation_timestamp, deletion_timestamp)
			 values ('archived-tier-nested', 'system', $1::jsonb, now(), now())`,
			`{"spec":{"backends":[{"backend_id":"sb-2","protocol":"STORAGE_PROTOCOL_BLOCK","quota_gib":200}]},"status":{"state":1}}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var backendHasProtocol, backendHasQuota bool
		var specProtocol string
		err = conn.QueryRow(ctx,
			`select (data->'spec'->'backends'->0) ? 'protocol', (data->'spec'->'backends'->0) ? 'quota_gib', data->'spec'->>'protocol'
			 from archived_storage_tiers where id = 'archived-tier-nested'`).Scan(&backendHasProtocol, &backendHasQuota, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(backendHasProtocol).To(BeFalse())
		Expect(backendHasQuota).To(BeFalse())
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_BLOCK"))
	})

	It("Restructures a pre-migration-77 archived row from the flat shape, relocating protocol and stripping quota_gib in the same pass", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into archived_storage_tiers (id, tenant, data, creation_timestamp, deletion_timestamp)
			 values ('archived-tier-flat', 'system', $1::jsonb, now(), now())`,
			`{"description":"legacy tier","backends":[{"backend_id":"sb-3","protocol":"STORAGE_PROTOCOL_NFS","quota_gib":50,"encryption_enabled":true}],"state":"STORAGE_TIER_STATE_ACTIVE"}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var data string
		err = conn.QueryRow(ctx,
			`select data::text from archived_storage_tiers where id = 'archived-tier-flat'`).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(ContainSubstring(`"status"`))

		var description, backendID, specProtocol string
		var encryptionEnabled, backendHasProtocol, backendHasQuota bool
		err = conn.QueryRow(ctx,
			`select data->'spec'->>'description', data->'spec'->'backends'->0->>'backend_id',
			        (data->'spec'->'backends'->0->>'encryption_enabled')::boolean,
			        (data->'spec'->'backends'->0) ? 'protocol', (data->'spec'->'backends'->0) ? 'quota_gib',
			        data->'spec'->>'protocol'
			 from archived_storage_tiers where id = 'archived-tier-flat'`).Scan(
			&description, &backendID, &encryptionEnabled, &backendHasProtocol, &backendHasQuota, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(description).To(Equal("legacy tier"))
		Expect(backendID).To(Equal("sb-3"))
		Expect(encryptionEnabled).To(BeTrue())
		Expect(backendHasProtocol).To(BeFalse())
		Expect(backendHasQuota).To(BeFalse())
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_NFS"))

		// "state" must round-trip as its string enum name here, never a bare int.
		var state string
		err = conn.QueryRow(ctx,
			`select data->'status'->>'state' from archived_storage_tiers where id = 'archived-tier-flat'`).Scan(&state)
		Expect(err).ToNot(HaveOccurred())
		Expect(state).To(Equal("STORAGE_TIER_STATE_ACTIVE"))
	})

	It("Defaults state to zero for a pre-migration-77 archived row with no backend associations", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into archived_storage_tiers (id, tenant, data, creation_timestamp, deletion_timestamp)
			 values ('archived-tier-flat-empty', 'system', $1::jsonb, now(), now())`,
			`{"description":"never provisioned","backends":[]}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 102)
		Expect(err).ToNot(HaveOccurred())

		var stateText, specProtocol string
		err = conn.QueryRow(ctx,
			`select (data->'status'->'state')::text, data->'spec'->>'protocol'
			 from archived_storage_tiers where id = 'archived-tier-flat-empty'`).Scan(&stateText, &specProtocol)
		Expect(err).ToNot(HaveOccurred())
		Expect(stateText).To(Equal("0"))
		Expect(specProtocol).To(Equal("STORAGE_PROTOCOL_UNSPECIFIED"))

		var backends string
		err = conn.QueryRow(ctx,
			`select (data->'spec'->'backends')::text from archived_storage_tiers where id = 'archived-tier-flat-empty'`).Scan(&backends)
		Expect(err).ToNot(HaveOccurred())
		Expect(backends).To(Equal("[]"))
	})
})
