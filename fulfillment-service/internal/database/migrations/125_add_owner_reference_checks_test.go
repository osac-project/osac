/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add owner reference checks", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 125)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}'),
			       ('tenant-b', 'tenant-b', 'tenant-b', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team-a', 'tenant-a', 'team-a', 'system', '{}'),
			       ('project-b', 'team-b', 'tenant-a', 'team-b', 'system', '{}'),
			       ('project-c', 'team-b', 'tenant-b', 'team-b', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertCluster := func(ctx context.Context, id, tenant, project string) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, name, tenant, project, data)
			values ($1, $1, $2, $3, '{}')`, id, tenant, project)
		Expect(err).ToNot(HaveOccurred())
	}

	insertWorker := func(ctx context.Context, id, tenant, project, data string) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, project, data)
			values ($1, $1, $2, $3, $4::jsonb)`, id, tenant, project, data)
		Expect(err).ToNot(HaveOccurred())
	}

	// workerData is the status of a cluster worker: an owner reference to its cluster and the
	// node set it serves.
	workerData := func(clusterID, nodeSet string) string {
		return `{"status":{"cluster":{"id":"` + clusterID + `"},"node_set":"` + nodeSet + `"}}`
	}

	expectCode := func(err error, code string) {
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal(code))
	}

	It("accepts a worker that targets a live cluster in the same tenant and project", func(ctx context.Context) {
		insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
		insertWorker(ctx, "worker-1", "tenant-a", "team-a", workerData("cluster-1", "compute"))
	})

	DescribeTable("rejects invalid owner-reference targets",
		func(ctx context.Context, targetSetup func(ctx context.Context)) {
			if targetSetup != nil {
				targetSetup(ctx)
			}
			_, err := conn.Exec(ctx, `
				insert into bare_metal_instances (id, name, tenant, project, data)
				values ('worker-1', 'worker-1', 'tenant-a', 'team-a', $1::jsonb)`,
				workerData("cluster-1", "compute"))
			expectCode(err, "Z0005")
		},
		Entry("missing target", nil),
		Entry("target in another tenant",
			func(ctx context.Context) { insertCluster(ctx, "cluster-1", "tenant-b", "team-b") }),
		Entry("target in another project",
			func(ctx context.Context) { insertCluster(ctx, "cluster-1", "tenant-a", "team-b") }),
		Entry("deleting target",
			func(ctx context.Context) {
				insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
				_, err := conn.Exec(ctx, `update clusters set deletion_timestamp = now() where id = 'cluster-1'`)
				Expect(err).ToNot(HaveOccurred())
			}),
	)

	DescribeTable("rejects malformed owner references",
		func(ctx context.Context, data string) {
			insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
			_, err := conn.Exec(ctx, `
				insert into bare_metal_instances (id, name, tenant, project, data)
				values ('worker-1', 'worker-1', 'tenant-a', 'team-a', $1::jsonb)`, data)
			expectCode(err, "Z0005")
		},
		Entry("empty target id", `{"status":{"cluster":{"id":""}}}`),
		Entry("empty reference", `{"status":{"cluster":{}}}`),
	)

	It("rejects changing or clearing an existing owner reference or node set", func(ctx context.Context) {
		insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
		insertCluster(ctx, "cluster-2", "tenant-a", "team-a")
		insertWorker(ctx, "worker-1", "tenant-a", "team-a", workerData("cluster-1", "compute"))

		_, err := conn.Exec(ctx, `
			update bare_metal_instances
			set data = jsonb_set(data, '{status,cluster,id}', '"cluster-2"')
			where id = 'worker-1'`)
		expectCode(err, "Z0005")

		_, err = conn.Exec(ctx, `
			update bare_metal_instances
			set data = data #- '{status,cluster}'
			where id = 'worker-1'`)
		expectCode(err, "Z0005")

		_, err = conn.Exec(ctx, `
			update bare_metal_instances
			set data = jsonb_set(data, '{status,node_set}', '"gpu"')
			where id = 'worker-1'`)
		expectCode(err, "Z0005")
	})

	It("allows updates and soft deletes that keep the owner reference unchanged", func(ctx context.Context) {
		insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
		insertWorker(ctx, "worker-1", "tenant-a", "team-a", workerData("cluster-1", "compute"))

		_, err := conn.Exec(ctx, `
			update bare_metal_instances
			set data = jsonb_set(data, '{status,state}', '"BARE_METAL_INSTANCE_STATE_READY"')
			where id = 'worker-1'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update bare_metal_instances set deletion_timestamp = now()
			where id = 'worker-1'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("allows a cluster to start deleting while it still owns workers", func(ctx context.Context) {
		insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
		insertWorker(ctx, "worker-1", "tenant-a", "team-a", workerData("cluster-1", "compute"))

		_, err := conn.Exec(ctx, `update clusters set deletion_timestamp = now() where id = 'cluster-1'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("finishes deleting a cluster only after its workers are gone", func(ctx context.Context) {
		insertCluster(ctx, "cluster-1", "tenant-a", "team-a")
		insertWorker(ctx, "worker-1", "tenant-a", "team-a", workerData("cluster-1", "compute"))

		_, err := conn.Exec(ctx, `delete from clusters where id = 'cluster-1'`)
		expectCode(err, "Z0003")

		_, err = conn.Exec(ctx, `delete from bare_metal_instances where id = 'worker-1'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `delete from clusters where id = 'cluster-1'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("leaves rows without an owner reference untouched", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, project, data)
			values ('worker-1', 'worker-1', 'tenant-a', 'team-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update bare_metal_instances
			set data = jsonb_set(data, '{status,state}', '"BARE_METAL_INSTANCE_STATE_READY"')
			where id = 'worker-1'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
