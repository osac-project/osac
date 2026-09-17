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
		// Keep these cases in one spec: DescribeMigration creates and migrates a fresh database per spec.
		type dependencyCase struct {
			name, target, catalog, field string
			targetData                   string // Empty means an empty JSON object.
			value                        string // Empty means the direct {"id":"target"} reference.
		}
		cases := []dependencyCase{
			{
				name:    "compute boot disk tier",
				target:  "storage_tiers",
				catalog: "compute_instance_catalog_items",
				field:   "boot_disk",
			},
			{
				name:    "compute additional disk tier",
				target:  "storage_tiers",
				catalog: "compute_instance_catalog_items",
				field:   "additional_disks",
				value:   `{"items":[{"storage_tier":{"id":"target"}}]}`,
			},
			{
				name:    "compute instance type",
				target:  "instance_types",
				catalog: "compute_instance_catalog_items",
				field:   "instance_type",
			},
			{
				name:       "cluster version",
				target:     "cluster_versions",
				catalog:    "cluster_catalog_items",
				field:      "version",
				targetData: `{"spec":{"version":"4.20.0","image":"quay.io/example/release:4.20"}}`,
			},
			{
				name:    "compute subnet",
				target:  "subnets",
				catalog: "compute_instance_catalog_items",
				field:   "network_attachments",
				value:   `{"items":[{"subnet":{"id":"target"}}]}`,
			},
			{
				name:    "compute security group",
				target:  "security_groups",
				catalog: "compute_instance_catalog_items",
				field:   "network_attachments",
				value:   `{"items":[{"security_groups":[{"id":"target"}]}]}`,
			},
			{
				name:    "cluster subnet",
				target:  "subnets",
				catalog: "cluster_catalog_items",
				field:   "network_attachment",
				value:   `{"subnet":{"id":"target"}}`,
			},
			{
				name:    "cluster security group",
				target:  "security_groups",
				catalog: "cluster_catalog_items",
				field:   "network_attachment",
				value:   `{"security_groups":[{"id":"target"}]}`,
			},
			{
				name:    "bare metal subnet",
				target:  "subnets",
				catalog: "bare_metal_instance_catalog_items",
				field:   "network_attachments",
				value:   `{"items":[{"subnet":{"id":"target"}}]}`,
			},
			{
				name:    "bare metal security group",
				target:  "security_groups",
				catalog: "bare_metal_instance_catalog_items",
				field:   "network_attachments",
				value:   `{"items":[{"security_groups":[{"id":"target"}]}]}`,
			},
			{
				name:    "bare metal instance type",
				target:  "bare_metal_instance_types",
				catalog: "bare_metal_instance_catalog_items",
				field:   "instance_type",
			},
			{
				name:    "compute disk image",
				target:  "disk_images",
				catalog: "compute_instance_catalog_items",
				field:   "disk_image",
			},
			{
				name:    "bare metal disk image",
				target:  "disk_images",
				catalog: "bare_metal_instance_catalog_items",
				field:   "disk_image",
			},
			{
				name:       "cluster pull secret",
				target:     "secrets",
				catalog:    "cluster_catalog_items",
				field:      "pull_secret_secret",
				targetData: `{"backend":"SECRET_BACKEND_HUB"}`,
			},
			{
				name:    "cluster node host type",
				target:  "host_types",
				catalog: "cluster_catalog_items",
				field:   "node_sets",
				value:   `{"items":{"arbitrary":{"host_type":{"id":"target"},"size":2}}}`,
			},
		}
		for _, tc := range cases {
			for _, branch := range []string{"locked", "default"} {
				By(fmt.Sprintf("%s in %s policy", tc.name, branch))
				targetData := `{}`
				if tc.targetData != "" {
					targetData = tc.targetData
				}
				_, err := conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ('target', 'target', 'system', $1::jsonb)", tc.target), targetData)
				Expect(err).ToNot(HaveOccurred())
				value := tc.value
				if value == "" {
					value = `{"id":"target"}`
				}
				policy := map[string]any{"locked": json.RawMessage(value)}
				if branch == "default" {
					policy = map[string]any{"editable": map[string]any{"default_value": json.RawMessage(value)}}
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
				if tc.target == "disk_images" {
					referrer := map[string]string{
						"compute_instance_catalog_items":    "compute instance catalog item",
						"bare_metal_instance_catalog_items": "bare metal instance catalog item",
					}[tc.catalog]
					Expect(pgError.Message).To(ContainSubstring("at least one "+referrer), "%s %s", tc.catalog, branch)
				}
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

	It("protects resource and template references, including new guards", func(ctx context.Context) {
		type referenceCase struct {
			name, targetTable, referenceTable string
			targetData, referenceData         func(string) string
		}
		cases := []referenceCase{
			{
				name:           "cluster version from cluster",
				targetTable:    "cluster_versions",
				referenceTable: "clusters",
				targetData:     clusterVersionData,
				referenceData:  jsonAt("spec", "version"),
			},
			{
				name:           "cluster version from template",
				targetTable:    "cluster_versions",
				referenceTable: "cluster_templates",
				targetData:     clusterVersionData,
				referenceData:  jsonAt("spec_defaults", "version"),
			},
			{
				name:           "instance type from compute instance",
				targetTable:    "instance_types",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "instance_type"),
			},
			{
				name:           "instance type from compute template",
				targetTable:    "instance_types",
				referenceTable: "compute_instance_templates",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec_defaults", "instance_type"),
			},
			{
				name:           "disk image from compute instance",
				targetTable:    "disk_images",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "disk_image"),
			},
			{
				name:           "disk image from compute template",
				targetTable:    "disk_images",
				referenceTable: "compute_instance_templates",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec_defaults", "disk_image"),
			},
			{
				name:           "disk image from bare metal instance",
				targetTable:    "disk_images",
				referenceTable: "bare_metal_instances",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "disk_image"),
			},
			{
				name:           "secret from cluster",
				targetTable:    "secrets",
				referenceTable: "clusters",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "pull_secret_secret"),
			},
			{
				name:           "secret from cluster template",
				targetTable:    "secrets",
				referenceTable: "cluster_templates",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec_defaults", "pull_secret_secret"),
			},
			{
				name:           "secret from hub",
				targetTable:    "secrets",
				referenceTable: "hubs",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "kubeconfig_secret"),
			},
			{
				name:           "secret from identity provider",
				targetTable:    "secrets",
				referenceTable: "identity_providers",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "open_id_connect", "client_secret_secret"),
			},
			{
				name:           "secret from storage backend",
				targetTable:    "secrets",
				referenceTable: "storage_backends",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "credentials", "password_secret"),
			},
			{
				name:           "subnet from compute instance",
				targetTable:    "subnets",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonArrayAt("spec", "network_attachments", "subnet"),
			},
			{
				name:           "subnet from cluster",
				targetTable:    "subnets",
				referenceTable: "clusters",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "network_attachment", "subnet"),
			},
			{
				name:           "subnet from bare metal instance",
				targetTable:    "subnets",
				referenceTable: "bare_metal_instances",
				targetData:     emptyJSON,
				referenceData:  jsonArrayAt("spec", "network_attachments", "subnet"),
			},
			{
				name:           "security group from compute instance",
				targetTable:    "security_groups",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonSecurityGroupArrayAt("spec", "network_attachments"),
			},
			{
				name:           "security group from cluster",
				targetTable:    "security_groups",
				referenceTable: "clusters",
				targetData:     emptyJSON,
				referenceData:  jsonSecurityGroupsAt("spec", "network_attachment"),
			},
			{
				name:           "security group from bare metal instance",
				targetTable:    "security_groups",
				referenceTable: "bare_metal_instances",
				targetData:     emptyJSON,
				referenceData:  jsonSecurityGroupArrayAt("spec", "network_attachments"),
			},
			{
				name:           "storage tier from compute boot disk",
				targetTable:    "storage_tiers",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "boot_disk", "storage_tier"),
			},
			{
				name:           "storage tier from compute additional disk",
				targetTable:    "storage_tiers",
				referenceTable: "compute_instances",
				targetData:     emptyJSON,
				referenceData:  jsonArrayAt("spec", "additional_disks", "storage_tier"),
			},
			{
				name:           "storage tier from template boot disk",
				targetTable:    "storage_tiers",
				referenceTable: "compute_instance_templates",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec_defaults", "boot_disk", "storage_tier"),
			},
			{
				name:           "storage tier from template additional disk",
				targetTable:    "storage_tiers",
				referenceTable: "compute_instance_templates",
				targetData:     emptyJSON,
				referenceData:  jsonArrayAt("spec_defaults", "additional_disks", "storage_tier"),
			},
			{
				name:           "bare metal instance type from resource",
				targetTable:    "bare_metal_instance_types",
				referenceTable: "bare_metal_instances",
				targetData:     emptyJSON,
				referenceData:  jsonAt("spec", "instance_type"),
			},
			{
				name:           "host type from cluster",
				targetTable:    "host_types",
				referenceTable: "clusters",
				targetData:     emptyJSON,
				referenceData:  jsonNodeSetAt("spec", "node_sets"),
			},
			{
				name:           "host type from cluster template",
				targetTable:    "host_types",
				referenceTable: "cluster_templates",
				targetData:     emptyJSON,
				referenceData:  jsonNodeSetAt("node_sets"),
			},
			{
				name:           "host type from bare metal template",
				targetTable:    "host_types",
				referenceTable: "bare_metal_instance_templates",
				targetData:     emptyJSON,
				referenceData:  bareMetalTemplateHostTypeData,
			},
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
			if tc.targetTable == "disk_images" {
				referrer := map[string]string{
					"compute_instances":          "compute instance",
					"compute_instance_templates": "compute instance template",
					"bare_metal_instances":       "bare metal instance",
				}[tc.referenceTable]
				Expect(pgError.Message).To(ContainSubstring("at least one "+referrer), tc.name)
			}

			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = $1", tc.referenceTable), "reference-"+id)
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = $1", tc.targetTable), id)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("protects templates while allowing deletion of catalogs used by resources", func(ctx context.Context) {
		for _, kind := range []string{"compute_instance", "cluster", "bare_metal_instance"} {
			By(kind + ": protecting a Template referenced by a Catalog Item")
			template, catalog, resource := kind+"_templates", kind+"_catalog_items", kind+"s"
			_, err := conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ('template', 'template', 'system', '{}')", template))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('catalog', 'catalog', 'system', '{"template":{"id":"template"}}')`, catalog))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'template'", template))
			Expect(err).To(HaveOccurred())
			By(kind + ": allowing Catalog Item deletion while a resource retains provenance")
			_, err = conn.Exec(ctx, fmt.Sprintf(`insert into %s (id, name, tenant, data) values ('resource', 'resource', 'system', '{"spec":{"catalog_item":{"id":"catalog"},"template":{"id":"template"}}}')`, resource))
			Expect(err).ToNot(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'catalog'", catalog))
			Expect(err).ToNot(HaveOccurred())
			By(kind + ": keeping the materialized Template protected by the resource")
			_, err = conn.Exec(ctx, fmt.Sprintf("update %s set deletion_timestamp = now() where id = 'template'", template))
			Expect(err).To(HaveOccurred())
			By(kind + ": allowing Template deletion after resource deletion")
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

func bareMetalTemplateHostTypeData(id string) string {
	return fmt.Sprintf(`{"host_type":%q}`, id)
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
		item := map[string]any{reference: map[string]any{"id": id}}
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: []any{item}},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonSecurityGroupArrayAt(container, field string) func(string) string {
	return func(id string) string {
		groups := []any{map[string]any{"id": id}}
		attachment := map[string]any{"security_groups": groups}
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: []any{attachment}},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonSecurityGroupsAt(container, field string) func(string) string {
	return func(id string) string {
		groups := []any{map[string]any{"id": id}}
		attachment := map[string]any{"security_groups": groups}
		data, err := json.Marshal(map[string]any{
			container: map[string]any{field: attachment},
		})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}

func jsonNodeSetAt(path ...string) func(string) string {
	return func(id string) string {
		hostType := map[string]any{"id": id}
		nodeSet := map[string]any{"host_type": hostType}
		value := any(map[string]any{"worker": nodeSet})
		for i := len(path) - 1; i >= 0; i-- {
			value = map[string]any{path[i]: value}
		}
		data, err := json.Marshal(value)
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
}
