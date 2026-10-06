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

var _ = DescribeMigration("Move watches to Kafka", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 118)).To(Succeed())
	})

	It("Removes the legacy notifications table", func(ctx context.Context) {
		var exists bool
		err := conn.QueryRow(ctx, `select to_regclass('public.notifications') is not null`).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("Writes signal changes for flagged no-op updates", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('acme', 'acme', 'acme', 'system', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into projects (id, tenant, project, name, creator, data)
			values ('p-1', 'acme', '', 'p-1', 'system', '{"spec":{"title":"Acme"}}')
		`)
		Expect(err).ToNot(HaveOccurred())

		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `set local osac.signal = 'on'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `
			update projects
			set version = version
			where id = 'p-1'
		`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.Commit(ctx)).To(Succeed())

		var op string
		var version int32
		err = conn.QueryRow(ctx, `
			select
				op,
				(data->>'version')::integer
			from
				changes
			where
				data->>'id' = 'p-1'
			order by
				id desc
			limit 1
		`).Scan(&op, &version)
		Expect(err).ToNot(HaveOccurred())
		Expect(op).To(Equal("SIGNAL"))
		Expect(version).To(Equal(int32(0)))

		// The setting is local to the signal transaction, so a later update remains a regular update.
		_, err = conn.Exec(ctx, `
			update projects
			set data = '{"spec":{"title":"Updated"}}'
			where id = 'p-1'
		`)
		Expect(err).ToNot(HaveOccurred())

		err = conn.QueryRow(ctx, `
			select
				op
			from
				changes
			where
				data->>'id' = 'p-1'
			order by
				id desc
			limit 1
		`).Scan(&op)
		Expect(err).ToNot(HaveOccurred())
		Expect(op).To(Equal("UPDATE"))
	})
})
