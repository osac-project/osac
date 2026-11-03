/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Subnet delete guard and SecurityGroup dependencies", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 144)).To(Succeed())
	})

	It("allows deleting a Subnet while preserving the SecurityGroup guard on its VirtualNetwork", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, name, tenant, data) values ('vn-1', 'vn-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into subnets (id, name, tenant, data) values ('subnet-1', 'subnet-1', 'system', $1::jsonb)`,
			`{"spec":{"virtual_network":{"id":"vn-1"}}}`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into security_groups (id, name, tenant, data) values ('sg-1', 'sg-1', 'system', $1::jsonb)`,
			`{"spec":{"virtual_network":{"id":"vn-1"}}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `update subnets set deletion_timestamp = now() where id = 'subnet-1'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `update virtual_networks set deletion_timestamp = now() where id = 'vn-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("SecurityGroup"))
	})
})
