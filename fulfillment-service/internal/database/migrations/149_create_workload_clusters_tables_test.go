//
// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create WorkloadCluster tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 149)).To(Succeed())
	})

	It("creates the provider-global schema and tenant foreign key", func(ctx context.Context) {
		var count int
		Expect(conn.QueryRow(ctx, `
			select count(*) from information_schema.columns
			where table_name = 'workload_clusters' and column_name = 'data'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))

		Expect(conn.QueryRow(ctx, `
			select count(*) from pg_constraint
			where conname = 'workload_clusters_tenant_fk'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})

	It("enforces unique active Hub identity", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-a', 'cluster-a', 'shared', '{"spec":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-b', 'cluster-b', 'shared', '{"spec":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}')`)
		Expect(err).To(HaveOccurred())
	})

	It("tracks active records and enforces the Hub foreign key", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-a', 'cluster-a', 'shared', '{"spec":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}')`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from workload_cluster_hub_refs where workload_cluster_id = 'wc-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(conn.QueryRow(ctx, `select count(*) from active_workload_clusters where id = 'wc-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))

		_, err = conn.Exec(ctx, `update workload_clusters set deletion_timestamp = now() where id = 'wc-a'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from workload_cluster_hub_refs where workload_cluster_id = 'wc-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(conn.QueryRow(ctx, `select count(*) from active_workload_clusters where id = 'wc-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("rejects unknown Hubs and prevents referenced Hub deletion", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-a', 'cluster-a', 'shared', '{"spec":{"source_hub_id":"missing","managed_cluster_name":"worker-a"}}')`)
		Expect(err).To(HaveOccurred())

		_, err = conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-b', 'cluster-b', 'shared', '{"spec":{"source_hub_id":"hub-a","managed_cluster_name":"worker-b"}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update hubs set deletion_timestamp = now() where id = 'hub-a'`)
		Expect(err).To(HaveOccurred())
	})
})
