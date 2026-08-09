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

var _ = DescribeMigration("Convert version_name to version reference", func() {
	It("Converts cluster spec.version_name string to spec.version reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant-1', 'tenant-1', 'tenant-1', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data) values ($1, $2, $3)`,
			"cluster-1", "tenant-1",
			`{"spec":{"version_name":"4-17-0"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 93)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from clusters where id = $1`, "cluster-1",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		spec := parsed["spec"].(map[string]interface{})
		_, hasVersionName := spec["version_name"]
		Expect(hasVersionName).To(BeFalse(), "version_name should be removed")

		version := spec["version"].(map[string]interface{})
		Expect(version["name"]).To(Equal("4-17-0"))
	})

	It("Converts cluster template spec_defaults.version_name to version reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ($1, $2, $3)`,
			"template-1", "shared",
			`{"title":"Test","spec_defaults":{"version_name":"4-16-0"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 93)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_templates where id = $1`, "template-1",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		specDefaults := parsed["spec_defaults"].(map[string]interface{})
		_, hasVersionName := specDefaults["version_name"]
		Expect(hasVersionName).To(BeFalse(), "version_name should be removed")

		version := specDefaults["version"].(map[string]interface{})
		Expect(version["name"]).To(Equal("4-16-0"))
	})

	It("Converts catalog item field_definitions path and default", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data) values ($1, $2, $3)`,
			"catalog-1", "shared",
			`{"field_definitions":[{"path":"version_name","default":"4-17-0"},{"path":"pull_secret","default":"secret"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 93)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_catalog_items where id = $1`, "catalog-1",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		fieldDefs := parsed["field_definitions"].([]interface{})
		Expect(fieldDefs).To(HaveLen(2))

		versionFd := fieldDefs[0].(map[string]interface{})
		Expect(versionFd["path"]).To(Equal("version"))
		versionDefault := versionFd["default"].(map[string]interface{})
		Expect(versionDefault["name"]).To(Equal("4-17-0"))

		pullSecretFd := fieldDefs[1].(map[string]interface{})
		Expect(pullSecretFd["path"]).To(Equal("pull_secret"))
		Expect(pullSecretFd["default"]).To(Equal("secret"))
	})

	It("Skips clusters without version_name", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant-1', 'tenant-1', 'tenant-1', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into clusters (id, tenant, data) values ($1, $2, $3)`,
			"cluster-no-version", "tenant-1",
			`{"spec":{"pull_secret":"my-secret"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 93)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from clusters where id = $1`, "cluster-no-version",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		spec := parsed["spec"].(map[string]interface{})
		_, hasVersion := spec["version"]
		Expect(hasVersion).To(BeFalse())
		Expect(spec["pull_secret"]).To(Equal("my-secret"))
	})

	It("Skips catalog items without version_name field definition", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data) values ($1, $2, $3)`,
			"catalog-no-version", "shared",
			`{"field_definitions":[{"path":"pull_secret","default":"secret"}]}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 93)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_catalog_items where id = $1`, "catalog-no-version",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		fieldDefs := parsed["field_definitions"].([]interface{})
		Expect(fieldDefs).To(HaveLen(1))
		fd := fieldDefs[0].(map[string]interface{})
		Expect(fd["path"]).To(Equal("pull_secret"))
	})
})
