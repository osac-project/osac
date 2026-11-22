/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create audit records table", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
	})

	It("stores audit records with their event ID and tenant metadata", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into audit_records (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"event-1", "tenant-a", `{"request_id":"request-1","action":"create"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var id, tenant, data string
		err = conn.QueryRow(ctx, `
			select id, tenant, data::text
			from audit_records
			where id = $1`,
			"event-1",
		).Scan(&id, &tenant, &data)
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(Equal("event-1"))
		Expect(tenant).To(Equal("tenant-a"))
		Expect(data).To(MatchJSON(`{"request_id":"request-1","action":"create"}`))
	})

	It("rejects duplicate stable event IDs", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into audit_records (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"event-1", "tenant-a", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into audit_records (id, tenant, data)
			values ($1, $2, $3::jsonb)`,
			"event-1", "tenant-b", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("requires a tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into audit_records (id, data)
			values ($1, $2::jsonb)`,
			"event-without-tenant", `{}`,
		)
		Expect(err).To(HaveOccurred())
	})
})
