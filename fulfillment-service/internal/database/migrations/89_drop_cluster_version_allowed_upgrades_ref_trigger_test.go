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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Drop cluster version allowed_upgrades referential integrity trigger", func() {
	It("Drops the 'check_cluster_version_allowed_upgrade_refs' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 89)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'check_cluster_version_allowed_upgrade_refs'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Drops the insert trigger from the cluster_versions table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 89)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_cluster_version_allowed_upgrade_refs_on_insert'
			  and event_object_table = 'cluster_versions'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Drops the update trigger from the cluster_versions table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 89)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_cluster_version_allowed_upgrade_refs_on_update'
			  and event_object_table = 'cluster_versions'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Allows inserting a ClusterVersion referencing a non-existent version name", func(ctx context.Context) {
		err := tool.Migrate(ctx, 89)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, 'system', $3::jsonb)`,
			"cv-stale-ref", "4.16.0",
			`{"spec":{"version":"4.16.0","image":"quay.io/test:4.16.0",
			  "allowed_upgrades":{"version_names":["does-not-exist"]}}}`)
		Expect(err).ToNot(HaveOccurred())
	})
})
