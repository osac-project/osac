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

var _ = DescribeMigration("Protect disk images referenced by cluster versions", func() {
	It("Creates and removes the cluster version disk image index", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 117)).To(Succeed())

		var indexDefinition string
		err := conn.QueryRow(ctx, `
			select indexdef
			from pg_indexes
			where tablename = 'cluster_versions'
			  and indexname = 'cluster_versions_disk_image_id'
		`).Scan(&indexDefinition)
		Expect(err).ToNot(HaveOccurred())
		Expect(indexDefinition).To(ContainSubstring("disk_image"))

		Expect(tool.Migrate(ctx, 116)).To(Succeed())
		var count int
		err = conn.QueryRow(ctx,
			`select count(*) from pg_indexes where indexname = 'cluster_versions_disk_image_id'`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
	})
})
