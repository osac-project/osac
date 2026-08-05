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

var _ = DescribeMigration("Remove project from membership spec", func() {
	It("Trigger reads project from the column instead of the JSONB spec", func(ctx context.Context) {
		err := tool.Migrate(ctx, 79)
		Expect(err).ToNot(HaveOccurred())

		// Create prerequisite tenant (auto-creates default project via trigger):
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, $2, $3) on conflict do nothing`,
			"test-tenant", "system", "{}")
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, name, project, data)
			 values ($1, $2, $3, $4, $5)`,
			"proj1-id", "test-tenant", "proj1", "", "{}")
		Expect(err).ToNot(HaveOccurred())

		// Insert a membership with project in the column but NOT in the JSONB spec
		// (simulating the new proto where spec.project no longer exists):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "test-membership", "creator", "test-tenant", "proj1",
			`{"spec":{"user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Verify the helper table was populated using the column value:
		var project, user string
		row := conn.QueryRow(ctx,
			`select project, "user" from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&project, &user)
		Expect(err).ToNot(HaveOccurred())
		Expect(project).To(Equal("proj1"))
		Expect(user).To(Equal("user-1"))
	})

	It("Rejects duplicate (tenant, project, user) tuples with helpful error message", func(ctx context.Context) {
		err := tool.Migrate(ctx, 79)
		Expect(err).ToNot(HaveOccurred())

		// Create prerequisite tenant (auto-creates default project via trigger):
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, $2, $3) on conflict do nothing`,
			"test-tenant", "system", "{}")
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, name, project, data)
			 values ($1, $2, $3, $4, $5)`,
			"proj1-id", "test-tenant", "proj1", "", "{}")
		Expect(err).ToNot(HaveOccurred())

		// Insert first membership:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "first-membership", "creator-1", "test-tenant", "proj1",
			`{"spec":{"user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to insert duplicate (same tenant, project column, user):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-2", "second-membership", "creator-2", "test-tenant", "proj1",
			`{"spec":{"user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_MANAGER"}}`)
		Expect(err).To(HaveOccurred())

		// Verify it's the duplicate membership error:
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(Equal(
			"user 'user-1' is already a member of project 'proj1' via membership 'first-membership'"))
	})

	It("Allows different users in the same project", func(ctx context.Context) {
		err := tool.Migrate(ctx, 79)
		Expect(err).ToNot(HaveOccurred())

		// Create prerequisite tenant (auto-creates default project via trigger):
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, $2, $3) on conflict do nothing`,
			"test-tenant", "system", "{}")
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, name, project, data)
			 values ($1, $2, $3, $4, $5)`,
			"proj1-id", "test-tenant", "proj1", "", "{}")
		Expect(err).ToNot(HaveOccurred())

		// Insert first user's membership:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "user1-proj1", "creator-1", "test-tenant", "proj1",
			`{"spec":{"user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Insert second user's membership to the same project (should succeed):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-2", "user2-proj1", "creator-2", "test-tenant", "proj1",
			`{"spec":{"user":"user-2","role":"PROJECT_MEMBERSHIP_ROLE_MANAGER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Verify both exist in the helper table:
		var count int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects
			 where tenant = $1 and project = $2`,
			"test-tenant", "proj1")
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))
	})

	It("Backfills existing rows using the project column", func(ctx context.Context) {
		// Set up data BEFORE applying migration 79 (old trigger from migration 67 is active):
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $1, $1, $2, $3) on conflict do nothing`,
			"test-tenant", "system", "{}")
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, name, project, data)
			 values ($1, $2, $3, $4, $5)`,
			"proj1-id", "test-tenant", "proj1", "", "{}")
		Expect(err).ToNot(HaveOccurred())

		// Insert a membership using the OLD trigger (reads spec.project from JSONB):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "existing-membership", "creator-1", "test-tenant", "proj1",
			`{"spec":{"project":"proj1","user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Verify the old trigger populated the helper table:
		var countBefore int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&countBefore)
		Expect(err).ToNot(HaveOccurred())
		Expect(countBefore).To(Equal(1))

		// Apply migration 79 (replaces trigger function and backfills):
		err = tool.Migrate(ctx, 79)
		Expect(err).ToNot(HaveOccurred())

		// Verify the helper table still has correct data after backfill:
		var project, user string
		row = conn.QueryRow(ctx,
			`select project, "user" from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&project, &user)
		Expect(err).ToNot(HaveOccurred())
		Expect(project).To(Equal("proj1"))
		Expect(user).To(Equal("user-1"))
	})
})
