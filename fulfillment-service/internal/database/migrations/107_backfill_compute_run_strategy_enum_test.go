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

var _ = DescribeMigration("Backfill compute run_strategy to enum name", func() {
	insertTenant := func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('tenant1', 'tenant1', 'tenant1', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Converts compute instance run_strategy from short name to enum name", func(ctx context.Context) {
		insertTenant(ctx)
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ci-always", "ci-always", "tenant1",
			`{"spec":{"run_strategy":"Always","disk_image":{"id":"img"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into compute_instances (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ci-halted", "ci-halted", "tenant1",
			`{"spec":{"run_strategy":"Halted","disk_image":{"id":"img"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 107)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage

		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-always",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		Expect(parsed["spec"].(map[string]interface{})["run_strategy"]).To(Equal("COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"))

		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-halted",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		Expect(parsed["spec"].(map[string]interface{})["run_strategy"]).To(Equal("COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"))
	})

	It("Skips compute instances without run_strategy", func(ctx context.Context) {
		insertTenant(ctx)
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ci-no-rs", "ci-no-rs", "tenant1",
			`{"spec":{"disk_image":{"id":"img"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 107)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-no-rs",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		_, hasRunStrategy := parsed["spec"].(map[string]interface{})["run_strategy"]
		Expect(hasRunStrategy).To(BeFalse())
	})

	It("Skips compute instances where run_strategy is already the enum name", func(ctx context.Context) {
		insertTenant(ctx)
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, name, tenant, data) values ($1, $2, $3, $4)`,
			"ci-already-enum", "ci-already-enum", "tenant1",
			`{"spec":{"run_strategy":"COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS","disk_image":{"id":"img"}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 107)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instances where id = $1`, "ci-already-enum",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		Expect(parsed["spec"].(map[string]interface{})["run_strategy"]).To(Equal("COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"))
	})

	It("Converts compute instance template spec_defaults.run_strategy", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into compute_instance_templates (id, tenant, data) values ($1, $2, $3)`,
			"tmpl-halted", "shared",
			`{"spec_defaults":{"run_strategy":"Halted"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 107)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_templates where id = $1`, "tmpl-halted",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())
		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		Expect(parsed["spec_defaults"].(map[string]interface{})["run_strategy"]).To(Equal("COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"))
	})
})
