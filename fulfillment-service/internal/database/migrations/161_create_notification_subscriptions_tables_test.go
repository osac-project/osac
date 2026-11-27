/*
Copyright (c) 2026 Red Hat, Inc.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create notification_subscriptions tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 161)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
            values ('sub-tenant-a', 'sub-tenant-a', 'sub-tenant-a', 'system', '{}'),
                   ('sub-tenant-b', 'sub-tenant-b', 'sub-tenant-b', 'system', '{}')
            on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enforces unique names per tenant and allows reuse after soft delete", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notification_subscriptions (id, name, tenant, data)
            values ('s1', 'quota-warn', 'sub-tenant-a', '{}'),
                   ('s2', 'quota-warn', 'sub-tenant-b', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notification_subscriptions (id, name, tenant, data)
            values ('s3', 'quota-warn', 'sub-tenant-a', '{}')`)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx, `update notification_subscriptions set deletion_timestamp = now() where id = 's1'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notification_subscriptions (id, name, tenant, data)
            values ('s4', 'quota-warn', 'sub-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows updating spec.paused, filter, and webhook_url", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notification_subscriptions (id, name, tenant, data) values (
			's1', 'quota-warn', 'sub-tenant-a',
			'{"spec":{"webhook_url":"https://hooks.example/osac","filter":"kind == \"quota\"","paused":false},"status":{"signing_secret":{"id":"sec-1","name":"whsec-quota-warn"}}}'
		)`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notification_subscriptions set data = jsonb_set(data, '{spec,paused}', 'true') where id = 's1'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update notification_subscriptions set data = jsonb_set(data, '{spec,filter}', '"severity == \"critical\""') where id = 's1'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update notification_subscriptions set data = jsonb_set(data, '{spec,webhook_url}', '"https://hooks.example/other"') where id = 's1'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update notification_subscriptions set data = jsonb_set(data, '{status,auto_paused}', 'true') where id = 's1'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects mutating status.signing_secret", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notification_subscriptions (id, name, tenant, data) values (
			's1', 'quota-warn', 'sub-tenant-a',
			'{"spec":{"webhook_url":"https://hooks.example/osac","filter":"true","paused":false},"status":{"signing_secret":{"id":"sec-1","name":"whsec-quota-warn"}}}'
		)`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notification_subscriptions set data = jsonb_set(data, '{status,signing_secret,id}', '"sec-other"') where id = 's1'`)
		Expect(err).To(HaveOccurred())
	})
})
