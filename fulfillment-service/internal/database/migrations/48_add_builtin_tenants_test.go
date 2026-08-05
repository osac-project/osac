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

	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/osac-project/fulfillment-service/internal/auth"
)

var _ = DescribeMigration("Add builtin tenants", func() {
	DescribeTable(
		"Inserts builtin tenants",
		func(ctx context.Context, id string) {
			err := tool.Migrate(ctx, 48)
			Expect(err).ToNot(HaveOccurred())
			var (
				name    string
				tenant  string
				creator string
				data    []byte
			)
			row := conn.QueryRow(ctx,
				`
				select
					name,
					tenant,
					creator,
					data
				from
					organizations
				where
					id = $1
				`,
				id,
			)
			err = row.Scan(&name, &tenant, &creator, &data)
			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal(id))
			Expect(tenant).To(Equal(id))
			Expect(creator).To(Equal(auth.SystemTenant))
			Expect(data).To(MatchJSON(`{}`))
		},
		Entry("System", auth.SystemTenant),
		Entry("Shared", auth.SharedTenant),
	)
})
