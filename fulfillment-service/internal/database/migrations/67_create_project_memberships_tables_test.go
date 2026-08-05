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

var _ = DescribeMigration("Create project memberships tables", func() {
	It("Creates the project_memberships table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Verify the project_memberships table exists:
		var exists bool
		row := conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'project_memberships'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Verify the archived_project_memberships table exists:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from information_schema.tables
				where table_name = 'archived_project_memberships'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Can insert and query a project membership", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"test-tenant", "Test Tenant", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert a test project membership:
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"test-membership-id", "test-membership", "test-creator", "test-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Query it back:
		var name, creator, tenant string
		row := conn.QueryRow(
			ctx,
			`select name, creator, tenant from project_memberships where id = $1`,
			"test-membership-id",
		)
		err = row.Scan(&name, &creator, &tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("test-membership"))
		Expect(creator).To(Equal("test-creator"))
		Expect(tenant).To(Equal("test-tenant"))
	})

	It("Creates indexes on project_memberships table", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Verify indexes exist:
		var exists bool

		// Check index on name:
		row := conn.QueryRow(
			ctx,
			`select exists (
				select from pg_indexes
				where tablename = 'project_memberships'
				and indexname = 'project_memberships_by_name'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Check index on creator:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from pg_indexes
				where tablename = 'project_memberships'
				and indexname = 'project_memberships_by_creator'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Check index on tenant:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from pg_indexes
				where tablename = 'project_memberships'
				and indexname = 'project_memberships_by_tenant'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())

		// Check GIN index on labels:
		row = conn.QueryRow(
			ctx,
			`select exists (
				select from pg_indexes
				where tablename = 'project_memberships'
				and indexname = 'project_memberships_by_label'
			)`,
		)
		err = row.Scan(&exists)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("Enforces foreign key constraint on tenant", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to insert a project membership with non-existent tenant (should fail):
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"test-membership-id", "test-membership", "test-creator", "nonexistent-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).To(HaveOccurred())

		// Verify it's a foreign key violation:
		var pgErr *pgconn.PgError
		Expect(err).To(BeAssignableToTypeOf(pgErr))
		pgErr = func() *pgconn.PgError {
			target := &pgconn.PgError{}
			_ = errors.As(err, &target)
			return target
		}()
		Expect(pgErr.Code).To(Equal("23503")) // foreign_key_violation
		Expect(pgErr.ConstraintName).To(Equal("project_memberships_tenant_fk"))
	})

	It("Allows project memberships with same name in different tenants", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Create organizations first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"tenant-1", "Tenant 1", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"tenant-2", "Tenant 2", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert project membership in tenant1:
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-1", "user-project-membership", "user-1", "tenant-1", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert project membership with same name in tenant2 (should succeed):
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-2", "user-project-membership", "user-2", "tenant-2", `{"spec":{"project":"proj-2","user":"user-2","role":"PROJECT_MEMBERSHIP_ROLE_MANAGER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Has all required columns with correct types", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Query column information:
		rows, err := conn.Query(
			ctx,
			`select column_name, data_type, is_nullable, column_default
			 from information_schema.columns
			 where table_name = 'project_memberships'
			 order by ordinal_position`,
		)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()

		// Build a map of column properties:
		columns := make(map[string]map[string]interface{})
		for rows.Next() {
			var name, dataType, nullable string
			var defaultValue *string
			err = rows.Scan(&name, &dataType, &nullable, &defaultValue)
			Expect(err).ToNot(HaveOccurred())
			columns[name] = map[string]interface{}{
				"type":     dataType,
				"nullable": nullable,
				"default":  defaultValue,
			}
		}

		// Verify key columns exist with correct properties:
		Expect(columns).To(HaveKey("id"))
		Expect(columns["id"]["type"]).To(Equal("text"))
		Expect(columns["id"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("name"))
		Expect(columns["name"]["type"]).To(Equal("text"))
		Expect(columns["name"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("creator"))
		Expect(columns["creator"]["type"]).To(Equal("text"))
		Expect(columns["creator"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("tenant"))
		Expect(columns["tenant"]["type"]).To(Equal("text"))
		Expect(columns["tenant"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("data"))
		Expect(columns["data"]["type"]).To(Equal("jsonb"))
		Expect(columns["data"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("finalizers"))
		Expect(columns["finalizers"]["type"]).To(Equal("ARRAY"))
		Expect(columns["finalizers"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("labels"))
		Expect(columns["labels"]["type"]).To(Equal("jsonb"))
		Expect(columns["labels"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("annotations"))
		Expect(columns["annotations"]["type"]).To(Equal("jsonb"))
		Expect(columns["annotations"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("creation_timestamp"))
		Expect(columns["creation_timestamp"]["type"]).To(Equal("timestamp with time zone"))
		Expect(columns["creation_timestamp"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("deletion_timestamp"))
		Expect(columns["deletion_timestamp"]["type"]).To(Equal("timestamp with time zone"))
		Expect(columns["deletion_timestamp"]["nullable"]).To(Equal("NO"))

		Expect(columns).To(HaveKey("version"))
		Expect(columns["version"]["type"]).To(Equal("integer"))
		Expect(columns["version"]["nullable"]).To(Equal("NO"))
	})

	It("Has correct default values", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"test-tenant", "Test Tenant", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert a minimal project membership (relying on defaults):
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, tenant, data)
			 values ($1, $2, $3)`,
			"test-membership-minimal", "test-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Query it back to verify defaults:
		var name, creator string
		var version int
		var finalizers []string
		row := conn.QueryRow(
			ctx,
			`select name, creator, version, finalizers from project_memberships where id = $1`,
			"test-membership-minimal",
		)
		err = row.Scan(&name, &creator, &version, &finalizers)
		Expect(err).ToNot(HaveOccurred())

		// Verify default values:
		Expect(name).To(Equal(""))       // default ''
		Expect(creator).To(Equal(""))    // default ''
		Expect(version).To(Equal(0))     // default 0
		Expect(finalizers).To(BeEmpty()) // default '{}'
	})

	It("Rejects duplicate (tenant, project, user) tuples with helpful error message", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"test-tenant", "Test Tenant", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert first project membership:
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-1", "first-membership", "creator-1", "test-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to insert a second membership with the same (tenant, project, user) tuple (should fail):
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-2", "second-membership", "creator-2", "test-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_MANAGER"}}`,
		)
		Expect(err).To(HaveOccurred())

		// Verify it's the duplicate membership error with the correct message:
		var pgErr *pgconn.PgError
		Expect(err).To(BeAssignableToTypeOf(pgErr))
		pgErr = func() *pgconn.PgError {
			target := &pgconn.PgError{}
			_ = errors.As(err, &target)
			return target
		}()
		Expect(pgErr.Code).To(Equal("Z0004")) // errNotUniqueCode
		Expect(pgErr.Message).To(Equal("user 'user-1' is already a member of project 'proj-1' via membership 'first-membership'"))
	})

	It("Allows different users to have memberships in the same project", func(ctx context.Context) {
		// Run the migration:
		err := tool.Migrate(ctx, 67)
		Expect(err).ToNot(HaveOccurred())

		// Create organization first (required by foreign key):
		_, err = conn.Exec(
			ctx,
			`insert into tenants (id, name, tenant, creator, data) values ($1, $2, $3, $4, $5) on conflict do nothing`,
			"test-tenant", "Test Tenant", "system", "system", "{}",
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert first user's membership to proj-1:
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-1", "user1-proj1", "creator-1", "test-tenant", `{"spec":{"project":"proj-1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Insert second user's membership to the same project (should succeed):
		_, err = conn.Exec(
			ctx,
			`insert into project_memberships (id, name, creator, tenant, data)
			 values ($1, $2, $3, $4, $5)`,
			"membership-2", "user2-proj1", "creator-2", "test-tenant", `{"spec":{"project":"proj-1","user":"user-2","role":"PROJECT_MEMBERSHIP_ROLE_MANAGER"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Verify both memberships exist:
		var count int
		row := conn.QueryRow(
			ctx,
			`select count(*) from project_memberships where tenant = $1 and data->'spec'->>'project' = $2`,
			"test-tenant", "proj-1",
		)
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))
	})

})
