/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("RoleBinding service-account subject index", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 142)).To(Succeed())
	})

	It("indexes the service-account subject list", func(ctx context.Context) {
		var definition string
		err := conn.QueryRow(ctx, `
			select indexdef
			from pg_indexes
			where tablename = 'role_bindings'
			  and indexname = 'role_bindings_by_service_account_subject'`).Scan(&definition)
		Expect(err).ToNot(HaveOccurred())
		Expect(definition).To(ContainSubstring("USING gin"))
		Expect(definition).To(ContainSubstring("service_accounts"))
	})
})
