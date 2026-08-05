/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create NAT gateways tables", func() {
	DescribeTable(
		"Creates the expected tables",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 73)
			Expect(err).ToNot(HaveOccurred())

			quotedTable := pgx.Identifier{table}.Sanitize()

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"test-id", "system", `{}`,
			)
			Expect(err).ToNot(HaveOccurred())

			var count int
			err = conn.QueryRow(ctx,
				fmt.Sprintf(`select count(*) from %s where id = $1`, quotedTable),
				"test-id",
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1))

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"bad-tenant-id", "no-such-tenant", `{}`,
			)
			Expect(err).To(HaveOccurred())
		},
		Entry("nat_gateways", "nat_gateways"),
	)

	insertVN := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data) values ($1, $2, $3)`,
			id, "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertNATGateway := func(ctx context.Context, id, virtualNetwork, externalIP string) error {
		_, err := conn.Exec(ctx,
			`insert into nat_gateways (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec":{"virtual_network":"`+virtualNetwork+`","external_ip":"`+externalIP+`"}}`,
		)
		return err
	}

	softDeleteNATGateway := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`update nat_gateways set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects duplicate active NATGateway for same VirtualNetwork", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-1")

		err = insertNATGateway(ctx, "ng-1", "vn-1", "eip-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-2", "vn-1", "eip-2")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	It("Allows same VirtualNetwork after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-2")

		err = insertNATGateway(ctx, "ng-3", "vn-2", "eip-3")
		Expect(err).ToNot(HaveOccurred())

		softDeleteNATGateway(ctx, "ng-3")

		err = insertNATGateway(ctx, "ng-4", "vn-2", "eip-4")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows different VirtualNetworks", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-3")
		insertVN(ctx, "vn-4")

		err = insertNATGateway(ctx, "ng-5", "vn-3", "eip-5")
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-6", "vn-4", "eip-6")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects NATGateway referencing a non-existent VirtualNetwork", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-7", "no-such-vn", "eip-7")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-vn"))
	})

	It("Rejects NATGateway referencing a soft-deleted VirtualNetwork", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-deleted")
		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-8", "vn-deleted", "eip-8")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("vn-deleted"))
	})

	It("Prevents soft-deleting a VirtualNetwork with active NATGateway", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-protected")

		err = insertNATGateway(ctx, "ng-9", "vn-protected", "eip-9")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-protected'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("vn-protected"))
		Expect(pgErr.Message).To(ContainSubstring("NATGateway"))
	})

	It("Allows soft-deleting VirtualNetwork after NATGateway is soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 73)
		Expect(err).ToNot(HaveOccurred())

		insertVN(ctx, "vn-released")

		err = insertNATGateway(ctx, "ng-10", "vn-released", "eip-10")
		Expect(err).ToNot(HaveOccurred())

		softDeleteNATGateway(ctx, "ng-10")

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-released'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
