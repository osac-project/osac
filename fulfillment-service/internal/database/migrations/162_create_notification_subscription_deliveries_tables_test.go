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

var _ = DescribeMigration("Create notification_subscription_deliveries tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 162)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
            values ('del-tenant-a', 'del-tenant-a', 'del-tenant-a', 'system', '{}')
            on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enforces unique match key per subscription and event", func(ctx context.Context) {
		data := `{"spec":{"subscription":{"id":"sub-1"},"event":{"id":"evt-1","source":"osac-billing"}},"status":{"state":"NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING"}}`
		_, err := conn.Exec(ctx, `insert into notification_subscription_deliveries (id, name, tenant, data)
            values ('d1', 'del-1', 'del-tenant-a', $1)`, data)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `insert into notification_subscription_deliveries (id, name, tenant, data)
            values ('d2', 'del-2', 'del-tenant-a', $1)`, data)
		Expect(err).To(HaveOccurred())
	})

	It("rejects mutating immutable spec.event", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notification_subscription_deliveries (id, name, tenant, data) values (
			'd1', 'del-1', 'del-tenant-a',
			'{"spec":{"subscription":{"id":"sub-1"},"event":{"id":"evt-1","source":"osac-billing"}},"status":{"state":"NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING"}}'
		)`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notification_subscription_deliveries set data = jsonb_set(data, '{status,state}', '"NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_DELIVERED"') where id = 'd1'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notification_subscription_deliveries set data = jsonb_set(data, '{spec,event,id}', '"other"') where id = 'd1'`)
		Expect(err).To(HaveOccurred())
	})
})
