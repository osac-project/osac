/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Remove HostType resource", func() {
	It("removes the historical HostType tables", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 123)).To(Succeed())

		for _, table := range []string{"host_types", "archived_host_types", "active_host_types"} {
			var exists bool
			Expect(conn.QueryRow(ctx, "select to_regclass($1) is not null", table).Scan(&exists)).To(Succeed())
			Expect(exists).To(BeFalse(), table)
		}
	})
})
