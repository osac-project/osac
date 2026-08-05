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
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add cluster version allowed_upgrades referential integrity trigger", func() {
	cvData := func(version, image string, versionNames ...string) string {
		names := "[]"
		if len(versionNames) > 0 {
			items := ""
			for i, n := range versionNames {
				if i > 0 {
					items += ","
				}
				items += fmt.Sprintf("%q", n)
			}
			names = "[" + items + "]"
		}
		return fmt.Sprintf(
			`{"spec":{"version":%q,"image":%q,"allowed_upgrades":{"version_names":%s}}}`,
			version, image, names,
		)
	}

	insertCV := func(ctx context.Context, id, name, version, image string, versionNames ...string) error {
		_, err := conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, 'system', $3::jsonb)`,
			id, name, cvData(version, image, versionNames...))
		return err
	}

	It("Creates the 'check_cluster_version_allowed_upgrade_refs' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'check_cluster_version_allowed_upgrade_refs'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds the insert trigger to the cluster_versions table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_cluster_version_allowed_upgrade_refs_on_insert'
			  and event_object_table = 'cluster_versions'
			  and action_timing = 'BEFORE'
			  and event_manipulation = 'INSERT'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds the update trigger to the cluster_versions table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_cluster_version_allowed_upgrade_refs_on_update'
			  and event_object_table = 'cluster_versions'
			  and action_timing = 'BEFORE'
			  and event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Allows inserting a ClusterVersion with valid version_names", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-ref", "4.17.0", "4.17.0", "quay.io/test:4.17.0")
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-with-refs", "4.16.0", "4.16.0", "quay.io/test:4.16.0", "4.17.0")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows inserting a ClusterVersion with empty version_names", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-empty", "4.17.0", "4.17.0", "quay.io/test:4.17.0")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows inserting a ClusterVersion without allowed_upgrades", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, 'system', $3::jsonb)`,
			"cv-no-au", "4.17.0",
			`{"spec":{"version":"4.17.0","image":"quay.io/test:4.17.0"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects inserting a ClusterVersion referencing a non-existent version name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-bad", "4.16.0", "4.16.0", "quay.io/test:4.16.0", "does-not-exist")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("does-not-exist"))
	})

	It("Rejects inserting a ClusterVersion referencing a soft-deleted version name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-deleted", "4.17.0", "4.17.0", "quay.io/test:4.17.0")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update cluster_versions set deletion_timestamp = now() where id = 'cv-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-ref-deleted", "4.16.0", "4.16.0", "quay.io/test:4.16.0", "4.17.0")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("4.17.0"))
	})

	It("Allows updating allowed_upgrades to add a valid reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-target", "4.17.0", "4.17.0", "quay.io/test:4.17.0")
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-source", "4.16.0", "4.16.0", "quay.io/test:4.16.0")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update cluster_versions set data = $1::jsonb where id = 'cv-source'`,
			cvData("4.16.0", "quay.io/test:4.16.0", "4.17.0"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects updating allowed_upgrades to add a non-existent reference", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-upd", "4.16.0", "4.16.0", "quay.io/test:4.16.0")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update cluster_versions set data = $1::jsonb where id = 'cv-upd'`,
			cvData("4.16.0", "quay.io/test:4.16.0", "no-such-version"))
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-version"))
	})

	It("Allows updating unrelated fields without triggering validation", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-unrelated", "4.16.0", "4.16.0", "quay.io/test:4.16.0")
		Expect(err).ToNot(HaveOccurred())

		// Update a non-allowed_upgrades field (add status) — should not trigger validation:
		_, err = conn.Exec(ctx,
			`update cluster_versions
			 set data = data || '{"status":{"state":"STATE_ENABLED"}}'::jsonb
			 where id = 'cv-unrelated'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Validates across tenants independently", func(ctx context.Context) {
		err := tool.Migrate(ctx, 81)
		Expect(err).ToNot(HaveOccurred())

		// Insert a CV under the system tenant:
		err = insertCV(ctx, "cv-sys", "4.17.0", "4.17.0", "quay.io/test:4.17.0")
		Expect(err).ToNot(HaveOccurred())

		// Create a second tenant (the create_default_project trigger auto-inserts its default project):
		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('other', 'other', 'other', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		// Referencing '4.17.0' from the other tenant should fail — it only exists in 'system':
		_, err = conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, 'other', $3::jsonb)`,
			"cv-other", "4.16.0",
			cvData("4.16.0", "quay.io/test:4.16.0", "4.17.0"))
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
	})
})
