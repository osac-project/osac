//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create Notification tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
            values ('notif-tenant-a', 'notif-tenant-a', 'notif-tenant-a', 'system', '{}'),
                   ('notif-tenant-b', 'notif-tenant-b', 'notif-tenant-b', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enforces unique names per tenant and allows reuse after soft delete", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n1', 'inbox-u1', 'notif-tenant-a', '{}'),
                   ('n2', 'inbox-u1', 'notif-tenant-b', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n3', 'inbox-u1', 'notif-tenant-a', '{}')`)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx, `update notifications set deletion_timestamp = now() where id = 'n1'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n4', 'inbox-u1', 'notif-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("tracks active IDs and enqueues every committed row change", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n1', 'inbox-u1', 'notif-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from active_notifications where id = 'n1'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))

		_, err = conn.Exec(ctx, `update notifications set deletion_timestamp = now() where id = 'n1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from active_notifications where id = 'n1'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(conn.QueryRow(ctx,
			`select count(*) from changes where "table" = 'notifications' and data->>'id' = 'n1'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
	})

	It("enforces unique inbox identity per tenant event recipient", func(ctx context.Context) {
		payload := `{"spec":{"kind":"NOTIFICATION_KIND_EVENT","event":{"id":"e1","source":"svc"},"recipient_id":"u1"}}`
		_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n1', 'a', 'notif-tenant-a', $1)`, payload)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n2', 'b', 'notif-tenant-a', $1)`, payload)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n3', 'c', 'notif-tenant-b', $1)`, payload)
		Expect(err).ToNot(HaveOccurred())

		otherRecipient := `{"spec":{"kind":"NOTIFICATION_KIND_EVENT","event":{"id":"e1","source":"svc"},"recipient_id":"u2"}}`
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n4', 'd', 'notif-tenant-a', $1)`, otherRecipient)
		Expect(err).ToNot(HaveOccurred())

		ann := `{"spec":{"kind":"NOTIFICATION_KIND_ANNOUNCEMENT","scope":"NOTIFICATION_SCOPE_TENANT","title":"maint"}}`
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('a1', 'ann-1', 'notif-tenant-a', $1),
                   ('a2', 'ann-2', 'notif-tenant-a', $1)`, ann)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows updating spec.state but rejects other immutable spec fields", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, data) values (
			'n1', 'inbox-u1', 'notif-tenant-a',
			'{"spec":{"kind":"NOTIFICATION_KIND_EVENT","title":"hi","state":"NOTIFICATION_STATE_UNREAD"}}'
		)`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notifications set data = jsonb_set(data, '{spec,state}', '"NOTIFICATION_STATE_READ"') where id = 'n1'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update notifications set data = jsonb_set(data, '{spec,title}', '"nope"') where id = 'n1'`)
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects changes to identity and ownership",
		func(ctx context.Context, query, value string) {
			_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
                values ('n1', 'inbox-u1', 'notif-tenant-a', '{}')`)
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, query, value)
			Expect(err).To(HaveOccurred())
		},
		Entry("ID", "update notifications set id = $1 where id = 'n1'", "other"),
		Entry("name", "update notifications set name = $1 where id = 'n1'", "other"),
		Entry("tenant", "update notifications set tenant = $1 where id = 'n1'", "notif-tenant-b"),
		Entry("project", "update notifications set project = $1 where id = 'n1'", "team"),
	)

	It("rejects project-scoped creation and missing tenants", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into notifications (id, name, tenant, project, data)
            values ('n1', 'x', 'notif-tenant-a', 'team', '{}')`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into notifications (id, name, tenant, data)
            values ('n1', 'x', 'absent', '{}')`)
		Expect(err).To(HaveOccurred())
	})

	It("creates archive storage", func(ctx context.Context) {
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from archived_notifications`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})
})
