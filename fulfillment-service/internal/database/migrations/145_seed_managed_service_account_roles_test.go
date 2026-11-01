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
)

var _ = DescribeMigration("Seed managed service-account roles", func() {
	It("seeds tenant roles in shared and the CSI role in system", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 145)).To(Succeed())
		for _, role := range []struct{ name, tenant string }{
			{"tenant-admin", "shared"},
			{"tenant-automation", "shared"},
			{"tenant-idp-manager", "shared"},
			{"osac-csi-driver", "system"},
		} {
			var count int
			err := conn.QueryRow(ctx, `
				select count(*) from roles
				where name = $1 and tenant = $2 and deletion_timestamp = 'epoch'`, role.name, role.tenant,
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1), "role %q must be seeded exactly once in %q", role.name, role.tenant)
		}
		var sharedCSI int
		Expect(conn.QueryRow(ctx, `select count(*) from roles where name = 'osac-csi-driver' and tenant = 'shared'`).
			Scan(&sharedCSI)).To(Succeed())
		Expect(sharedCSI).To(BeZero())
	})

	It("marks the managed role catalog entries ready", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 145)).To(Succeed())
		for _, role := range []struct{ name, tenant string }{
			{"tenant-automation", "shared"},
			{"tenant-idp-manager", "shared"},
			{"osac-csi-driver", "system"},
		} {
			var state string
			err := conn.QueryRow(ctx, `
				select data #>> '{status,state}' from roles
				where name = $1 and tenant = $2 and deletion_timestamp = 'epoch'`, role.name, role.tenant,
			).Scan(&state)
			Expect(err).ToNot(HaveOccurred())
			Expect(state).To(Equal("ROLE_STATE_READY"), "role %q must be ready", role.name)
		}
	})

	It("skips a seed when the role name already exists in any tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into roles (id, name, tenant, data, deletion_timestamp)
			values ('existing-tenant-automation', 'tenant-automation', 'system', '{}', now())`)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 145)).To(Succeed())

		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from roles where name = 'tenant-automation'`).Scan(&count)).
			To(Succeed())
		Expect(count).To(Equal(1))

		var tenant string
		var active bool
		Expect(conn.QueryRow(ctx, `select tenant, deletion_timestamp = 'epoch' from roles where name = 'tenant-automation'`).
			Scan(&tenant, &active)).To(Succeed())
		Expect(tenant).To(Equal("system"))
		Expect(active).To(BeFalse())
	})
})
