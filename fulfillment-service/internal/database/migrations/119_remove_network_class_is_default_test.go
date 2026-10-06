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

var _ = DescribeMigration("Remove NetworkClass is_default", func() {
	It("removes persisted flags and the default-only index", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into network_classes (id, tenant, name, data)
			values ('network-class-legacy', 'shared', 'legacy', '{"is_default":true,"title":"legacy"}'::jsonb)
		`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 119)).To(Succeed())

		var data []byte
		err = conn.QueryRow(ctx, `select data from network_classes where id = 'network-class-legacy'`).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(MatchJSON(`{"title":"legacy"}`))

		var count int
		err = conn.QueryRow(ctx,
			`select count(*) from pg_indexes where indexname = 'network_classes_single_default'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())

		_, err = conn.Exec(ctx, `delete from network_classes where id = 'network-class-legacy'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
