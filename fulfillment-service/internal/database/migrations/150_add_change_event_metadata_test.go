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

var _ = DescribeMigration("Add change event metadata", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 150)).To(Succeed())
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
			values ('event-metadata-wc', 'event-metadata-wc', 'shared', '{"spec":{"source_hub_id":"event-metadata-hub","managed_cluster_name":"worker-1"}}')`)
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
})
