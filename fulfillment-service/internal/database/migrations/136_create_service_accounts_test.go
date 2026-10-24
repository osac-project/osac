/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create ServiceAccount tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 136)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
            values ('sa-tenant-a', 'sa-tenant-a', 'sa-tenant-a', 'system', '{}'),
                   ('sa-tenant-b', 'sa-tenant-b', 'sa-tenant-b', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})
	It("reserves names within each tenant including while deletion is pending", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data)
            values ('a', 'automation', 'sa-tenant-a', '{}'), ('b', 'automation', 'sa-tenant-b', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update service_accounts set deletion_timestamp = now(), finalizers = '{fulfillment-controller}' where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data) values ('c', 'automation', 'sa-tenant-a', '{}')`)
		Expect(err).To(HaveOccurred())
	})
	It("tracks active IDs and enqueues every committed row change", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data) values ('a', 'automation', 'sa-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from active_service_accounts where id = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		_, err = conn.Exec(ctx, `update service_accounts set deletion_timestamp = now() where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from active_service_accounts where id = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(conn.QueryRow(ctx, `select count(*) from changes where "table" = 'service_accounts' and data->>'id' = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
	})
	DescribeTable("rejects changes to identity and ownership", func(ctx context.Context, query, value string) {
		_, err := conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data) values ('a', 'automation', 'sa-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, query, value)
		Expect(err).To(HaveOccurred())
	},
		Entry("ID", "update service_accounts set id = $1 where id = 'a'", "other"),
		Entry("name", "update service_accounts set name = $1 where id = 'a'", "other"),
		Entry("tenant", "update service_accounts set tenant = $1 where id = 'a'", "sa-tenant-b"),
		Entry("project", "update service_accounts set project = $1 where id = 'a'", "team"),
	)
	It("rejects project-scoped creation and missing tenants", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into service_accounts (id, name, tenant, project, data) values ('a', 'automation', 'sa-tenant-a', 'team', '{}')`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data) values ('a', 'automation', 'absent', '{}')`)
		Expect(err).To(HaveOccurred())
	})
	It("indexes unique nonempty client IDs without blocking initial reservations", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into service_accounts (id, name, tenant, data)
            values ('a', 'one', 'sa-tenant-a', '{}'), ('b', 'two', 'sa-tenant-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update service_accounts set data = '{"status":{"client_id":"osac-sa-example"},"spec":{"roles":[{"id":"grant"}]}}' where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update service_accounts set data = '{"status":{"client_id":"osac-sa-example"}}' where id = 'b'`)
		Expect(err).To(HaveOccurred())
		var id, grants string
		Expect(conn.QueryRow(ctx, `select id, data->'spec'->'roles'->0->>'id' from service_accounts where data->'status'->>'client_id' = $1`, "osac-sa-example").Scan(&id, &grants)).To(Succeed())
		Expect(id).To(Equal("a"))
		Expect(grants).To(Equal("grant"))
		var definition string
		Expect(conn.QueryRow(ctx, `select indexdef from pg_indexes where indexname = 'service_accounts_by_client_id'`).Scan(&definition)).To(Succeed())
		Expect(definition).To(ContainSubstring("UNIQUE INDEX"))
		Expect(definition).To(ContainSubstring("client_id"))
	})
	It("adds shared ready machine-only roles and archive storage", func(ctx context.Context) {
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from roles where name in ('tenant-automation', 'osac-csi-driver') and tenant = 'shared' and data->'status'->>'state' = 'ROLE_STATE_READY'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		Expect(conn.QueryRow(ctx, `select count(*) from archived_service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})
})
