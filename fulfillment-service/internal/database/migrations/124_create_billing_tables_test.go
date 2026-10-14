/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

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

var _ = DescribeMigration("Create billing account and assignment tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 124)).To(Succeed())
		for _, tenant := range []string{"tenant-a", "tenant-b"} {
			_, err := conn.Exec(ctx, `
				insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}'::jsonb)
			`, tenant)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, account := range []string{"account-a", "account-b"} {
			_, err := conn.Exec(ctx, `
				insert into billing_accounts (
					id, provider_key, provider_account_reference, display_name, state, status_observed_at
				) values ($1, 'm360', $1, $1, 'ACTIVE', now())
			`, account)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("enforces one active assignment per tenant and billing account", func(ctx context.Context) {
		insertAssignment := func(id, tenant, account string) error {
			_, err := conn.Exec(ctx, `
				insert into tenant_billing_assignments (id, name, tenant, creator, data)
				values ($1, $1, $2, 'system', jsonb_build_object(
					'spec', jsonb_build_object('tenant_id', $2, 'billing_account_id', $3),
					'status', jsonb_build_object('connection_state', 1)
				))
			`, id, tenant, account)
			return err
		}

		Expect(insertAssignment("assignment-1", "tenant-a", "account-a")).To(Succeed())
		Expect(insertAssignment("assignment-2", "tenant-a", "account-b")).To(HaveOccurred())
		Expect(insertAssignment("assignment-3", "tenant-b", "account-a")).To(HaveOccurred())

		_, err := conn.Exec(ctx, `
			update tenant_billing_assignments
			set data = jsonb_set(data, '{status,effective_to}', '"2026-10-12T12:00:00Z"'::jsonb)
			where id = 'assignment-1'
		`)
		Expect(err).NotTo(HaveOccurred())
		Expect(insertAssignment("assignment-3", "tenant-b", "account-a")).To(Succeed())
	})
})
