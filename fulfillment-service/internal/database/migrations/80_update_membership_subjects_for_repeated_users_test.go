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

var _ = DescribeMigration("Update membership subjects for repeated users", func() {
	// Helper that creates the prerequisite tenant and project used by most tests.
	setup := func(ctx context.Context) {
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
	}

	It("Materializes multiple users from the repeated users array", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "viewers", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1","user-2","user-3"]}}`)
		Expect(err).ToNot(HaveOccurred())

		// All three users should have rows in the helper table:
		var count int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(3))

		// Verify each user is present:
		rows, err := conn.Query(ctx,
			`select "user" from project_membership_subjects
			 where membership = $1 order by "user"`,
			"membership-1")
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()

		var users []string
		for rows.Next() {
			var u string
			Expect(rows.Scan(&u)).To(Succeed())
			users = append(users, u)
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
		Expect(users).To(Equal([]string{"user-1", "user-2", "user-3"}))
	})

	It("Works with a single user in the array", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "single-user", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1"]}}`)
		Expect(err).ToNot(HaveOccurred())

		var user string
		row := conn.QueryRow(ctx,
			`select "user" from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&user)
		Expect(err).ToNot(HaveOccurred())
		Expect(user).To(Equal("user-1"))
	})

	It("Works with an empty users array", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "no-users", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":[]}}`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Rejects duplicate user across different memberships in the same project", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		// First membership with user-1:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "first", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1"]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Second membership also containing user-1 in the same project:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-2", "second", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_MANAGER","users":["user-1"]}}`)
		Expect(err).To(HaveOccurred())

		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(Equal(
			"user 'user-1' is already a member of project 'proj1' via membership 'first'"))
	})

	It("Rejects duplicate user within the same membership", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		// A membership that lists the same user twice:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "duped", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1","user-1"]}}`)
		Expect(err).To(HaveOccurred())

		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
	})

	It("Allows the same user in different projects", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		// Create a second project:
		_, err = conn.Exec(ctx,
			`insert into projects (id, tenant, name, project, data)
			 values ($1, $2, $3, $4, $5)`,
			"proj2-id", "test-tenant", "proj2", "", "{}")
		Expect(err).ToNot(HaveOccurred())

		// user-1 in proj1:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "m1", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1"]}}`)
		Expect(err).ToNot(HaveOccurred())

		// user-1 in proj2 (should succeed):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-2", "m2", "creator", "test-tenant", "proj2",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1"]}}`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects where "user" = $1`,
			"user-1")
		err = row.Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))
	})

	It("Updates helper table when membership is updated with new users", func(ctx context.Context) {
		err := tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		// Insert initial membership with two users:
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "evolving", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-1","user-2"]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Update to a different set of users:
		_, err = conn.Exec(ctx,
			`update project_memberships set data = $1 where id = $2`,
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":["user-2","user-3"]}}`,
			"membership-1")
		Expect(err).ToNot(HaveOccurred())

		// user-1 should be gone, user-2 retained, user-3 added:
		rows, err := conn.Query(ctx,
			`select "user" from project_membership_subjects
			 where membership = $1 order by "user"`,
			"membership-1")
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()

		var users []string
		for rows.Next() {
			var u string
			Expect(rows.Scan(&u)).To(Succeed())
			users = append(users, u)
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
		Expect(users).To(Equal([]string{"user-2", "user-3"}))
	})

	It("Converts old single-user format and backfills correctly", func(ctx context.Context) {
		// Insert data BEFORE migration 80 (old trigger from migration 79 is active,
		// which reads spec.user as a single value):
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

		// Insert using the old format (spec.user singular):
		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "old-format", "creator", "test-tenant", "proj1",
			`{"spec":{"user":"user-1","role":"PROJECT_MEMBERSHIP_ROLE_VIEWER"}}`)
		Expect(err).ToNot(HaveOccurred())

		// Verify the old trigger populated the helper table:
		var countBefore int
		row := conn.QueryRow(ctx,
			`select count(*) from project_membership_subjects where membership = $1`,
			"membership-1")
		err = row.Scan(&countBefore)
		Expect(err).ToNot(HaveOccurred())
		Expect(countBefore).To(Equal(1))

		// Apply migration 80 — it converts old "user" to "users" array, replaces trigger, and backfills:
		err = tool.Migrate(ctx, 80)
		Expect(err).ToNot(HaveOccurred())

		// Verify the data was converted to the new format:
		var data string
		row = conn.QueryRow(ctx,
			`select data::text from project_memberships where id = $1`,
			"membership-1")
		err = row.Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(data).To(ContainSubstring(`"users"`))
		Expect(data).ToNot(ContainSubstring(`"user"`))

		// Verify the helper table has correct data after backfill:
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
