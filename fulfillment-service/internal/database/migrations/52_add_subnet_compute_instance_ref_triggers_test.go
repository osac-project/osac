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

var _ = DescribeMigration("Add subnet compute instance ref triggers", func() {
	It("Creates the 'check_subnet_not_in_use' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_subnet_not_in_use' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the subnets table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_subnet_not_in_use' and
				event_object_table = 'subnets' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the GIN index on compute_instances network_attachments", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_indexes
			where
				tablename = 'compute_instances' and
				indexname = 'compute_instances_network_attachments'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents soft-deleting a subnet that is in use by a compute instance", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
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

		// Try to soft-delete the subnet — should fail:
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-1'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-1"))
		Expect(pgErr.Message).To(ContainSubstring("ci-1"))
	})

	It("Allows soft-deleting a subnet that is not in use", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a subnet with no compute instances:
		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-2', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed:
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-2'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prevents soft-deleting a subnet when compute instance has additional fields in network attachment", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a subnet:
		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-sg', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance with security_groups alongside the subnet:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-sg', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"subnet-sg","security_groups":["sg-1","sg-2"]}]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should fail — the JSONB containment operator matches on the subnet
		// field even when the network attachment object has additional fields:
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-sg'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-sg"))
		Expect(pgErr.Message).To(ContainSubstring("ci-sg"))
	})

	It("Creates the 'check_compute_instance_subnet_refs' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_compute_instance_subnet_refs' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the compute_instances table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_compute_instance_subnet_refs' and
				event_object_table = 'compute_instances' and
				action_timing = 'BEFORE' and
				event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents creating a compute instance referencing a non-existent subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing a subnet that doesn't exist:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"no-such-subnet"}]}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-subnet"))
	})

	It("Prevents creating a compute instance referencing a soft-deleted subnet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create and soft-delete a subnet:
		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-deleted', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing the deleted subnet:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"subnet-deleted"}]}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("subnet-deleted"))
	})

	It("Allows creating a compute instance with no network attachments", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance with no network attachments:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-ref-3', 'test-tenant', '{"spec":{}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a subnet when the compute instance is already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 52)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a subnet:
		_, err = conn.Exec(ctx,
			`insert into subnets (id, tenant, data)
			 values ('subnet-3', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a soft-deleted compute instance referencing the subnet:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, deletion_timestamp, data)
			 values ('ci-2', 'test-tenant', now(), $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":"subnet-3"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed since the compute instance is already deleted:
		_, err = conn.Exec(ctx,
			`update subnets set deletion_timestamp = now() where id = 'subnet-3'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
