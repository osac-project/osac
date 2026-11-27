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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create documents tables", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 159)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
			values ('doc-tenant-a', 'doc-tenant-a', 'doc-tenant-a', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into projects (id, tenant, project, name, creator, data)
			values (uuidv7(), 'doc-tenant-a', ''::ltree, 'docs'::ltree, 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("creates the documents tables", func(ctx context.Context) {
		for _, table := range []string{"documents", "archived_documents", "active_documents"} {
			var count int
			Expect(conn.QueryRow(ctx, `select count(*) from `+table).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		}
		Expect(tool.CheckSchema(ctx)).To(Succeed())
	})

	It("adds a nullable description column to every object table", func(ctx context.Context) {
		tables := []string{
			"objects", "add_on_operators", "bare_metal_instances",
			"bare_metal_instance_catalog_items",
			"bare_metal_instance_templates", "bare_metal_instance_types", "clusters",
			"cluster_catalog_items", "cluster_templates", "cluster_versions",
			"compute_instances", "compute_instance_catalog_items", "compute_instance_templates",
			"disk_images", "external_ips", "external_ip_attachments", "external_ip_pools",
			"fabric_domains", "hubs", "identity_providers", "instance_types", "nat_gateways",
			"network_classes", "projects", "project_memberships", "quota_default_limits",
			"quota_limits", "quota_limit_requests", "quota_thresholds", "roles",
			"role_bindings", "secrets", "security_groups", "service_accounts",
			"storage_backends", "storage_tiers", "subnets", "tenants", "users",
			"virtual_networks", "volumes", "documents",
		}
		for _, table := range tables {
			for _, archived := range []string{"", "archived_"} {
				name := archived + table
				var nullable string
				Expect(conn.QueryRow(ctx, `select is_nullable from information_schema.columns
					where table_name = $1 and column_name = 'description'`, name).Scan(&nullable)).To(Succeed())
				Expect(nullable).To(Equal("YES"), name)
			}
		}
	})

	It("enforces unique document names within a tenant and project", func(ctx context.Context) {
		insert := func(id string) error {
			_, err := conn.Exec(ctx, `insert into documents (id, name, tenant, project, data)
				values ($1, 'guide', 'doc-tenant-a', ''::ltree, '{}')`, id)
			return err
		}
		Expect(insert("doc-1")).To(Succeed())
		Expect(insert("doc-2")).ToNot(Succeed())
	})

	It("materializes active document identifiers", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into documents (id, name, tenant, project, data)
			values ('doc-1', 'guide', 'doc-tenant-a', ''::ltree, '{}')`)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(conn.QueryRow(ctx, `select count(*) from active_documents where id = 'doc-1'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		_, err = conn.Exec(ctx, `update documents set deletion_timestamp = now() where id = 'doc-1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from active_documents where id = 'doc-1'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("keeps document identity and ownership immutable", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into documents (id, name, tenant, project, data)
			values ('doc-1', 'guide', 'doc-tenant-a', ''::ltree, '{}')`)
		Expect(err).ToNot(HaveOccurred())
		for _, statement := range []string{
			`update documents set id = 'other' where id = 'doc-1'`,
			`update documents set name = 'other' where id = 'doc-1'`,
			`update documents set tenant = 'other' where id = 'doc-1'`,
			`update documents set project = 'other'::ltree where id = 'doc-1'`,
		} {
			_, err = conn.Exec(ctx, statement)
			Expect(err).To(HaveOccurred(), statement)
		}
	})
})
