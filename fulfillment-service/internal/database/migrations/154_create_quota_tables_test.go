//
// Copyright (c) 2026 Red Hat, Inc.
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

var _ = DescribeMigration("Create quota tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 154)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
            values ('q-tenant-a', 'q-tenant-a', 'q-tenant-a', 'system', '{}'),
                   ('q-tenant-b', 'q-tenant-b', 'q-tenant-b', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into projects (id, tenant, project, name, creator, data)
            values (uuidv7(), 'q-tenant-a', ''::ltree, 'team'::ltree, 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertLimit := func(ctx context.Context, id, tenant, project, dimension, value string) error {
		_, err := conn.Exec(ctx, `insert into quota_limits (id, name, tenant, project, data)
			values ($1, $1, $2, $3::ltree, jsonb_build_object('spec', jsonb_build_object('dimension', $4::text, 'value', $5::text)))`,
			id, tenant, project, dimension, value)
		return err
	}

	It("allows one limit per dimension per scope", func(ctx context.Context) {
		Expect(insertLimit(ctx, "a", "q-tenant-a", "", "compute.vcpus", "10")).To(Succeed())
		// The same dimension in another tenant, and in a project, is a different scope:
		Expect(insertLimit(ctx, "b", "q-tenant-b", "", "compute.vcpus", "10")).To(Succeed())
		Expect(insertLimit(ctx, "c", "q-tenant-a", "team", "compute.vcpus", "4")).To(Succeed())
		// The same scope and dimension is taken:
		Expect(insertLimit(ctx, "d", "q-tenant-a", "", "compute.vcpus", "20")).ToNot(Succeed())
		// A deleted limit can be set again while the deleted row still awaits the archiver,
		// the same way a cleared default can:
		_, err := conn.Exec(ctx, `update quota_limits set deletion_timestamp = now() where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(insertLimit(ctx, "e", "q-tenant-a", "", "compute.vcpus", "20")).To(Succeed())
	})

	It("rejects changes to identity and ownership", func(ctx context.Context) {
		Expect(insertLimit(ctx, "a", "q-tenant-a", "", "compute.vcpus", "10")).To(Succeed())
		_, err := conn.Exec(ctx, `update quota_limits set id = 'other' where id = 'a'`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `update quota_limits set name = 'other' where id = 'a'`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `update quota_limits set tenant = 'q-tenant-b' where id = 'a'`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `update quota_limits set project = 'team'::ltree where id = 'a'`)
		Expect(err).To(HaveOccurred())
		// The limit's value stays updatable:
		_, err = conn.Exec(ctx, `update quota_limits set data = jsonb_set(data, '{spec,value}', '20') where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("enforces tenant and project references", func(ctx context.Context) {
		Expect(insertLimit(ctx, "a", "absent", "", "compute.vcpus", "10")).ToNot(Succeed())
		Expect(insertLimit(ctx, "a", "q-tenant-a", "absent", "compute.vcpus", "10")).ToNot(Succeed())
	})

	It("holds one default per dimension, on the shared tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into quota_default_limits (id, name, data)
			values ('a', 'a', jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'value', '10')))`)
		Expect(err).ToNot(HaveOccurred())
		// The row landed on the shared tenant, the home of every platform-scoped kind:
		var tenant string
		Expect(conn.QueryRow(ctx, `select tenant from quota_default_limits where id = 'a'`).Scan(&tenant)).To(Succeed())
		Expect(tenant).To(Equal("shared"))
		_, err = conn.Exec(ctx, `insert into quota_default_limits (id, name, data)
			values ('b', 'b', jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'value', '20')))`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_default_limits (id, name, tenant, data)
			values ('c', 'c', 'q-tenant-a', '{}')`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_default_limits (id, name, project, data)
			values ('d', 'd', 'team'::ltree, '{}')`)
		Expect(err).To(HaveOccurred())
		// Clearing a default archives the row, and a cleared dimension can be set again
		// while the deleted row still awaits the archiver:
		_, err = conn.Exec(ctx, `update quota_default_limits set deletion_timestamp = now() where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_default_limits (id, name, data)
			values ('e', 'e', jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'value', '30')))`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("holds a threshold at a project's scope or the tenant's own", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into quota_thresholds (id, name, tenant, project, data)
			values ('a', 'a', 'q-tenant-a', 'team'::ltree, jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'percent', '80')))`)
		Expect(err).ToNot(HaveOccurred())
		// The tenant's own threshold of the same dimension is a different scope:
		_, err = conn.Exec(ctx, `insert into quota_thresholds (id, name, tenant, project, data)
			values ('b', 'b', 'q-tenant-a', ''::ltree, jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'percent', '90')))`)
		Expect(err).ToNot(HaveOccurred())
		// One threshold per dimension per scope:
		_, err = conn.Exec(ctx, `insert into quota_thresholds (id, name, tenant, project, data)
			values ('c', 'c', 'q-tenant-a', 'team'::ltree, jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'percent', '60')))`)
		Expect(err).To(HaveOccurred())
	})

	It("holds one pending limit request per dimension and tenant", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into quota_limit_requests (id, name, tenant, data)
			values ('a', 'a', 'q-tenant-a', jsonb_build_object(
				'spec', jsonb_build_object('dimension', 'compute.vcpus', 'requested_value', '20'),
				'status', jsonb_build_object('state', 'QUOTA_LIMIT_REQUEST_STATE_PENDING')))`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_limit_requests (id, name, tenant, data)
			values ('b', 'b', 'q-tenant-a', jsonb_build_object(
				'spec', jsonb_build_object('dimension', 'compute.vcpus', 'requested_value', '30'),
				'status', jsonb_build_object('state', 'QUOTA_LIMIT_REQUEST_STATE_PENDING')))`)
		Expect(err).To(HaveOccurred())
		// Once the request is no longer pending, a new one fits:
		_, err = conn.Exec(ctx, `update quota_limit_requests
			set data = jsonb_set(data, '{status,state}', '"QUOTA_LIMIT_REQUEST_STATE_DENIED"') where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_limit_requests (id, name, tenant, data)
			values ('c', 'c', 'q-tenant-a', jsonb_build_object(
				'spec', jsonb_build_object('dimension', 'compute.vcpus', 'requested_value', '30'),
				'status', jsonb_build_object('state', 'QUOTA_LIMIT_REQUEST_STATE_PENDING')))`)
		Expect(err).ToNot(HaveOccurred())
		// A request never carries a project:
		_, err = conn.Exec(ctx, `insert into quota_limit_requests (id, name, tenant, project, data)
			values ('d', 'd', 'q-tenant-a', 'team'::ltree, '{}')`)
		Expect(err).To(HaveOccurred())
		// A deleted pending request doesn't block a new one while it awaits the archiver:
		_, err = conn.Exec(ctx, `update quota_limit_requests set deletion_timestamp = now() where id = 'c'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_limit_requests (id, name, tenant, data)
			values ('e', 'e', 'q-tenant-a', jsonb_build_object(
				'spec', jsonb_build_object('dimension', 'compute.vcpus', 'requested_value', '40'),
				'status', jsonb_build_object('state', 'QUOTA_LIMIT_REQUEST_STATE_PENDING')))`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("holds the outbox rows to their tenant and project", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into quota_outbox (tenant, project, dimension, data)
			values ('q-tenant-a', 'team'::ltree, 'compute.vcpus', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		// A row of an absent tenant or project is refused:
		_, err = conn.Exec(ctx, `insert into quota_outbox (tenant, project, dimension, data)
			values ('absent', ''::ltree, 'compute.vcpus', '{}')`)
		Expect(err).To(HaveOccurred())
		// The rows leave with their project, so a warning an in-flight request wrote after
		// the servers' cleanup cannot strand the project's archival:
		_, err = conn.Exec(ctx, `delete from projects where name = 'team'`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from quota_outbox`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("audits limit changes in the changes table", func(ctx context.Context) {
		Expect(insertLimit(ctx, "a", "q-tenant-a", "", "compute.vcpus", "10")).To(Succeed())
		_, err := conn.Exec(ctx, `update quota_limits set data = jsonb_set(data, '{spec,value}', '20') where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into quota_default_limits (id, name, data)
			values ('d', 'd', jsonb_build_object('spec', jsonb_build_object('dimension', 'compute.vcpus', 'value', '10')))`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		// The insert and the update of the limit, each carrying the full row:
		Expect(conn.QueryRow(ctx, `select count(*) from changes where "table" = 'quota_limits' and data->>'id' = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		Expect(conn.QueryRow(ctx, `select count(*) from changes where "table" = 'quota_default_limits'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})

	It("creates archive storage for every quota object table", func(ctx context.Context) {
		for _, table := range []string{
			"archived_quota_limits", "archived_quota_default_limits",
			"archived_quota_thresholds", "archived_quota_limit_requests",
		} {
			var count int
			Expect(conn.QueryRow(ctx, `select count(*) from `+table).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		}
		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})

	It("materializes the active identifiers of every quota object table", func(ctx context.Context) {
		Expect(insertLimit(ctx, "a", "q-tenant-a", "", "compute.vcpus", "10")).To(Succeed())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from active_quota_limits where id = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		_, err := conn.Exec(ctx, `update quota_limits set deletion_timestamp = now() where id = 'a'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from active_quota_limits where id = 'a'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
})
