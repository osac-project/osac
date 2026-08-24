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

var _ = DescribeMigration("Add instance types name check", func() {
	It("Applies successfully", func(ctx context.Context) {
		err := tool.Migrate(ctx, 108)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects an empty-name insert", func(ctx context.Context) {
		err := tool.Migrate(ctx, 108)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-empty", "", "system", `{"spec":{}}`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("instance_types_name_not_empty"))
	})

	It("Accepts a normal insert", func(ctx context.Context) {
		err := tool.Migrate(ctx, 108)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"standard-4-16", "standard-4-16", "system", `{"spec":{"cores":4,"memory_gib":16}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `
			select name from instance_types where id = $1`,
			"standard-4-16",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("standard-4-16"))
	})

	It("Fails migration if empty-name rows exist", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"it-bad", "", "system", `{"spec":{}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 108)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty name"))
	})
})
