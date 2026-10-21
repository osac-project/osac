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
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add BMaaS SSH-key Secret reference trigger", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 135)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('bmaas-test-tenant', 'bmaas-test-tenant', 'bmaas-test-tenant', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects an active BMaaS instance that references a missing Secret", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('bmaas-missing-secret', 'bmaas-missing-secret', 'bmaas-test-tenant',
			        '{"spec":{"ssh_key":{"id":"missing-secret"}}}')`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
	})

	It("allows deleting a Secret referenced by an active BMaaS instance", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, name, tenant, data)
			values ('bmaas-deletable-secret', 'bmaas-deletable-secret', 'bmaas-test-tenant',
			        '{"backend":"SECRET_BACKEND_VAULT"}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('bmaas-referencing-instance', 'bmaas-referencing-instance', 'bmaas-test-tenant',
			        '{"spec":{"ssh_key":{"id":"bmaas-deletable-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update secrets set deletion_timestamp = now() where id = 'bmaas-deletable-secret'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows updating an active BMaaS instance after its Secret was deleted", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, name, tenant, data)
			values ('bmaas-stale-secret', 'bmaas-stale-secret', 'bmaas-test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('bmaas-stale-ref-instance', 'bmaas-stale-ref-instance', 'bmaas-test-tenant',
			        '{"spec":{"ssh_key":{"id":"bmaas-stale-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update secrets set deletion_timestamp = now() where id = 'bmaas-stale-secret'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update bare_metal_instances
			set data = data || '{"status":{
				"state":"BARE_METAL_INSTANCE_STATE_FAILED",
				"state_transition_time":"2026-01-01T10:00:00Z"
			}}'::jsonb
			where id = 'bmaas-stale-ref-instance'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows deleting a BMaaS instance whose Secret was deleted", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into secrets (id, name, tenant, data)
			values ('bmaas-stale-del-secret', 'bmaas-stale-del-secret', 'bmaas-test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('bmaas-stale-del-instance', 'bmaas-stale-del-instance', 'bmaas-test-tenant',
			        '{"spec":{"ssh_key":{"id":"bmaas-stale-del-secret"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update secrets set deletion_timestamp = now() where id = 'bmaas-stale-del-secret'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update bare_metal_instances set deletion_timestamp = now()
			where id = 'bmaas-stale-del-instance'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
