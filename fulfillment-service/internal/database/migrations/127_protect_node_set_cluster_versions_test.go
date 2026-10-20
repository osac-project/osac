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
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Protect ClusterVersions referenced by node sets", func() {
	insertVersion := func(ctx context.Context, id, name string) {
		_, err := conn.Exec(ctx, `insert into cluster_versions (id, name, tenant, data)
			values ($1, $2, 'system', $3::jsonb)`, id, name,
			fmt.Sprintf(`{"spec":{"version":%q,"image":"quay.io/example/release:4.17"}}`, strings.ReplaceAll(name, "-", ".")))
		Expect(err).ToNot(HaveOccurred())
	}
	insertCluster := func(ctx context.Context, id, data string) {
		_, err := conn.Exec(ctx, `insert into clusters (id, name, tenant, data)
			values ($1, $1, 'system', $2::jsonb)`, id, data)
		Expect(err).ToNot(HaveOccurred())
	}
	deleteVersion := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx, `update cluster_versions set deletion_timestamp = now() where id = $1`, id)
		return err
	}
	expectInUse := func(err error) {
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal("Z0003"))
		ExpectWithOffset(1, pgErr.Message).To(ContainSubstring("in use by at least one cluster"))
	}

	It("protects a canonical node-set reference without a control-plane reference", func(ctx context.Context) {
		insertVersion(ctx, "node-version", "4-17-0")
		insertCluster(ctx, "cluster", `{"spec":{"node_sets":{"workers":{"version":{"id":"node-version","name":"4-17-0"}}}}}`)
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		expectInUse(deleteVersion(ctx, "node-version"))
	})

	It("ignores non-object node-set values when checking deletion", func(ctx context.Context) {
		insertVersion(ctx, "unreferenced-version", "4-17-0")
		insertCluster(ctx, "null-node-sets", `{"spec":{"node_sets":null}}`)
		insertCluster(ctx, "array-node-sets", `{"spec":{"node_sets":[]}}`)
		insertCluster(ctx, "string-node-sets", `{"spec":{"node_sets":"invalid"}}`)
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		Expect(deleteVersion(ctx, "unreferenced-version")).To(Succeed())
	})

	It("uses a canonical ID ahead of a matching name", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into projects (id, name, tenant, project, data)
			values ('other-project', 'other-project', 'system', '', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into cluster_versions (id, name, tenant, project, data) values
			('target', '4-17-0', 'system', '', '{"spec":{"version":"4.17.0","image":"quay.io/example/release:4.17"}}'),
			('other', '4-17-0', 'system', 'other-project', '{"spec":{"version":"4.17.0","image":"quay.io/example/release:4.17"}}')`)
		Expect(err).ToNot(HaveOccurred())
		insertCluster(ctx, "cluster", `{"spec":{"node_sets":{"workers":{"version":{"id":"other","name":"4-17-0"}}}}}`)
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		Expect(deleteVersion(ctx, "target")).To(Succeed())
		expectInUse(deleteVersion(ctx, "other"))
	})

	It("protects references added after migration and releases protection when they are removed", func(ctx context.Context) {
		insertVersion(ctx, "node-version", "4-17-0")
		insertCluster(ctx, "cluster", `{"spec":{"node_sets":{"workers":{}}}}`)
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		var data []byte
		Expect(conn.QueryRow(ctx, `select data from clusters where id = 'cluster'`).Scan(&data)).To(Succeed())
		Expect(string(data)).To(MatchJSON(`{"spec":{"node_sets":{"workers":{}}}}`))

		_, err := conn.Exec(ctx, `update clusters set data = jsonb_set(data,
			'{spec,node_sets,workers,version}', '{"id":"node-version","name":"4-17-0"}'::jsonb) where id = 'cluster'`)
		Expect(err).ToNot(HaveOccurred())
		expectInUse(deleteVersion(ctx, "node-version"))

		_, err = conn.Exec(ctx, `update clusters set data = data #- '{spec,node_sets,workers,version}' where id = 'cluster'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteVersion(ctx, "node-version")).To(Succeed())
	})

	It("ignores references held only by soft-deleted clusters", func(ctx context.Context) {
		insertVersion(ctx, "node-version", "4-17-0")
		insertCluster(ctx, "cluster", `{"spec":{"node_sets":{"workers":{"version":{"id":"node-version"}}}}}`)
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		_, err := conn.Exec(ctx, `update clusters set deletion_timestamp = now() where id = 'cluster'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteVersion(ctx, "node-version")).To(Succeed())
	})

	It("protects a node set in a cluster created after migration", func(ctx context.Context) {
		insertVersion(ctx, "node-version", "4-17-0")
		Expect(tool.Migrate(ctx, 127)).To(Succeed())
		insertCluster(ctx, "cluster", `{"spec":{"node_sets":{"new-workers":{"version":{"id":"node-version"}}}}}`)

		expectInUse(deleteVersion(ctx, "node-version"))
	})

	It("retains control-plane, template, and catalog-item protection", func(ctx context.Context) {
		insertVersion(ctx, "cp-version", "4-17-0")
		insertVersion(ctx, "template-version", "4-17-1")
		insertVersion(ctx, "catalog-version", "4-17-2")
		insertVersion(ctx, "editable-version", "4-17-3")
		insertCluster(ctx, "cluster", `{"spec":{"version":{"id":"cp-version"}}}`)
		_, err := conn.Exec(ctx, `insert into cluster_templates (id, name, tenant, data)
			values ('template', 'template', 'system', '{"spec_defaults":{"version":{"id":"template-version"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into cluster_catalog_items (id, name, tenant, data)
			values ('catalog', 'catalog', 'system', '{"fields":{"version":{"locked":{"id":"catalog-version"}}}}'),
				('editable-catalog', 'editable-catalog', 'system',
				'{"fields":{"version":{"editable":{"default_value":{"id":"editable-version"}}}}}')`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, 127)).To(Succeed())

		for _, id := range []string{"cp-version", "template-version", "catalog-version", "editable-version"} {
			expectInUse(deleteVersion(ctx, id))
		}
	})

})
