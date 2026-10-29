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
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 141)).To(Succeed())
	})

	It("seeds each assignable role once in the shared catalog", func(ctx context.Context) {
		for _, name := range []string{
			"tenant-admin",
			"tenant-automation",
			"tenant-idp-manager",
			"osac-csi-driver",
		} {
			var count int
			err := conn.QueryRow(ctx, `
				select count(*) from roles
				where name = $1 and tenant = 'shared' and deletion_timestamp = 'epoch'`, name,
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1), "role %q must be seeded exactly once", name)
		}
	})

	It("marks the managed role catalog entries ready", func(ctx context.Context) {
		for _, name := range []string{"tenant-automation", "tenant-idp-manager", "osac-csi-driver"} {
			var state string
			err := conn.QueryRow(ctx, `
				select data #>> '{status,state}' from roles
				where name = $1 and tenant = 'shared' and deletion_timestamp = 'epoch'`, name,
			).Scan(&state)
			Expect(err).ToNot(HaveOccurred())
			Expect(state).To(Equal("ROLE_STATE_READY"), "role %q must be ready", name)
		}
	})
})
