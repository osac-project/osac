/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add BMaaS SSH-key index", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 134)).To(Succeed())
	})

	It("creates the BMaaS SSH-key index", func(ctx context.Context) {
		var exists bool
		err := conn.QueryRow(ctx, `
			select exists (
				select 1 from pg_indexes
				where schemaname = current_schema()
				  and indexname = 'bare_metal_instances_ssh_key'
			)`).Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})
})
