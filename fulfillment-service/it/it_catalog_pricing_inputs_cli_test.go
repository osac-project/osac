/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The inventory is derived from the catalog alone, so the dev deployment's unconfigured billing is no gap here:
// these specs exercise the deployed CLI boundary end to end without needing a provider.
var _ = Describe("Catalog pricing inputs CLI", Label("billing"), func() {
	It("lists the metered catalog pricing input inventory over the CLI", func(ctx context.Context) {
		// Seed one published item offering any instance type, so the spec asserts a real aggregated row through the
		// deployed API instead of passing on an empty catalog.
		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(),
			publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
						Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
					}.Build(),
				}.Build(),
			}.Build())

		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "catalogpricinginput", "-o", "json")
		Expect(exitCode).To(Equal(0))
		var items []map[string]any
		Expect(json.Unmarshal([]byte(stdout), &items)).To(Succeed())
		Expect(items).ToNot(BeEmpty())
		// The seeded item offers any instance type: its (service, dimension) row must come back with the others.
		found := false
		for _, item := range items {
			Expect(item).To(HaveKey("service"))
			Expect(item).To(HaveKey("dimension"))
			if item["service"] == "compute_instance" && item["dimension"] == "instance_type" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "the seeded catalog item's instance_type row is missing")
	})
})
