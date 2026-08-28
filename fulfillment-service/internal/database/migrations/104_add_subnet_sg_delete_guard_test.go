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

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add Subnet SG delete guard", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 104)
		Expect(err).ToNot(HaveOccurred())
	})

	insertVN := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, name, tenant, data) values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertSubnet := func(ctx context.Context, id, vnID string) {
		_, err := conn.Exec(ctx,
			`insert into subnets (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"virtual_network":{"id":"`+vnID+`"}}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	insertSG := func(ctx context.Context, id, vnID string) {
		_, err := conn.Exec(ctx,
			`insert into security_groups (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"virtual_network":{"id":"`+vnID+`"}}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	softDelete := func(ctx context.Context, table, id string) error {
		_, err := conn.Exec(ctx,
			`update `+table+` set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectPgErr := func(err error, code string) *pgconn.PgError {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal(code))
		return pgErr
	}

	It("Prevents deleting a Subnet when active SecurityGroups exist on the same VirtualNetwork", func(ctx context.Context) {
		insertVN(ctx, "vn-1")
		insertSubnet(ctx, "subnet-1", "vn-1")
		insertSG(ctx, "sg-1", "vn-1")

		err := softDelete(ctx, "subnets", "subnet-1")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("subnet-1"))
		Expect(pgErr.Message).To(ContainSubstring("SecurityGroup"))
	})

	It("Allows deleting a Subnet when SecurityGroups on the same VN are soft-deleted", func(ctx context.Context) {
		insertVN(ctx, "vn-2")
		insertSubnet(ctx, "subnet-2", "vn-2")
		insertSG(ctx, "sg-2", "vn-2")

		err := softDelete(ctx, "security_groups", "sg-2")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "subnets", "subnet-2")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting a Subnet when no SecurityGroups exist on the VN", func(ctx context.Context) {
		insertVN(ctx, "vn-3")
		insertSubnet(ctx, "subnet-3", "vn-3")

		err := softDelete(ctx, "subnets", "subnet-3")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting a Subnet when SecurityGroups exist on a different VN", func(ctx context.Context) {
		insertVN(ctx, "vn-4")
		insertVN(ctx, "vn-5")
		insertSubnet(ctx, "subnet-4", "vn-4")
		insertSG(ctx, "sg-4", "vn-5")

		err := softDelete(ctx, "subnets", "subnet-4")
		Expect(err).ToNot(HaveOccurred())
	})
})
