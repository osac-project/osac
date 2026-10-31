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
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Allow unused Subnet deletion with SecurityGroups", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 142)).To(Succeed())
		_, err := conn.Exec(ctx, `
            insert into virtual_networks (id, name, tenant, data)
            values ('vn', 'vn', 'system', '{}');
            insert into subnets (id, name, tenant, data) values
            ('target', 'target', 'system', '{"spec":{"virtual_network":{"id":"vn"}}}'),
            ('owner', 'owner', 'system', '{"spec":{"virtual_network":{"id":"vn"}}}');
            insert into security_groups (id, name, tenant, data)
            values ('sg', 'sg', 'system', '{"spec":{"virtual_network":{"id":"vn"}}}');
        `)
		Expect(err).ToNot(HaveOccurred())
	})

	It("deletes an unused sibling while the owner VM and its SecurityGroup remain active", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data)
            values ('vm', 'vm', 'system', '{"spec":{"network_attachments":[{"subnet":{"id":"owner"},"security_groups":[{"id":"sg"}]}]}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update subnets set deletion_timestamp = now() where id = 'target'`)
		Expect(err).ToNot(HaveOccurred())
		for table, id := range map[string]string{"subnets": "owner", "security_groups": "sg", "compute_instances": "vm"} {
			var active bool
			err = conn.QueryRow(ctx, fmt.Sprintf(`select deletion_timestamp = 'epoch' from %s where id = $1`, table), id).Scan(&active)
			Expect(err).ToNot(HaveOccurred())
			Expect(active).To(BeTrue(), "%s/%s must remain active", table, id)
		}
	})

	It("keeps a referenced SecurityGroup protected", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data)
            values ('vm', 'vm', 'system', '{"spec":{"network_attachments":[{"subnet":{"id":"owner"},"security_groups":[{"id":"sg"}]}]}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update security_groups set deletion_timestamp = now() where id = 'sg'`)
		var pgError *pgconn.PgError
		Expect(errors.As(err, &pgError)).To(BeTrue())
		Expect(pgError.Code).To(Equal("Z0003"))
	})

	DescribeTable("keeps direct workload references protected", func(ctx context.Context, table, data, consumer string) {
		_, err := conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('consumer', 'consumer', 'system', $1::jsonb)`, table), data)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update subnets set deletion_timestamp = now() where id = 'target'`)
		var pgError *pgconn.PgError
		Expect(errors.As(err, &pgError)).To(BeTrue())
		Expect(pgError.Code).To(Equal("Z0003"))
		Expect(pgError.Message).To(ContainSubstring(consumer))
	},
		Entry("ComputeInstance", "compute_instances", `{"spec":{"network_attachments":[{"subnet":{"id":"target"},"security_groups":[{"id":"sg"}]}]}}`, "compute instance"),
		Entry("Cluster", "clusters", `{"spec":{"network_attachment":{"subnet":{"id":"target"}}}}`, "cluster"),
		Entry("BareMetalInstance", "bare_metal_instances", `{"spec":{"network_attachments":[{"subnet":{"id":"target"}}]}}`, "bare metal instance"),
	)

	DescribeTable("keeps catalog subnet policies protected", func(ctx context.Context, table, field, value, policy string) {
		branch := fmt.Sprintf(`{"locked":%s}`, value)
		if policy == "default" {
			branch = fmt.Sprintf(`{"editable":{"default_value":%s}}`, value)
		}
		data := fmt.Sprintf(`{"fields":{"%s":%s}}`, field, branch)
		_, err := conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('catalog', 'catalog', 'system', $1::jsonb)`, table), data)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update subnets set deletion_timestamp = now() where id = 'target'`)
		var pgError *pgconn.PgError
		Expect(errors.As(err, &pgError)).To(BeTrue())
		Expect(pgError.Code).To(Equal("Z0003"))
		Expect(pgError.Message).To(ContainSubstring("catalog policy"))
	},
		Entry("compute locked", "compute_instance_catalog_items", "network_attachments", `{"items":[{"subnet":{"id":"target"}}]}`, "locked"),
		Entry("compute default", "compute_instance_catalog_items", "network_attachments", `{"items":[{"subnet":{"id":"target"}}]}`, "default"),
		Entry("cluster locked", "cluster_catalog_items", "network_attachment", `{"subnet":{"id":"target"}}`, "locked"),
		Entry("cluster default", "cluster_catalog_items", "network_attachment", `{"subnet":{"id":"target"}}`, "default"),
		Entry("bare metal locked", "bare_metal_instance_catalog_items", "network_attachments", `{"items":[{"subnet":{"id":"target"}}]}`, "locked"),
		Entry("bare metal default", "bare_metal_instance_catalog_items", "network_attachments", `{"items":[{"subnet":{"id":"target"}}]}`, "default"),
	)
})
