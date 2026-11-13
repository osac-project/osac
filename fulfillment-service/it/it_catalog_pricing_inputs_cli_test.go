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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	// catalogPricingCacheNamespace, catalogPricingCacheDeployment and catalogPricingCacheContainer locate the
	// deployed grpc-server whose inventory cache TTL this spec shortens.
	catalogPricingCacheNamespace  = "osac"
	catalogPricingCacheDeployment = "fulfillment-grpc-server"
	catalogPricingCacheContainer  = "grpc-server"

	// catalogPricingInventoryTTLEnv is the override the spec patches. Four seconds gives the sweep of both catalog
	// tables the cache's half-TTL fill budget of two seconds, and five refreshes still fit inside the 20s poll.
	catalogPricingInventoryTTLEnv = "OSAC_CATALOG_CACHE_INVENTORY_SECONDS"
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

// catalogPricingSetEnv sets name to value in the container's environment, replacing any existing entry.
func catalogPricingSetEnv(container *corev1.Container, name, value string) {
	for i := range container.Env {
		if container.Env[i].Name == name {
			container.Env[i].Value = value
			container.Env[i].ValueFrom = nil
			return
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: name, Value: value})
}

// catalogPricingShortenInventoryTTL patches the deployed fulfillment Deployment to reuse one catalog inventory sweep
// for four seconds instead of the shipped 30s, and restores the original pod spec on cleanup. It follows the
// patch-and-restore pattern the billing provider status specs use for the provider mounts and variables.
func catalogPricingShortenInventoryTTL(ctx context.Context) {
	GinkgoHelper()
	kube := tool.KubeClient()
	key := crclient.ObjectKey{Namespace: catalogPricingCacheNamespace, Name: catalogPricingCacheDeployment}
	current := &appsv1.Deployment{}
	Expect(kube.Get(ctx, key, current)).To(Succeed())
	original := current.Spec.Template.Spec.DeepCopy()
	DeferCleanup(func(ctx context.Context) {
		latest := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		latest.Spec.Template.Spec = *original
		Expect(kube.Update(ctx, latest)).To(Succeed())
		waitForFulfillmentRollout(ctx, key)
	})
	patched := false
	for i := range current.Spec.Template.Spec.Containers {
		container := &current.Spec.Template.Spec.Containers[i]
		if container.Name != catalogPricingCacheContainer {
			continue
		}
		catalogPricingSetEnv(container, catalogPricingInventoryTTLEnv, "4")
		patched = true
	}
	Expect(patched).To(BeTrue(), "container %q not found", catalogPricingCacheContainer)
	Expect(kube.Update(ctx, current)).To(Succeed())
	waitForFulfillmentRollout(ctx, key)
}

// The inventory is derived from the catalog alone, so the dev deployment's unconfigured billing is no gap here:
// these specs exercise the deployed CLI boundary end to end without needing a provider.
var _ = Describe("Catalog pricing inputs CLI", Label("billing"), Serial, func() {
	It("preserves the API total in count-only CLI output in every format", func(ctx context.Context) {
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
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		client := publicv1.NewCatalogPricingInputsClient(tool.ExternalView().AdminConn())
		filter := `this.service == "compute_instance" && this.dimension == "instance_type" && this.value == ""`
		for _, format := range []string{"table", "json", "yaml"} {
			By("comparing count-only " + format + " output with the seeded API inventory")
			// Allow the shipped 30s cache to refresh instead of assuming a preceding spec's snapshot or patching
			// the Deployment. Re-read the total on each attempt; no fixed catalog cardinality is assumed.
			Eventually(ctx, func(g Gomega) {
				attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				list, err := client.List(attemptCtx, publicv1.CatalogPricingInputsListRequest_builder{
					Filter: &filter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.GetTotal()).To(BeNumerically(">", 0))
				g.Expect(catalogPricingGRPCInputRowListsItem(list, item.GetId())).To(BeTrue(),
					"the inventory must include this spec's seed, not merely a positive cached count")
				count, err := client.List(attemptCtx, publicv1.CatalogPricingInputsListRequest_builder{
					Filter: &filter, Limit: new(int32(0)),
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(count.GetSize()).To(BeZero())
				g.Expect(count.GetItems()).To(BeEmpty())
				g.Expect(count.GetTotal()).To(Equal(list.GetTotal()))
				stdout, stderr, exitCode := tool.RunCLI(attemptCtx, homeDir,
					"get", "catalogpricinginput", "--filter", filter, "--limit", "0", "-o", format)
				g.Expect(exitCode).To(Equal(0), "stdout: %s\nstderr: %s", stdout, stderr)
				expectBillingPricingCountOnlyCLI(g, stdout, format, count.GetTotal())
			}, 90*time.Second, 2*time.Second).Should(Succeed())
		}
	})

	It("lists the metered catalog pricing input inventory over the CLI", func(ctx context.Context) {
		// The deployed server reuses one catalog inventory sweep until the cache TTL elapses, so a read right after
		// seeding can observe a snapshot from another item. Shorten that TTL on the deployed Deployment first, then
		// warm the cache before creating the fixture, so the freshness window measured afterwards belongs to this
		// spec's seed.
		catalogPricingShortenInventoryTTL(ctx)
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

		// The patched cache expires after four seconds, so the 20s poll spans several refreshes with margin for a slow
		// one. Each CLI invocation gets its own timeout, kept inside the poll budget so a hung child process is killed
		// instead of stalling the whole poll.
		Eventually(ctx, func(g Gomega) {
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
		}, 20*time.Second, 2*time.Second).Should(Succeed())
	})
})
