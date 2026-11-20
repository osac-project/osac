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

var _ = DescribeMigration("User grant lookup indexes", func() {
	It("adds both live subject lookup indexes", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 151)).To(Succeed())
		for name, fragments := range map[string][]string{
			"users_by_live_username":        {"ON public.users USING btree", "tenant", "username", "INCLUDE (id, name)"},
			"role_bindings_by_user_subject": {"ON public.role_bindings USING gin", "users", "jsonb_path_ops"},
		} {
			var definition string
			Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes where indexname = $1`, name).
				Scan(&definition)).To(Succeed())
			for _, fragment := range fragments {
				Expect(definition).To(ContainSubstring(fragment))
			}
			Expect(definition).To(ContainSubstring("WHERE (deletion_timestamp = '1970-01-01"))
		}
	})
})
