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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// catalogPricingRowListsItem reports whether an aggregated pricing input row's catalog_items reference the given ID.
func catalogPricingRowListsItem(row map[string]any, id string) bool {
	entries, ok := row["catalog_items"].([]any)
	if !ok {
		return false
	}
	for _, entry := range entries {
		if entryMap, ok := entry.(map[string]any); ok && entryMap["id"] == id {
			return true
		}
	}
	return false
}

// The inventory is derived from the catalog alone, so the dev deployment's unconfigured billing is no gap here:
// these specs exercise the deployed CLI boundary end to end without needing a provider.
var _ = Describe("Catalog pricing inputs CLI", Label("billing"), func() {
	It("lists the metered catalog pricing input inventory over the CLI", func(ctx context.Context) {
		// The deployed server shares a catalog inventory cache with a 30s TTL across specs, so a read right after
		// seeding can observe a stale snapshot from another item. Start the CLI session and warm the cache before
		// creating the fixture, so the TTL window measured afterwards belongs to this spec's seed.
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		// Warm the deployed server's inventory cache; only the exit code matters here.
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "get", "catalogpricinginput", "-o", "json")
		Expect(exitCode).To(Equal(0), "cache-warming read should succeed")

		// Seed one published item offering any instance type, so the spec asserts a real aggregated row through the
		// deployed API instead of passing on an empty catalog.
		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(),
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
		itemID := item.GetId()

		// The shared cache expires after 30s: 60s is twice the TTL plus margin, polled every 2s. Each CLI invocation
		// gets its own timeout so a hung child process is killed instead of stalling the whole poll budget.
		Eventually(ctx, func(g Gomega) {
			callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			stdout, stderr, exitCode := tool.RunCLI(callCtx, homeDir, "get", "catalogpricinginput", "-o", "json")
			g.Expect(exitCode).To(Equal(0), "CLI read failed\nstdout: %s\nstderr: %s", stdout, stderr)
			var items []map[string]any
			g.Expect(json.Unmarshal([]byte(stdout), &items)).To(Succeed(),
				"inventory is not valid JSON\nstdout: %s\nstderr: %s", stdout, stderr)
			g.Expect(items).ToNot(BeEmpty())
			// Every aggregated row must be shaped; the seeded item's row must appear once the cache refreshes.
			found := false
			for _, row := range items {
				g.Expect(row).To(HaveKey("service"))
				g.Expect(row).To(HaveKey("dimension"))
				if row["service"] != "compute_instance" || row["dimension"] != "instance_type" {
					continue
				}
				value, _ := row["value"].(string)
				if value == "" && row["accepts_any_value"] == true && catalogPricingRowListsItem(row, itemID) {
					found = true
				}
			}
			g.Expect(found).To(BeTrue(),
				"the seeded catalog item's instance_type row is missing\nstdout: %s\nstderr: %s", stdout, stderr)
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})
})
