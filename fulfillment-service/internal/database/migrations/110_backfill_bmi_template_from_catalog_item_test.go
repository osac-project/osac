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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Backfill BMI spec.template from catalog item", func() {
	insertTenant := func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant1', 'tenant1', 'tenant1', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	}

	insertCatalogItem := func(ctx context.Context, id, name, templateID string) {
		data := `{"template":{"id":"` + templateID + `"}}`
		_, err := conn.Exec(ctx,
			`insert into bare_metal_instance_catalog_items (id, name, tenant, data)
			 values ($1, $2, 'shared', $3::jsonb)`,
			id, name, data)
		Expect(err).ToNot(HaveOccurred())
	}

	insertCatalogItemWithoutTemplate := func(ctx context.Context, id, name string) {
		_, err := conn.Exec(ctx,
			`insert into bare_metal_instance_catalog_items (id, name, tenant, data)
			 values ($1, $2, 'shared', '{}'::jsonb)`,
			id, name)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Copies catalog item template reference into spec.template", func(ctx context.Context) {
		insertTenant(ctx)
		insertCatalogItem(ctx, "ci-1", "gpu-node", "tmpl-gpu")

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-1", "bmi-1", "tenant1",
			`{"spec":{"catalog_item":{"id":"ci-1"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-1",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		tmpl := spec["template"].(map[string]any)
		Expect(tmpl["id"]).To(Equal("tmpl-gpu"))
	})

	It("Resolves catalog item by name when only name is set", func(ctx context.Context) {
		insertTenant(ctx)
		insertCatalogItem(ctx, "ci-2", "cpu-node", "tmpl-cpu")

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-2", "bmi-2", "tenant1",
			`{"spec":{"catalog_item":{"name":"cpu-node"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-2",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		tmpl := spec["template"].(map[string]any)
		Expect(tmpl["id"]).To(Equal("tmpl-cpu"))
	})

	It("Skips BMIs that already have spec.template", func(ctx context.Context) {
		insertTenant(ctx)
		insertCatalogItem(ctx, "ci-3", "ci-3", "tmpl-new")

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-3", "bmi-3", "tenant1",
			`{"spec":{"catalog_item":{"id":"ci-3"},"template":{"id":"tmpl-existing"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-3",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		tmpl := spec["template"].(map[string]any)
		Expect(tmpl["id"]).To(Equal("tmpl-existing"))
	})

	It("Skips BMIs without catalog_item", func(ctx context.Context) {
		insertTenant(ctx)

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-4", "bmi-4", "tenant1",
			`{"spec":{}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-4",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		_, hasTemplate := spec["template"]
		Expect(hasTemplate).To(BeFalse())
	})

	It("Skips BMIs whose catalog item does not exist", func(ctx context.Context) {
		insertTenant(ctx)

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-5", "bmi-5", "tenant1",
			`{"spec":{"catalog_item":{"id":"nonexistent"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-5",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		_, hasTemplate := spec["template"]
		Expect(hasTemplate).To(BeFalse())
	})

	It("Skips catalog items without a template and preserves the BMI data", func(ctx context.Context) {
		insertTenant(ctx)
		insertCatalogItemWithoutTemplate(ctx, "ci-6", "missing-template")

		original := `{"metadata":{"name":"bmi-6"},"spec":{"catalog_item":{"id":"ci-6"}},"status":{"state":"pending"}}`
		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-6", "bmi-6", "tenant1", original)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-6",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(MatchJSON(original))
	})

	It("Prefers an ID match over a name match", func(ctx context.Context) {
		insertTenant(ctx)
		insertCatalogItem(ctx, "ci-7", "id-match", "tmpl-by-id")
		insertCatalogItem(ctx, "ci-8", "ci-7", "tmpl-by-name")

		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $2, $3, $4::jsonb)`,
			"bmi-7", "bmi-7", "tenant1",
			`{"spec":{"catalog_item":{"id":"ci-7","name":"ci-7"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 110)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from bare_metal_instances where id = $1`, "bmi-7",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		tmpl := spec["template"].(map[string]any)
		Expect(tmpl["id"]).To(Equal("tmpl-by-id"))
	})
})
