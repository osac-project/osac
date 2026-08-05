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

var _ = DescribeMigration("Remove legacy cluster template seed", func() {
	It("Deletes the ocp_4_17_small seed row", func(ctx context.Context) {
		err := tool.Migrate(ctx, 83)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx,
			`select count(*) from cluster_templates where id = 'ocp_4_17_small'`,
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Is idempotent when the row does not exist", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`delete from cluster_templates where id = 'ocp_4_17_small'`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 83)
		Expect(err).ToNot(HaveOccurred())
	})
})
