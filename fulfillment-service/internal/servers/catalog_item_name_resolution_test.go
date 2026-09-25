/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Catalog item name resolution", func() {
	var catalogItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]

	BeforeEach(func() {
		// The shared tenant is created by a database migration (48_add_builtin_tenants).
		// testTenant is created by the suite-level BeforeEach.

		var err error
		catalogItemsDao, err = dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("finds a shared-tenant catalog item by name from a different tenant", func() {
		// Seed a catalog item in the shared tenant.
		_, err := catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "shared-item-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "gpu-offering",
				Tenant: auth.SharedTenant,
			}.Build(),
			Title:     "GPU Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Resolve by name from a caller whose VM is in testTenant.
		resolved, err := resolveCatalogItemByName(ctx, catalogItemsDao, "gpu-offering", testTenant, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.GetId()).To(Equal("shared-item-id"))
		Expect(resolved.GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
	})

	It("prefers the caller's tenant when name exists in both tenants", func() {
		// Seed catalog items with the same name in both tenants.
		_, err := catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "tenant-item-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "standard-offering",
				Tenant: testTenant,
			}.Build(),
			Title:     "Tenant Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "shared-item-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "standard-offering",
				Tenant: auth.SharedTenant,
			}.Build(),
			Title:     "Shared Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Resolve by name — the caller's tenant should win.
		resolved, err := resolveCatalogItemByName(ctx, catalogItemsDao, "standard-offering", testTenant, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.GetId()).To(Equal("tenant-item-id"))
		Expect(resolved.GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("resolves a catalog item by ID regardless of tenant", func() {
		_, err := catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "direct-id-lookup",
			Metadata: privatev1.Metadata_builder{
				Name:   "by-id-offering",
				Tenant: auth.SharedTenant,
			}.Build(),
			Title:     "ID Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Resolve by ID — should match even though the item is in the shared tenant.
		resolved, err := resolveCatalogItemByName(ctx, catalogItemsDao, "direct-id-lookup", testTenant, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.GetId()).To(Equal("direct-id-lookup"))
	})

	It("finds a catalog item by name in the caller's own tenant", func() {
		_, err := catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "own-tenant-item",
			Metadata: privatev1.Metadata_builder{
				Name:   "local-offering",
				Tenant: testTenant,
			}.Build(),
			Title:     "Local Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		resolved, err := resolveCatalogItemByName(ctx, catalogItemsDao, "local-offering", testTenant, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.GetId()).To(Equal("own-tenant-item"))
		Expect(resolved.GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("returns NotFound for a nonexistent catalog item name", func() {
		_, err := resolveCatalogItemByName(ctx, catalogItemsDao, "nonexistent", testTenant, "")
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})

	It("returns NotFound when a name-only match belongs to an unrelated tenant", func() {
		// Create a second tenant that is neither the caller's tenant nor shared.
		const unrelatedTenant = "other-org"
		tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = tenantsDao.Create().
			SetObject(privatev1.Tenant_builder{
				Id: unrelatedTenant,
				Metadata: privatev1.Metadata_builder{
					Name:   unrelatedTenant,
					Tenant: unrelatedTenant,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Seed a catalog item visible to the admin but owned by the unrelated tenant.
		_, err = catalogItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id: "other-org-item",
			Metadata: privatev1.Metadata_builder{
				Name:   "exclusive-offering",
				Tenant: unrelatedTenant,
			}.Build(),
			Title:     "Other Org Offering",
			Published: true,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// An admin with total visibility resolves by name from testTenant.
		// The item belongs to an unrelated tenant, so it must not be returned.
		_, err = resolveCatalogItemByName(ctx, catalogItemsDao, "exclusive-offering", testTenant, "")
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})
})
