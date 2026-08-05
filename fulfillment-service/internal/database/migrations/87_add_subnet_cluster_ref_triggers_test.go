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

var _ = DescribeMigration("Add subnet cluster ref triggers", func() {
	It("Creates the btree index on clusters network_attachment subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_indexes
			where
				tablename = 'clusters' and
				indexname = 'clusters_network_attachment_subnet'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the 'check_cluster_subnet_refs' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_cluster_subnet_refs' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a BEFORE INSERT trigger to the clusters table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_cluster_subnet_refs' and
				event_object_table = 'clusters' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents soft-deleting a subnet that is in use by a cluster", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-1', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-1"}}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-1"))
		Expect(pgErr.Message).To(ContainSubstring("cluster"))
	})

	It("Allows soft-deleting a subnet that is not in use by any cluster", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-2', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-2'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents soft-deleting a subnet when both a compute instance and a cluster reference it", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-shared', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"subnet-shared"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-shared"}}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-shared'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
	})

	It("Prevents creating a cluster referencing a non-existent subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"no-such-subnet"}}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-subnet"))
	})

	It("Prevents creating a cluster referencing a soft-deleted subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-deleted"}}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-deleted"))
	})

	It("Allows creating a cluster with no network_attachment", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-ref-3', 'test-tenant', '{"spec":{}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a subnet when the referencing cluster is already soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, deletion_timestamp, data)
			 values ('cluster-2', 'test-tenant', now(), $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-3"}}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-3'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a cluster with a valid subnet reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-valid', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-valid', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-valid"}}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a cluster with network_attachment that has security_groups alongside subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 87)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-sg', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data)
			 values ('cluster-sg', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachment":{"subnet":"subnet-sg","security_groups":["sg-1","sg-2"]}}}`)
		Expect(err).ToNot(HaveOccurred())
	})
})
