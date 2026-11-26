//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create quota cluster link index", func() {
	It("creates the hosts cluster link index", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 156)).To(Succeed())
		var exists bool
		Expect(conn.QueryRow(ctx,
			`select exists (select 1 from pg_index
			where indexrelid = 'bare_metal_instances_by_cluster'::regclass)`).Scan(&exists)).To(Succeed())
		Expect(exists).To(BeTrue())
	})
})
