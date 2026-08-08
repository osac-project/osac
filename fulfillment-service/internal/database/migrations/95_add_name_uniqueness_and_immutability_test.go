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
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add name uniqueness and immutability", func() {
	// createTenantAndProject inserts a tenant and its default project. The default project trigger
	// from migration 69 fires on tenant insert, so only the tenant insert is needed.
	createTenantAndProject := func(ctx context.Context, tenant string) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ($1, $2, $3, 'system', '{}')
			 on conflict do nothing`,
			tenant, tenant, tenant,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Applies successfully", func(ctx context.Context) {
		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects duplicate name within same tenant and project", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "my-cluster", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c2", "my-cluster", "t1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))
	})

	It("Allows same name in different tenants", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")
		createTenantAndProject(ctx, "t2")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "my-cluster", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c2", "my-cluster", "t2",
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects name reuse even after soft-delete", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "my-cluster", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update clusters set deletion_timestamp = now() where id = $1`,
			"c1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c2", "my-cluster", "t1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))
	})

	It("Rejects name update with immutable error", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "original-name", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update clusters set name = $1 where id = $2`,
			"new-name", "c1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("Enforces global uniqueness for roles", func(ctx context.Context) {
		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into roles (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"r1", "custom-role", "shared",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into roles (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"r2", "custom-role", "other-tenant",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))
	})

	It("Enforces tenant-scoped uniqueness for users", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")
		createTenantAndProject(ctx, "t2")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into users (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"u1", "alice", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		// Same name, same tenant: rejected
		_, err = conn.Exec(ctx,
			`insert into users (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"u2", "alice", "t1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))

		// Same name, different tenant: allowed
		_, err = conn.Exec(ctx,
			`insert into users (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"u3", "alice", "t2",
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Replaces partial index on cluster_versions", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		// Insert and soft-delete a cluster version before migration
		_, err := conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, $3, $4)`,
			"cv1", "4-17-0", "t1",
			`{"spec":{"version":"4.17.0","image":"quay.io/openshift/4.17.0"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update cluster_versions set deletion_timestamp = now() where id = $1`,
			"cv1",
		)
		Expect(err).ToNot(HaveOccurred())

		// Before migration 95, reusing the name of a soft-deleted row is allowed
		// because the old partial index only covers active rows.
		_, err = conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, $3, $4)`,
			"cv2", "4-17-0", "t1",
			`{"spec":{"version":"4.17.0-reuse","image":"quay.io/openshift/4.17.0"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		// Clean up cv2 so migration can apply (would violate new full index)
		_, err = conn.Exec(ctx,
			`delete from cluster_versions where id = $1`,
			"cv2",
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		// After migration, reusing the name of a soft-deleted row is rejected
		_, err = conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, $3, $4)`,
			"cv3", "4-17-0", "t1",
			`{"spec":{"version":"4.17.0-new","image":"quay.io/openshift/4.17.0"}}`,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))
	})

	It("Preserves existing tenant and project immutability", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "my-cluster", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update clusters set tenant = $1 where id = $2`,
			"other-tenant", "c1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	It("Does not modify identity_providers", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		// identity_providers already had UNIQUE(tenant, name) and 'name' in its trigger.
		// Verify both still work as before.
		_, err = conn.Exec(ctx,
			`insert into identity_providers (id, name, tenant, data)
			 values ($1, $2, $3, '{"spec":{"type":"IDENTITY_PROVIDER_TYPE_OPEN_ID_CONNECT","open_id_connect":{"issuer_url":"https://example.com","client_id":"test","client_secret":"secret"}},"status":{}}')`,
			"idp1", "my-idp", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into identity_providers (id, name, tenant, data)
			 values ($1, $2, $3, '{"spec":{"type":"IDENTITY_PROVIDER_TYPE_OPEN_ID_CONNECT","open_id_connect":{"issuer_url":"https://example.com","client_id":"test2","client_secret":"secret2"}},"status":{}}')`,
			"idp2", "my-idp", "t1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unique"))

		_, err = conn.Exec(ctx,
			`update identity_providers set name = $1 where id = $2`,
			"renamed-idp", "idp1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("immutable"))
	})

	DescribeTable(
		"Creates a unique index on name for each resource table",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 95)
			Expect(err).ToNot(HaveOccurred())

			var count int
			err = conn.QueryRow(ctx,
				`select count(*)
				 from pg_index ix
				 join pg_class t on t.oid = ix.indrelid
				 join pg_class i on i.oid = ix.indexrelid
				 join pg_attribute a on a.attrelid = t.oid and a.attnum = any(ix.indkey)
				 where t.relname = $1 and ix.indisunique and a.attname = 'name'
				   and not ix.indisprimary`,
				table,
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(BeNumerically(">=", 1),
				fmt.Sprintf("table %s should have a unique index on name", table))
		},
		Entry(nil, "clusters"),
		Entry(nil, "cluster_templates"),
		Entry(nil, "cluster_catalog_items"),
		Entry(nil, "cluster_versions"),
		Entry(nil, "compute_instances"),
		Entry(nil, "compute_instance_templates"),
		Entry(nil, "compute_instance_catalog_items"),
		Entry(nil, "virtual_networks"),
		Entry(nil, "subnets"),
		Entry(nil, "security_groups"),
		Entry(nil, "network_classes"),
		Entry(nil, "nat_gateways"),
		Entry(nil, "external_ip_pools"),
		Entry(nil, "external_ips"),
		Entry(nil, "external_ip_attachments"),
		Entry(nil, "roles"),
		Entry(nil, "role_bindings"),
		Entry(nil, "users"),
		Entry(nil, "objects"),
		Entry(nil, "secrets"),
	)

	DescribeTable(
		"Includes name in immutable trigger args for each resource table",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 95)
			Expect(err).ToNot(HaveOccurred())

			var args string
			err = conn.QueryRow(ctx,
				`select encode(t.tgargs, 'escape')
				 from pg_trigger t
				 join pg_class c on c.oid = t.tgrelid
				 where c.relname = $1 and t.tgname = 'check_immutable_columns'`,
				table,
			).Scan(&args)
			Expect(err).ToNot(HaveOccurred())
			Expect(args).To(ContainSubstring("name"),
				fmt.Sprintf("table %s trigger should include 'name' in its args", table))
		},
		Entry(nil, "clusters"),
		Entry(nil, "cluster_templates"),
		Entry(nil, "cluster_catalog_items"),
		Entry(nil, "cluster_versions"),
		Entry(nil, "compute_instances"),
		Entry(nil, "compute_instance_templates"),
		Entry(nil, "compute_instance_catalog_items"),
		Entry(nil, "virtual_networks"),
		Entry(nil, "subnets"),
		Entry(nil, "security_groups"),
		Entry(nil, "network_classes"),
		Entry(nil, "nat_gateways"),
		Entry(nil, "external_ip_pools"),
		Entry(nil, "external_ips"),
		Entry(nil, "external_ip_attachments"),
		Entry(nil, "roles"),
		Entry(nil, "role_bindings"),
		Entry(nil, "users"),
		Entry(nil, "objects"),
		Entry(nil, "secrets"),
	)

	It("Rejects name update with SQLSTATE Z0001", func(ctx context.Context) {
		createTenantAndProject(ctx, "t1")

		err := tool.Migrate(ctx, 95)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data)
			 values ($1, $2, $3, '{}')`,
			"c1", "my-cluster", "t1",
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update clusters set name = $1 where id = $2`,
			"renamed", "c1",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Z0001"))
	})
})
