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

var _ = DescribeMigration("Remove leaked IDs from ref trigger errors", func() {
	It("Instance type in-use error does not include the compute instance ID", func(ctx context.Context) {
		err := tool.Migrate(ctx, 72)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create an instance type:
		_, err = conn.Exec(ctx,
			`insert into instance_types (id, name, tenant, data)
			 values ('standard-4-16', 'standard-4-16', 'test-tenant', '{"spec":{"cores":4,"memoryGib":16}}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing that instance type:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"instance_type":"standard-4-16"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the instance type — should fail without leaking the CI ID:
		_, err = conn.Exec(ctx,
			`update instance_types set deletion_timestamp = now() where id = 'standard-4-16'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("standard-4-16"))
		Expect(pgErr.Message).ToNot(ContainSubstring("ci-1"))
	})

	It("Subnet in-use error does not include the compute instance ID", func(ctx context.Context) {
		err := tool.Migrate(ctx, 72)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a subnet:
		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing that subnet:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"subnet-1"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the subnet — should fail without leaking the CI ID:
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-1"))
		Expect(pgErr.Message).ToNot(ContainSubstring("ci-1"))
	})

	It("Cluster catalog item in-use error does not include the cluster ID", func(ctx context.Context) {
		err := tool.Migrate(ctx, 72)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster catalog item:
		_, err = conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data)
			 values ('cci-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster referencing that catalog item:
		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cci-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail without leaking the cluster ID:
		_, err = conn.Exec(ctx,
			`update cluster_catalog_items set deletion_timestamp = now() where id = 'cci-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cci-1"))
		Expect(pgErr.Message).ToNot(ContainSubstring("cluster-1"))
	})

	It("Compute instance catalog item in-use error does not include the compute instance ID", func(ctx context.Context) {
		err := tool.Migrate(ctx, 72)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instance_catalog_items (id, tenant, data)
			 values ('cici-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing that catalog item:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('vm-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"catalog_item":"cici-1"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Try to soft-delete the catalog item — should fail without leaking the CI ID:
		_, err = conn.Exec(ctx,
			`update compute_instance_catalog_items set deletion_timestamp = now() where id = 'cici-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("cici-1"))
		Expect(pgErr.Message).ToNot(ContainSubstring("vm-1"))
	})
})
