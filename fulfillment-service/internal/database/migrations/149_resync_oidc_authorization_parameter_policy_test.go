/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at:

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

var _ = DescribeMigration("Resync OIDC authorization parameter policy", func() {
	It("resets active READY OIDC providers and emits versioned updates", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-1', 'tenant-1', 'tenant-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into identity_providers (id, name, creator, tenant, data, version, deletion_timestamp)
			values
				('ready-oidc', 'ready-oidc', 'system', 'tenant-1',
				 '{"spec":{"oidc":{}},"status":{"phase":"IDENTITY_PROVIDER_PHASE_READY","message":"synced"}}', 4, 'epoch'),
				('ready-non-oidc', 'ready-non-oidc', 'system', 'tenant-1',
				 '{"spec":{},"status":{"phase":"IDENTITY_PROVIDER_PHASE_READY","message":"synced"}}', 4, 'epoch'),
				('unknown-oidc', 'unknown-oidc', 'system', 'tenant-1',
				 '{"spec":{"oidc":{}},"status":{"phase":"IDENTITY_PROVIDER_PHASE_UNKNOWN"}}', 4, 'epoch'),
				('deleting-oidc', 'deleting-oidc', 'system', 'tenant-1',
				 '{"spec":{"oidc":{}},"status":{"phase":"IDENTITY_PROVIDER_PHASE_READY","message":"synced"}}', 4, now())`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 149)).To(Succeed())

		var phase, message string
		var version int
		err = conn.QueryRow(ctx, `
			select data->'status'->>'phase', data->'status'->>'message', version
			from identity_providers where id = 'ready-oidc'`).Scan(&phase, &message, &version)
		Expect(err).ToNot(HaveOccurred())
		Expect(phase).To(Equal("IDENTITY_PROVIDER_PHASE_UNKNOWN"))
		Expect(message).To(Equal(
			"Reconciliation required after OIDC authorization parameter forwarding update"))
		Expect(version).To(Equal(5))
		var hasResyncCondition bool
		err = conn.QueryRow(ctx, `
			select data->'status'->'conditions' @>
				'[{"type":"OIDCForwardParametersResync"}]'::jsonb
			from identity_providers where id = 'ready-oidc'`).Scan(&hasResyncCondition)
		Expect(err).ToNot(HaveOccurred())
		Expect(hasResyncCondition).To(BeTrue())

		var emittedUpdates int
		err = conn.QueryRow(ctx, `
			select count(*) from changes
			where "table" = 'identity_providers'
			  and op = 'UPDATE'
			  and data->>'id' = 'ready-oidc'
			  and data->'data'->'status'->>'phase' = 'IDENTITY_PROVIDER_PHASE_UNKNOWN'`).Scan(&emittedUpdates)
		Expect(err).ToNot(HaveOccurred())
		Expect(emittedUpdates).To(Equal(1))

		for _, id := range []string{"ready-non-oidc", "unknown-oidc", "deleting-oidc"} {
			err = conn.QueryRow(ctx, `
				select data->'status'->>'phase', version
				from identity_providers where id = $1`, id).Scan(&phase, &version)
			Expect(err).ToNot(HaveOccurred())
			if id == "ready-non-oidc" || id == "deleting-oidc" {
				Expect(phase).To(Equal("IDENTITY_PROVIDER_PHASE_READY"), id)
			} else {
				Expect(phase).To(Equal("IDENTITY_PROVIDER_PHASE_UNKNOWN"), id)
			}
			Expect(version).To(Equal(4), id)
		}
	})
})
