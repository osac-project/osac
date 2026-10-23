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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/proto/metering"
)

// The migration adds the owner resource to the tracked-field copy the database carries, so the
// drift test's wholesale comparison against the proto annotations holds. The attribute names
// the resource a worker's owner reference points at; the calculator resolves the ownership
// root by it and gates the worker's billing on the root's states. The trigger itself never
// reads the attribute: the set-once rule already covers the field.
var _ = DescribeMigration("Add metering owner resources", func() {
	trackedFields := func(ctx context.Context, table string) []metering.Field {
		var config []byte
		err := conn.QueryRow(
			ctx, "select metering_tracked_fields($1)", table,
		).Scan(&config)
		Expect(err).ToNot(HaveOccurred())
		var fields []metering.Field
		Expect(json.Unmarshal(config, &fields)).To(Succeed())
		return fields
	}

	fieldByPath := func(fields []metering.Field, path string) metering.Field {
		for _, field := range fields {
			if field.Path == path {
				return field
			}
		}
		Fail("no tracked field " + path)
		return metering.Field{}
	}

	It("carries the owner resource of a worker's cluster reference", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 136)).To(Succeed())

		fields := trackedFields(ctx, "bare_metal_instances")
		cluster := fieldByPath(fields, "status.cluster")
		Expect(cluster.OwnerResource).To(Equal("Cluster"))
		Expect(cluster.Kind).To(Equal(metering.KindFixedOnceSet))

		// The attribute marks the one owner reference; every other field stays unmarked.
		for _, field := range fields {
			if field.Path == "status.cluster" {
				continue
			}
			Expect(field.OwnerResource).To(BeEmpty(),
				"field %s must not name an owner resource", field.Path)
		}
	})

	It("leaves the other tracked-field lists untouched", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 136)).To(Succeed())

		for _, table := range []string{
			"compute_instances", "clusters", "volumes", "external_ips", "nat_gateways",
		} {
			for _, field := range trackedFields(ctx, table) {
				Expect(field.OwnerResource).To(BeEmpty(),
					"field %s of %s must not name an owner resource", field.Path, table)
			}
		}
	})
})
