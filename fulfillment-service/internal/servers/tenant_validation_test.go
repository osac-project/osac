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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("Tenant validation helpers", func() {

	Describe("resolveObjectTenant", func() {
		It("returns metadata.tenant when set", func() {
			obj := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()

			tenant, err := resolveObjectTenant(context.Background(), obj, tenancy)
			Expect(err).ToNot(HaveOccurred())
			Expect(tenant).To(Equal("tenant-a"))
		})

		It("falls back to DetermineDefaultTenant when metadata.tenant is empty", func() {
			mockCtrl := gomock.NewController(GinkgoT())
			mockTenancy := auth.NewMockTenancyLogic(mockCtrl)
			mockTenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
				Return("fallback-tenant", nil)

			obj := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{}.Build(),
			}.Build()

			tenant, err := resolveObjectTenant(context.Background(), obj, mockTenancy)
			Expect(err).ToNot(HaveOccurred())
			Expect(tenant).To(Equal("fallback-tenant"))
		})

		It("returns error when DetermineDefaultTenant fails", func() {
			mockCtrl := gomock.NewController(GinkgoT())
			mockTenancy := auth.NewMockTenancyLogic(mockCtrl)
			mockTenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
				Return("", fmt.Errorf("auth error"))

			obj := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{}.Build(),
			}.Build()

			_, err := resolveObjectTenant(context.Background(), obj, mockTenancy)
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.Internal))
		})
	})

	Describe("validateTenantMatch", func() {
		It("returns nil when tenants match", func() {
			err := validateTenantMatch("tenant-a", "tenant-a", "Subnet", "sub-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns InvalidArgument when tenants differ", func() {
			err := validateTenantMatch("tenant-a", "tenant-b", "Subnet", "sub-1")
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("cross-tenant"))
			Expect(st.Message()).To(ContainSubstring("tenant-a"))
			Expect(st.Message()).To(ContainSubstring("tenant-b"))
		})
	})

	Describe("validateTenantOrShared", func() {
		It("returns nil when tenants match", func() {
			err := validateTenantOrShared("tenant-a", "tenant-a", "ExternalIPPool", "pool-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns nil when referenced tenant is shared", func() {
			err := validateTenantOrShared("tenant-a", auth.SharedTenant, "ExternalIPPool", "pool-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns InvalidArgument when tenants differ and not shared", func() {
			err := validateTenantOrShared("tenant-a", "tenant-b", "ExternalIPPool", "pool-1")
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("cross-tenant"))
		})
	})

	Describe("fetchAndValidateTenantMatch", func() {
		It("returns nil for same-tenant object", func() {
			obj := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()

			err := fetchAndValidateTenantMatch("tenant-a", obj, "Subnet", "sub-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns error for cross-tenant object", func() {
			obj := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-b",
				}.Build(),
			}.Build()

			err := fetchAndValidateTenantMatch("tenant-a", obj, "Subnet", "sub-1")
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("returns Internal error for nil metadata", func() {
			obj := privatev1.Subnet_builder{}.Build()

			err := fetchAndValidateTenantMatch("tenant-a", obj, "Subnet", "sub-1")
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.Internal))
		})
	})

	Describe("fetchAndValidateTenantOrShared", func() {
		It("returns nil for same-tenant object", func() {
			obj := privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()

			err := fetchAndValidateTenantOrShared("tenant-a", obj, "ExternalIPPool", "pool-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns nil for shared-tenant object", func() {
			obj := privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()

			err := fetchAndValidateTenantOrShared("tenant-a", obj, "ExternalIPPool", "pool-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns error for cross-tenant non-shared object", func() {
			obj := privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-b",
				}.Build(),
			}.Build()

			err := fetchAndValidateTenantOrShared("tenant-a", obj, "ExternalIPPool", "pool-1")
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		})
	})
})
