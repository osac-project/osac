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

var _ = DescribeMigration("Restructure storage tier spec status", func() {
	insertBackend := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data)
			 values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Updates trigger functions to read from spec.backends path", func(ctx context.Context) {
		err := tool.Migrate(ctx, 77)
		Expect(err).ToNot(HaveOccurred())

		insertBackend(ctx, "sb-1")

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-1', 'tier-1', 'system', $1::jsonb)`,
			`{"spec":{"backends":[{"backend_id":"sb-1"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from storage_tier_backends
			where storage_tier_id = 'tier-1'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Validates backend refs from the spec.backends path", func(ctx context.Context) {
		err := tool.Migrate(ctx, 77)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-bad', 'tier-bad', 'system', $1::jsonb)`,
			`{"spec":{"backends":[{"backend_id":"no-such-backend"}]}}`)
		Expect(err).To(HaveOccurred())
	})

	It("Backfills existing rows from flat to spec/status structure", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		insertBackend(ctx, "sb-backfill")

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-old', 'tier-old', 'system', $1::jsonb)`,
			`{"description":"old tier","backends":[{"backend_id":"sb-backfill"}],"state":1}`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 77)
		Expect(err).ToNot(HaveOccurred())

		var data string
		err = conn.QueryRow(ctx,
			`select data::text from storage_tiers where id = 'tier-old'`).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(ContainSubstring(`"spec"`))
		Expect(data).To(ContainSubstring(`"status"`))
		Expect(data).To(ContainSubstring(`"sb-backfill"`))
	})
})
