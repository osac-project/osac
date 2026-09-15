/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*/

package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Typed catalog policy reverse references", func() {
	BeforeEach(func(ctx context.Context) { Expect(tool.Migrate(ctx, 114)).To(Succeed()) })

	It("protects locked and editable default dependencies of active catalog items", func(ctx context.Context) {
		cases := []struct {
			target, catalog, field string
			value                  any
		}{
			{"storage_tiers", "compute_instance_catalog_items", "boot_disk", nil},
			{"storage_tiers", "compute_instance_catalog_items", "additional_disks", map[string]any{"items": []any{map[string]any{"storage_tier": map[string]any{"id": "target"}}}}},
			{"instance_types", "compute_instance_catalog_items", "instance_type", map[string]any{"id": "target"}},
			{"cluster_versions", "cluster_catalog_items", "version", map[string]any{"id": "target"}},
			{"subnets", "compute_instance_catalog_items", "network_attachments", map[string]any{"items": []any{map[string]any{"subnet": map[string]any{"id": "target"}}}}},
			{"security_groups", "compute_instance_catalog_items", "network_attachments", map[string]any{"items": []any{map[string]any{"security_groups": []any{map[string]any{"id": "target"}}}}}},
			{"subnets", "cluster_catalog_items", "network_attachment", map[string]any{"subnet": map[string]any{"id": "target"}}},
			{"security_groups", "cluster_catalog_items", "network_attachment", map[string]any{"security_groups": []any{map[string]any{"id": "target"}}}},
			{"subnets", "bare_metal_instance_catalog_items", "network_attachments", map[string]any{"items": []any{map[string]any{"subnet": map[string]any{"id": "target"}}}}},
			{"security_groups", "bare_metal_instance_catalog_items", "network_attachments", map[string]any{"items": []any{map[string]any{"security_groups": []any{map[string]any{"id": "target"}}}}}},

			{"bare_metal_instance_types", "bare_metal_instance_catalog_items", "instance_type", map[string]any{"id": "target"}},
			{"disk_images", "compute_instance_catalog_items", "disk_image", map[string]any{"id": "target"}},
			{"disk_images", "bare_metal_instance_catalog_items", "disk_image", map[string]any{"id": "target"}},
			{"secrets", "cluster_catalog_items", "pull_secret_secret", map[string]any{"id": "target"}},
			{"host_types", "cluster_catalog_items", "node_sets", map[string]any{"items": map[string]any{"arbitrary": map[string]any{"host_type": map[string]any{"id": "target"}, "size": 2}}}},
		}
		for _, tc := range cases {
			for _, branch := range []string{"locked", "default"} {
				targetData := `{}`
				if tc.target == "secrets" {
					targetData = `{"backend":"SECRET_BACKEND_HUB"}`
				}
				if tc.target == "cluster_versions" {
					targetData = `{"spec":{"version":"4.20.0","image":"quay.io/example/release:4.20"}}`
				}
				_, err := conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ('target', 'target', 'system', $1::jsonb)", tc.target), targetData)
				Expect(err).ToNot(HaveOccurred())
				value := tc.value
				if value == nil {
					value = map[string]any{"id": "target"}
				}
				policy := map[string]any{"locked": value}
				if branch == "default" {
					policy = map[string]any{"editable": map[string]any{"default_value": value}}
				}
				var field any = policy
				if tc.field == "boot_disk" {
					field = map[string]any{"storage_tier": policy}
				}
				data, err := json.Marshal(map[string]any{"published": branch == "locked", "fields": map[string]any{tc.field: field}})
				Expect(err).ToNot(HaveOccurred())
				_, err = conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ('catalog', 'catalog', 'system', $1::jsonb)", tc.catalog), string(data))
				Expect(err).ToNot(HaveOccurred())
				_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'target'", tc.target))
				var pgErr *pgconn.PgError
				Expect(err).To(BeAssignableToTypeOf(pgErr), "%s %s %s", tc.target, tc.field, branch)
				var pgError *pgconn.PgError
				Expect(errors.As(err, &pgError)).To(BeTrue())
				Expect(pgError.Code).To(Equal("Z0003"))
				_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'catalog'", tc.catalog))
				Expect(err).ToNot(HaveOccurred())
				_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'target'", tc.target))
				Expect(err).ToNot(HaveOccurred())
				_, err = conn.Exec(ctx, fmt.Sprintf("delete from %s where id = 'catalog'", tc.catalog))
				Expect(err).ToNot(HaveOccurred())
				_, err = conn.Exec(ctx, fmt.Sprintf("delete from %s where id = 'target'", tc.target))
				Expect(err).ToNot(HaveOccurred())
			}
		}
	})

	It("uses canonical version IDs even when another version has the same name", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into projects (id, name, tenant, project, data) values ('other', 'other', 'system', '', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into cluster_versions (id, name, tenant, project, data) values
			('target', '4-20', 'system', '', '{"spec":{"version":"4.20.0","image":"quay.io/example/release:4.20"}}'),
			('other', '4-20', 'system', 'other', '{"spec":{"version":"4.20.0","image":"quay.io/example/release:4.20"}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into cluster_catalog_items (id, name, tenant, data)
			values ('catalog', 'catalog', 'system', '{"fields":{"version":{"locked":{"id":"target","name":"4-20"}}}}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update cluster_versions set deletion_timestamp = now() where id = 'other'`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update cluster_versions set deletion_timestamp = now() where id = 'target'`)
		var pgError *pgconn.PgError
		Expect(errors.As(err, &pgError)).To(BeTrue())
		Expect(pgError.Code).To(Equal("Z0003"))
	})

	It("preserves deletion protection for existing resource and template references", func(ctx context.Context) {
		cases := []struct {
			name, targetTable, referenceTable string
			targetData, referenceData         func(string) string
		}{
			{"cluster version from cluster", "cluster_versions", "clusters", clusterVersionData, jsonAt("spec", "version")},
			{"cluster version from template", "cluster_versions", "cluster_templates", clusterVersionData, jsonAt("spec_defaults", "version")},
			{"instance type from compute instance", "instance_types", "compute_instances", emptyJSON, jsonAt("spec", "instance_type")},
			{"instance type from compute template", "instance_types", "compute_instance_templates", emptyJSON, jsonAt("spec_defaults", "instance_type")},
			{"disk image from compute instance", "disk_images", "compute_instances", emptyJSON, jsonAt("spec", "disk_image")},
			{"disk image from compute template", "disk_images", "compute_instance_templates", emptyJSON, jsonAt("spec_defaults", "disk_image")},
			{"disk image from bare metal instance", "disk_images", "bare_metal_instances", emptyJSON, jsonAt("spec", "disk_image")},
			{"secret from cluster", "secrets", "clusters", emptyJSON, jsonAt("spec", "pull_secret_secret")},
			{"secret from cluster template", "secrets", "cluster_templates", emptyJSON, jsonAt("spec_defaults", "pull_secret_secret")},
			{"secret from hub", "secrets", "hubs", emptyJSON, jsonAt("spec", "kubeconfig_secret")},
			{"secret from identity provider", "secrets", "identity_providers", emptyJSON, jsonAt("spec", "open_id_connect", "client_secret_secret")},
			{"secret from storage backend", "secrets", "storage_backends", emptyJSON, jsonAt("spec", "credentials", "password_secret")},
			{"subnet from compute instance", "subnets", "compute_instances", emptyJSON, jsonArrayAt("spec", "network_attachments", "subnet")},
			{"subnet from cluster", "subnets", "clusters", emptyJSON, jsonAt("spec", "network_attachment", "subnet")},
			{"subnet from bare metal instance", "subnets", "bare_metal_instances", emptyJSON, jsonArrayAt("spec", "network_attachments", "subnet")},
			{"security group from compute instance", "security_groups", "compute_instances", emptyJSON, jsonSecurityGroupArrayAt("spec", "network_attachments")},
			{"security group from cluster", "security_groups", "clusters", emptyJSON, jsonSecurityGroupsAt("spec", "network_attachment")},
			{"security group from bare metal instance", "security_groups", "bare_metal_instances", emptyJSON, jsonSecurityGroupArrayAt("spec", "network_attachments")},
			{"storage tier from compute boot disk", "storage_tiers", "compute_instances", emptyJSON, jsonAt("spec", "boot_disk", "storage_tier")},
			{"storage tier from compute additional disk", "storage_tiers", "compute_instances", emptyJSON, jsonArrayAt("spec", "additional_disks", "storage_tier")},
			{"storage tier from template boot disk", "storage_tiers", "compute_instance_templates", emptyJSON, jsonAt("spec_defaults", "boot_disk", "storage_tier")},
			{"storage tier from template additional disk", "storage_tiers", "compute_instance_templates", emptyJSON, jsonArrayAt("spec_defaults", "additional_disks", "storage_tier")},
			{"bare metal instance type from resource", "bare_metal_instance_types", "bare_metal_instances", emptyJSON, jsonAt("spec", "instance_type")},
			{"host type from cluster", "host_types", "clusters", emptyJSON, jsonNodeSetAt("spec", "node_sets")},
			{"host type from cluster template", "host_types", "cluster_templates", emptyJSON, jsonNodeSetAt("node_sets")},
			{"host type from bare metal template", "host_types", "bare_metal_instance_templates", emptyJSON, func(id string) string {
				return fmt.Sprintf(`{"host_type":%q}`, id)
			}},
		}

		for i, tc := range cases {
			By(tc.name)
			id := fmt.Sprintf("preserved-%d", i)
			_, err := conn.Exec(ctx, fmt.Sprintf(
				"insert into %s (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)", tc.targetTable,
			), id, tc.targetData(id))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf(
				"insert into %s (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)", tc.referenceTable,
			), "reference-"+id, tc.referenceData(id))
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = $1", tc.targetTable), id)
			var pgError *pgconn.PgError
			Expect(errors.As(err, &pgError)).To(BeTrue(), tc.name)
			Expect(pgError.Code).To(Equal("Z0003"), tc.name)

			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = $1", tc.referenceTable), "reference-"+id)
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = $1", tc.targetTable), id)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("protects templates while allowing deletion of catalogs used by resources", func(ctx context.Context) {
		for _, kind := range []string{"compute_instance", "cluster", "bare_metal_instance"} {
			template, catalog, resource := kind+"_templates", kind+"_catalog_items", kind+"s"
			_, err := conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ('template', 'template', 'system', '{}')", template))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('catalog', 'catalog', 'system', '{"template":{"id":"template"}}')`, catalog))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'template'", template))
			Expect(err).To(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('resource', 'resource', 'system', '{"spec":{"catalog_item":{"id":"catalog"},"template":{"id":"template"}}}')`, resource))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'catalog'", catalog))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'template'", template))
			Expect(err).To(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'resource'", resource))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'template'", template))
			Expect(err).ToNot(HaveOccurred())
		}
	})
})

func emptyJSON(string) string {
	return `{}`
}

func clusterVersionData(id string) string {
	return fmt.Sprintf(`{"spec":{"version":%q,"image":"quay.io/example/release:4.20"}}`, id)
}

func jsonAt(path ...string) func(string) string {
	return func(id string) string {
		value := any(map[string]any{"id": id})
		for i := len(path) - 1; i >= 0; i-- {
			value = map[string]any{path[i]: value}
		}
		data, err := json.Marshal(value)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonArrayAt(container, field, reference string) func(string) string {
	return func(id string) string {
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: []any{map[string]any{reference: map[string]any{"id": id}}}},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonSecurityGroupArrayAt(container, field string) func(string) string {
	return func(id string) string {
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: []any{map[string]any{"security_groups": []any{map[string]any{"id": id}}}}},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonSecurityGroupsAt(container, field string) func(string) string {
	return func(id string) string {
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: map[string]any{"security_groups": []any{map[string]any{"id": id}}}},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonNodeSetAt(path ...string) func(string) string {
	return func(id string) string {
		value := any(map[string]any{"worker": map[string]any{"host_type": map[string]any{"id": id}}})
		for i := len(path) - 1; i >= 0; i-- {
			value = map[string]any{path[i]: value}
		}
		data, err := json.Marshal(value)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}
