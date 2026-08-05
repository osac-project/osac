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

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add virtual network child ref triggers", func() {
	insertTenant := func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Creates the 'check_virtual_network_not_in_use' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_virtual_network_not_in_use' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a delete trigger to the virtual_networks table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_virtual_network_not_in_use' and
				event_object_table = 'virtual_networks' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates indexes for child virtual_network lookups", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())

		type idxExpectation struct {
			name  string
			table string
		}
		for _, tc := range []idxExpectation{
			{name: "subnets_by_virtual_network", table: "subnets"},
			{name: "security_groups_by_virtual_network", table: "security_groups"},
		} {
			var indexDef string
			err = conn.QueryRow(ctx, `
				select indexdef
				from pg_indexes
				where indexname = $1
				  and tablename = $2
			`, tc.name, tc.table).Scan(&indexDef)
			Expect(err).ToNot(HaveOccurred())
			Expect(indexDef).To(ContainSubstring("(data -> 'spec'::text) ->> 'virtual_network'::text"))
			Expect(indexDef).To(ContainSubstring("WHERE (deletion_timestamp = '1970-01-01 00:00:00+00'::timestamp with time zone)"))
		}
	})

	It("Prevents soft-deleting a virtual network referenced by a subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"vn-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("vn-1"))
		Expect(pgErr.Message).To(ContainSubstring("Subnet"))
	})

	It("Prevents soft-deleting a virtual network referenced by a security group", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-2', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into security_groups (id, tenant, data)
			 values ('sg-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"vn-2"}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-2'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("vn-2"))
		Expect(pgErr.Message).To(ContainSubstring("SecurityGroup"))
	})

	It("Reports the count of referencing subnets", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		for i := range 3 {
			_, err = conn.Exec(ctx,
				`insert into subnets (id, tenant, data)
				 values ($1, 'test-tenant', $2::jsonb)`,
				fmt.Sprintf("subnet-%d", i),
				fmt.Sprintf(`{"spec":{"virtual_network":"vn-3","ipv4_cidr":"10.0.%d.0/24"}}`, i))
			Expect(err).ToNot(HaveOccurred())
		}

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-3'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("3 Subnet"))
	})

	It("Allows soft-deleting a virtual network with no child references", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-4', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-4'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a virtual network when child subnets are already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-5', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-del', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"vn-5"}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-del'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-5'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the subnet virtual_network insert trigger", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_subnet_virtual_network_ref' and
				event_object_table = 'subnets' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents creating a subnet referencing a non-existent virtual network", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-ref', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"no-such-vn"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-vn"))
	})

	It("Prevents creating a subnet referencing a soft-deleted virtual network", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-ref-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"vn-deleted"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("vn-deleted"))
	})

	It("Creates the security group virtual_network insert trigger", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_security_group_virtual_network_ref' and
				event_object_table = 'security_groups' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents creating a security group referencing a non-existent virtual network", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into security_groups (id, tenant, data)
			 values ('sg-ref-missing', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"no-such-vn"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-vn"))
	})

	It("Prevents creating a security group referencing a soft-deleted virtual network", func(ctx context.Context) {
		err := tool.Migrate(ctx, 55)
		Expect(err).ToNot(HaveOccurred())
		insertTenant(ctx)

		_, err = conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data)
			 values ('vn-sg-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update virtual_networks set deletion_timestamp = now() where id = 'vn-sg-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into security_groups (id, tenant, data)
			 values ('sg-ref', 'test-tenant', $1::jsonb)`,
			`{"spec":{"virtual_network":"vn-sg-deleted"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("vn-sg-deleted"))
	})
})
