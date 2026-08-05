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

var _ = DescribeMigration("Add instance type ref triggers", func() {
	It("Creates the 'check_instance_type_not_in_use' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_instance_type_not_in_use' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the instance_types table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_instance_type_not_in_use' and
				event_object_table = 'instance_types' and
				action_timing = 'BEFORE' and
				event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the partial index on compute_instances instance_type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				pg_indexes
			where
				tablename = 'compute_instances' and
				indexname = 'compute_instances_instance_type'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Prevents soft-deleting an instance type that is in use by a compute instance", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
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

		// Try to soft-delete the instance type — should fail:
		_, err = conn.Exec(ctx,
			`update instance_types set deletion_timestamp = now() where id = 'standard-4-16'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("standard-4-16"))
		Expect(pgErr.Message).To(ContainSubstring("ci-1"))
	})

	It("Allows soft-deleting an instance type that is not in use", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create an instance type with no compute instances:
		_, err = conn.Exec(ctx,
			`insert into instance_types (id, name, tenant, data)
			 values ('unused-type', 'unused-type', 'test-tenant', '{"spec":{"cores":2,"memoryGib":8}}')`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed:
		_, err = conn.Exec(ctx,
			`update instance_types set deletion_timestamp = now() where id = 'unused-type'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting an instance type when the compute instance is already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create an instance type:
		_, err = conn.Exec(ctx,
			`insert into instance_types (id, name, tenant, data)
			 values ('type-with-deleted-ci', 'type-with-deleted-ci', 'test-tenant', '{"spec":{"cores":4,"memoryGib":16}}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a soft-deleted compute instance referencing the instance type:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, deletion_timestamp, data)
			 values ('ci-deleted', 'test-tenant', now(), $1::jsonb)`,
			`{"spec":{"instance_type":"type-with-deleted-ci"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Soft-delete should succeed since the compute instance is already deleted:
		_, err = conn.Exec(ctx,
			`update instance_types set deletion_timestamp = now() where id = 'type-with-deleted-ci'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the 'check_compute_instance_instance_type_ref' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.routines
			where
				routine_name = 'check_compute_instance_instance_type_ref' and
				routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds a trigger to the compute_instances table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select
				count(*)
			from
				information_schema.triggers
			where
				trigger_name = 'check_compute_instance_instance_type_ref' and
				event_object_table = 'compute_instances' and
				action_timing = 'BEFORE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically(">=", 1))
	})

	It("Prevents creating a compute instance referencing a non-existent instance type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing an instance type that doesn't exist:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-bad-ref', 'test-tenant', $1::jsonb)`,
			`{"spec":{"instance_type":"no-such-type"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-type"))
	})

	It("Prevents creating a compute instance referencing a soft-deleted instance type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create and soft-delete an instance type:
		_, err = conn.Exec(ctx,
			`insert into instance_types (id, name, tenant, data)
			 values ('deleted-type', 'deleted-type', 'test-tenant', '{"spec":{"cores":2,"memoryGib":8}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`update instance_types set deletion_timestamp = now() where id = 'deleted-type'`)
		Expect(err).ToNot(HaveOccurred())

		// Try to create a compute instance referencing the deleted instance type:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-deleted-ref', 'test-tenant', $1::jsonb)`,
			`{"spec":{"instance_type":"deleted-type"}}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("deleted-type"))
	})

	It("Allows creating a compute instance with no instance type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance with no instance type field:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-no-type', 'test-tenant', '{"spec":{}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows creating a compute instance referencing a valid instance type", func(ctx context.Context) {
		err := tool.Migrate(ctx, 56)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenant:
		_, err = conn.Exec(ctx,
			`insert into organizations (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'system', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		// Create an instance type:
		_, err = conn.Exec(ctx,
			`insert into instance_types (id, name, tenant, data)
			 values ('valid-type', 'valid-type', 'test-tenant', '{"spec":{"cores":4,"memoryGib":16}}')`)
		Expect(err).ToNot(HaveOccurred())

		// Create a compute instance referencing it:
		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, tenant, data)
			 values ('ci-valid-ref', 'test-tenant', $1::jsonb)`,
			`{"spec":{"instance_type":"valid-type"}}`)
		Expect(err).ToNot(HaveOccurred())
	})
})
