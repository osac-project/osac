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

var _ = DescribeMigration("Create FabricDomain tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 113)).To(Succeed())
	})

	It("persists active FabricDomain objects and materializes active ids", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into fabric_domains (id, name, tenant, data)
			values ($1, $2, $3, $4)`, "fd-1", "gpu-ew", "tenant-a", `{}`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `select count(*) from active_fabric_domains where id = $1`, "fd-1").Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("enforces active name uniqueness within a tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into fabric_domains (id, name, tenant, data)
			values ('fd-1', 'gpu-ew', 'tenant-a', '{}'), ('fd-2', 'gpu-ew', 'tenant-a', '{}')`)
		Expect(err).To(HaveOccurred())
	})
})
