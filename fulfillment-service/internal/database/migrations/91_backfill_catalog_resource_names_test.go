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

var _ = DescribeMigration("Backfill catalog resource names", func() {
	It("Derives cluster template name from ID", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ($1, $2, $3)`,
			"osac.templates.ocp_small", "shared",
			`{"title":"OpenShift Small Cluster"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 91)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from cluster_templates where id = $1`, "osac.templates.ocp_small",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("ocp-small"))

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_templates where id = $1`, "osac.templates.ocp_small",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		metadata := parsed["metadata"].(map[string]interface{})
		Expect(metadata["name"]).To(Equal("ocp-small"))
	})

	It("Derives network class name from implementation_strategy", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into network_classes (id, tenant, data) values ($1, $2, $3)`,
			"nc-uuid-1", "shared",
			`{"implementation_strategy":"cudn_net","title":"CUDN"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 91)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from network_classes where id = $1`, "nc-uuid-1",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("cudn-net"))
	})

	It("Derives host type name from ID", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into host_types (id, tenant, data) values ($1, $2, $3)`,
			"acme_1tb", "shared",
			`{"title":"Acme 1TB"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 91)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from host_types where id = $1`, "acme_1tb",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("acme-1tb"))
	})

	It("Skips rows that already have a name", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"osac.templates.custom", "my-custom-name", "shared",
			`{"title":"Custom","metadata":{"name":"my-custom-name"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 91)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from cluster_templates where id = $1`, "osac.templates.custom",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("my-custom-name"))
	})

	It("Enforces unique name per tenant after backfill", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into host_types (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ht-1", "same-name", "shared", `{"title":"HT1"}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 91)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into host_types (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ht-2", "same-name", "shared", `{"title":"HT2"}`,
		)
		Expect(err).To(HaveOccurred())
	})
})
