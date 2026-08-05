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

var _ = DescribeMigration("Backfill template reference fields", func() {
	It("Converts cluster template node_sets host_type from string to reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ($1, $2, $3)`,
			"osac.templates.ocp_small", "shared",
			`{"title":"OpenShift Small","node_sets":{"fc430":{"host_type":"fc430","size":2},"g5":{"host_type":"g5","size":1}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 92)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_templates where id = $1`, "osac.templates.ocp_small",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		nodeSets := parsed["node_sets"].(map[string]interface{})
		fc430 := nodeSets["fc430"].(map[string]interface{})
		hostType := fc430["host_type"].(map[string]interface{})
		Expect(hostType["id"]).To(Equal("fc430"))

		g5 := nodeSets["g5"].(map[string]interface{})
		hostTypeG5 := g5["host_type"].(map[string]interface{})
		Expect(hostTypeG5["id"]).To(Equal("g5"))
	})

	It("Skips cluster templates where host_type is already a reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ($1, $2, $3)`,
			"osac.templates.already_ref", "shared",
			`{"title":"Already Ref","node_sets":{"fc430":{"host_type":{"id":"fc430"},"size":2}}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 92)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from cluster_templates where id = $1`, "osac.templates.already_ref",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		nodeSets := parsed["node_sets"].(map[string]interface{})
		fc430 := nodeSets["fc430"].(map[string]interface{})
		hostType := fc430["host_type"].(map[string]interface{})
		Expect(hostType["id"]).To(Equal("fc430"))
	})

	It("Converts compute instance template instance_type from string to reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into compute_instance_templates (id, tenant, data) values ($1, $2, $3)`,
			"osac.templates.ocp_virt_vm", "shared",
			`{"title":"OCP Virt VM","spec_defaults":{"instance_type":"standard-4vcpu"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 92)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_templates where id = $1`, "osac.templates.ocp_virt_vm",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		specDefaults := parsed["spec_defaults"].(map[string]interface{})
		instanceType := specDefaults["instance_type"].(map[string]interface{})
		Expect(instanceType["id"]).To(Equal("standard-4vcpu"))
	})

	It("Skips compute instance templates without instance_type", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into compute_instance_templates (id, tenant, data) values ($1, $2, $3)`,
			"osac.templates.no_instance_type", "shared",
			`{"title":"No Instance Type","spec_defaults":{"run_strategy":"Always"}}`,
		)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 92)
		Expect(err).ToNot(HaveOccurred())

		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select data from compute_instance_templates where id = $1`, "osac.templates.no_instance_type",
		).Scan(&data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())

		specDefaults := parsed["spec_defaults"].(map[string]interface{})
		_, hasInstanceType := specDefaults["instance_type"]
		Expect(hasInstanceType).To(BeFalse())
	})
})
