/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add ExternalIP consumer reference indexes", func() {
	It("Indexes both ExternalIP reference fields on active and deleting consumers", func(ctx context.Context) {
		err := tool.Migrate(ctx, 120)
		Expect(err).ToNot(HaveOccurred())

		indexes := map[string]string{
			"external_ip_attachments_external_ip_ref_id_idx":   "'id'",
			"external_ip_attachments_external_ip_ref_name_idx": "'name'",
			"nat_gateways_external_ip_ref_id_idx":              "'id'",
			"nat_gateways_external_ip_ref_name_idx":            "'name'",
		}
		for name, referenceField := range indexes {
			var definition string
			err = conn.QueryRow(ctx, `select indexdef from pg_indexes where indexname = $1`, name).Scan(&definition)
			Expect(err).ToNot(HaveOccurred())
			Expect(definition).To(ContainSubstring(referenceField))
			Expect(definition).ToNot(ContainSubstring(" WHERE "))
		}
	})
})
