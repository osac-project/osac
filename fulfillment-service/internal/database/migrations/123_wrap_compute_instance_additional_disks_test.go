/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Wrap ComputeInstance additional disks", func() {
	It("wraps active and archived data and preserves storage-tier references", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into storage_tiers (id, name, tenant, data) values ('tier', 'tier', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		activeData := `{"spec":{"additional_disks":[{"storage_tier":{"id":"tier"}}]}}`
		_, err = conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data) values ('active-ci', 'active-ci', 'system', $1::jsonb)`, activeData)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into archived_compute_instances (id, name, tenant, creation_timestamp, deletion_timestamp, data)
			values ('archived-ci', 'archived-ci', 'system', now(), now(), $1::jsonb)`, activeData)
		Expect(err).ToNot(HaveOccurred())

		Expect(tool.Migrate(ctx, 123)).To(Succeed())

		for _, table := range []string{"compute_instances", "archived_compute_instances"} {
			var data []byte
			err = conn.QueryRow(ctx, `select data from `+table+` where id = $1`, map[string]string{
				"compute_instances":          "active-ci",
				"archived_compute_instances": "archived-ci",
			}[table]).Scan(&data)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(MatchJSON(`{"spec":{"additional_disks":{"items":[{"storage_tier":{"id":"tier"}}]}}}`))
		}

		_, err = conn.Exec(ctx, `update storage_tiers set deletion_timestamp = now() where id = 'tier'`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
	})

	It("preserves empty and omitted disk fields for active and archived rows", func(ctx context.Context) {
		fixtures := []struct {
			name    string
			input   string
			afterUp string
		}{
			{
				name:    "empty",
				input:   `{"spec":{"additional_disks":[]}}`,
				afterUp: `{"spec":{"additional_disks":{"items":[]}}}`,
			},
			{
				name:    "omitted",
				input:   `{"spec":{}}`,
				afterUp: `{"spec":{}}`,
			},
		}
		tables := []struct {
			name   string
			prefix string
		}{
			{name: "compute_instances", prefix: "active"},
			{name: "archived_compute_instances", prefix: "archived"},
		}

		for _, table := range tables {
			for _, fixture := range fixtures {
				id := table.prefix + "-" + fixture.name
				if table.name == "compute_instances" {
					_, err := conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`, id, fixture.input)
					Expect(err).ToNot(HaveOccurred())
				} else {
					_, err := conn.Exec(ctx, `insert into archived_compute_instances (id, name, tenant, creation_timestamp, deletion_timestamp, data)
						values ($1, $1, 'system', now(), now(), $2::jsonb)`, id, fixture.input)
					Expect(err).ToNot(HaveOccurred())
				}
			}
		}

		assertData := func(table, id, expected string) {
			var data []byte
			err := conn.QueryRow(ctx, `select data from `+table+` where id = $1`, id).Scan(&data)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(MatchJSON(expected))
		}
		assertFixtures := func() {
			for _, table := range tables {
				for _, fixture := range fixtures {
					assertData(table.name, table.prefix+"-"+fixture.name, fixture.afterUp)
				}
			}
		}

		Expect(tool.Migrate(ctx, 123)).To(Succeed())
		assertFixtures()
	})
})
