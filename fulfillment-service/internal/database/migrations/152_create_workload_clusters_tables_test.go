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
		Expect(tool.Migrate(ctx, 150)).To(Succeed())
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

	It("adds event metadata columns to durable changes", func(ctx context.Context) {
		var reasonType, severityType string
		err := conn.QueryRow(ctx, `
			select
			(select data_type from information_schema.columns where table_name = 'changes' and column_name = 'reason'),
			(select data_type from information_schema.columns where table_name = 'changes' and column_name = 'severity')
		`).Scan(&reasonType, &severityType)
		Expect(err).ToNot(HaveOccurred())
		Expect(reasonType).To(Equal("text"))
		Expect(severityType).To(Equal("text"))
	})

	It("persists event metadata set by a signal operation", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `select set_config('osac.event_reason', 'HubReconnected', false), set_config('osac.event_severity', 'Normal', false)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('event-metadata-hub', 'event-metadata-hub', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('event-metadata-wc', 'event-metadata-wc', 'shared', '{"spec":{"maas":{"source_hub_id":"event-metadata-hub","managed_cluster_name":"worker-1"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		var reason, severity string
		err = conn.QueryRow(ctx, `
			select reason, severity from changes
			where "table" = 'workload_clusters' and data->>'id' = 'event-metadata-wc'
			order by id desc limit 1
		`).Scan(&reason, &severity)
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(Equal("HubReconnected"))
		Expect(severity).To(Equal("Normal"))
	})

	It("marks only actual WorkloadCluster availability transitions", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into hubs (id, name, tenant, data) values ('transition-hub', 'transition-hub', 'shared', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('transition-wc', 'transition-wc', 'shared',
			'{"spec":{"maas":{"source_hub_id":"transition-hub","managed_cluster_name":"worker-1"}},"status":{"conditions":[{"type":"Available","status":"False","reason":"ManagedClusterUnavailable"}]}}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update workload_clusters
			set data = '{"spec":{"maas":{"source_hub_id":"transition-hub","managed_cluster_name":"worker-1"}},"status":{"conditions":[{"type":"Available","status":"True","reason":"ManagedClusterAvailable"}]}}'
			where id = 'transition-wc'
		`)
		Expect(err).ToNot(HaveOccurred())
		var reason, severity string
		err = conn.QueryRow(ctx, `
			select reason, severity from changes
			where "table" = 'workload_clusters' and data->>'id' = 'transition-wc' and op = 'UPDATE'
			order by id desc limit 1
		`).Scan(&reason, &severity)
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(Equal("Available"))
		Expect(severity).To(Equal("Normal"))

		_, err = conn.Exec(ctx, `update workload_clusters set finalizers = '{unrelated}' where id = 'transition-wc'`)
		Expect(err).ToNot(HaveOccurred())
		var unchangedReason, unchangedSeverity *string
		err = conn.QueryRow(ctx, `
			select reason, severity from changes
			where "table" = 'workload_clusters' and data->>'id' = 'transition-wc' and op = 'UPDATE'
			order by id desc limit 1
		`).Scan(&unchangedReason, &unchangedSeverity)
		Expect(err).ToNot(HaveOccurred())
		Expect(unchangedReason).To(BeNil())
		Expect(unchangedSeverity).To(BeNil())
	})

	It("enforces unique active Hub identity", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-a', 'cluster-a', 'shared', '{"spec":{"maas":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-b', 'cluster-b', 'shared', '{"spec":{"maas":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}}')`)
		Expect(err).To(HaveOccurred())
	})

	It("tracks active records and preserves the Hub identity projection", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-a', 'cluster-a', 'shared', '{"spec":{"maas":{"source_hub_id":"hub-a","managed_cluster_name":"worker-a"}}}')`)
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

	It("preserves registrations when a Hub is deleted", func(ctx context.Context) {
		var err error
		_, err = conn.Exec(ctx, `insert into hubs (id, name, tenant, data) values ('hub-a', 'hub-a', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into workload_clusters (id, name, tenant, data)
			values ('wc-b', 'cluster-b', 'shared', '{"spec":{"maas":{"source_hub_id":"hub-a","managed_cluster_name":"worker-b"}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update hubs set deletion_timestamp = now() where id = 'hub-a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `delete from hubs where id = 'hub-a'`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from workload_clusters where id = 'wc-b'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(conn.QueryRow(ctx, `select count(*) from workload_cluster_hub_refs where workload_cluster_id = 'wc-b'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
})
