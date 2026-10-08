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

var _ = DescribeMigration("Fix project membership subjects trigger for typed user references", func() {
	// Helper that creates the prerequisite tenant and project used by the tests.
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

	It("Extracts the id from each typed UserReference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 122)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "viewers", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":[{"id":"user-1","name":"a"},{"id":"user-2","name":"b"}]}}`)
		Expect(err).ToNot(HaveOccurred())

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
		Expect(users).To(Equal([]string{"user-1", "user-2"}))
	})

	It("Repairs rows corrupted by the previous trigger via backfill", func(ctx context.Context) {
		// The database starts at the previous migration, whose trigger reads the users array with
		// jsonb_array_elements_text and therefore stores the whole stringified UserReference object.
		setup(ctx)

		_, err := conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "viewers", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":[{"id":"user-1","name":"a"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		// Confirm the pre-migration trigger stored a corrupted (stringified object) value:
		var corrupted string
		row := conn.QueryRow(ctx,
			`select "user" from project_membership_subjects where membership = $1`,
			"membership-1")
		Expect(row.Scan(&corrupted)).To(Succeed())
		Expect(corrupted).ToNot(Equal("user-1"))
		Expect(corrupted).To(ContainSubstring("{"))

		// Apply the fix migration; its backfill must repair the existing row:
		err = tool.Migrate(ctx, 122)
		Expect(err).ToNot(HaveOccurred())

		var repaired string
		row = conn.QueryRow(ctx,
			`select "user" from project_membership_subjects where membership = $1`,
			"membership-1")
		Expect(row.Scan(&repaired)).To(Succeed())
		Expect(repaired).To(Equal("user-1"))
	})

	It("Materializes nothing for an empty users array", func(ctx context.Context) {
		err := tool.Migrate(ctx, 122)
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
		Expect(row.Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0))
	})

	It("Still rejects a duplicate user in the same project with a bare id message", func(ctx context.Context) {
		err := tool.Migrate(ctx, 122)
		Expect(err).ToNot(HaveOccurred())
		setup(ctx)

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-1", "first", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_VIEWER","users":[{"id":"user-1"}]}}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into project_memberships (id, name, creator, tenant, project, data)
			 values ($1, $2, $3, $4, $5, $6)`,
			"membership-2", "second", "creator", "test-tenant", "proj1",
			`{"spec":{"role":"PROJECT_MEMBERSHIP_ROLE_MANAGER","users":[{"id":"user-1"}]}}`)
		Expect(err).To(HaveOccurred())

		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(Equal(
			"user 'user-1' is already a member of project 'proj1' via membership 'first'"))
	})
})
