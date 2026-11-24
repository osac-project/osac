/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = DescribeMigration("Seed the shared Tenant default Role", func() {
	readData := func(ctx context.Context, id string) []byte {
		var data []byte
		Expect(conn.QueryRow(ctx, "select data from tenants where id = $1", id).Scan(&data)).To(Succeed())
		return data
	}

	It("sets only the shared default even when the spec is absent", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('acme', 'acme', 'acme', 'system', '{"spec":{"domains":["example.com"]}}')
		`)
		Expect(err).NotTo(HaveOccurred())
		systemBefore := readData(ctx, "system")
		acmeBefore := readData(ctx, "acme")

		Expect(tool.Migrate(ctx, 153)).To(Succeed())

		tenant := &privatev1.Tenant{}
		Expect(protojson.Unmarshal(readData(ctx, "shared"), tenant)).To(Succeed())
		ref := tenant.GetSpec().GetDefaultRole()
		var roleID string
		Expect(conn.QueryRow(ctx, "select id from roles where tenant = 'shared' and name = 'tenant-user'").
			Scan(&roleID)).To(Succeed())
		Expect(ref.GetId()).To(Equal(roleID))
		Expect(ref.GetName()).To(Equal("tenant-user"))
		Expect(ref.GetTenant()).To(Equal("shared"))
		Expect(ref.GetShared()).To(BeTrue())
		Expect(readData(ctx, "system")).To(MatchJSON(systemBefore))
		Expect(readData(ctx, "acme")).To(MatchJSON(acmeBefore))
	})

	It("preserves unrelated spec fields", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `update tenants set data = '{"spec":{"domains":["example.com"]}}' where id = 'shared'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		tenant := &privatev1.Tenant{}
		Expect(protojson.Unmarshal(readData(ctx, "shared"), tenant)).To(Succeed())
		Expect(tenant.GetSpec().GetDomains()).To(Equal([]string{"example.com"}))
		Expect(tenant.GetSpec().HasDefaultRole()).To(BeTrue())
	})

	It("preserves an existing selection", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			update tenants as t
			set data = jsonb_build_object('spec', jsonb_build_object('default_role',
				jsonb_build_object('id', r.id, 'name', r.name, 'tenant', r.tenant, 'shared', true)))
			from roles as r where t.id = 'shared' and r.name = 'tenant-reader' and r.tenant = 'shared'
		`)
		Expect(err).NotTo(HaveOccurred())
		before := readData(ctx, "shared")
		Expect(tool.Migrate(ctx, 153)).To(Succeed())
		Expect(readData(ctx, "shared")).To(MatchJSON(before))
	})
})
