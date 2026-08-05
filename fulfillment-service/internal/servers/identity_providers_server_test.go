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
	"go.uber.org/mock/gomock"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Identity Providers Server", func() {
	var (
		ctrl         *gomock.Controller
		tenancyLogic *auth.MockTenancyLogic
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		tenancyLogic = auth.NewMockTenancyLogic(ctrl)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("Builder", func() {
		It("builds successfully with all required parameters", func() {
			attributionLogic := auth.NewMockAttributionLogic(ctrl)
			server, err := NewIdentityProvidersServer().
				SetLogger(logger).
				SetTenancyLogic(tenancyLogic).
				SetAttributionLogic(attributionLogic).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("fails if logger is not set", func() {
			server, err := NewIdentityProvidersServer().
				SetTenancyLogic(tenancyLogic).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("fails if tenancy logic is not set", func() {
			server, err := NewIdentityProvidersServer().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Interface compliance", func() {
		It("implements IdentityProvidersServer interface", func() {
			var _ publicv1.IdentityProvidersServer = (*IdentityProvidersServer)(nil)
		})
	})

	Describe("CRUD operations", func() {
		var (
			publicServer *IdentityProvidersServer
		)

		BeforeEach(func() {
			var err error

			// Identity providers require a real tenant (cannot use 'shared'). Create a tenant for tests:
			tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = tenantsDao.Create().
				SetObject(
					privatev1.Tenant_builder{
						Id: "test-tenant",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-tenant",
							Tenant: "test-tenant",
						}.Build(),
					}.Build(),
				).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			publicServer, err = NewIdentityProvidersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates and retrieves an identity provider", func() {
			createResp, err := publicServer.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
				Object: publicv1.IdentityProvider_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "test-idp",
						Tenant: "test-tenant",
					}.Build(),
					Spec: publicv1.IdentityProviderSpec_builder{
						Title:   "Test Identity Provider",
						Enabled: true,
						Oidc: publicv1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							Issuer:           "https://example.com",
							ClientId:         "test-client-id",
							ClientSecret:     "test-secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResp.GetObject().GetId()).ToNot(BeEmpty())
			Expect(createResp.GetObject().GetMetadata().GetName()).To(Equal("test-idp"))

			getResp, err := publicServer.Get(ctx, publicv1.IdentityProvidersGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetId()).To(Equal(createResp.GetObject().GetId()))
			Expect(getResp.GetObject().GetMetadata().GetName()).To(Equal("test-idp"))
		})

		It("lists identity providers", func() {
			for i := range 3 {
				name := "test-idp-" + string(rune('a'+i))
				_, err := publicServer.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
					Object: publicv1.IdentityProvider_builder{
						Metadata: publicv1.Metadata_builder{
							Name:   name,
							Tenant: "test-tenant",
						}.Build(),
						Spec: publicv1.IdentityProviderSpec_builder{
							Title:   "Test IDP " + name,
							Enabled: true,
							Oidc: publicv1.OidcConfig_builder{
								AuthorizationUrl: "https://example.com/auth",
								TokenUrl:         "https://example.com/token",
								Issuer:           "https://example.com",
								ClientId:         "client-id",
								ClientSecret:     "secret",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			listResp, err := publicServer.List(ctx, publicv1.IdentityProvidersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.GetItems()).To(HaveLen(3))
		})

		It("updates an identity provider", func() {
			createResp, err := publicServer.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
				Object: publicv1.IdentityProvider_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "test-idp",
						Tenant: "test-tenant",
					}.Build(),
					Spec: publicv1.IdentityProviderSpec_builder{
						Title:   "Original Title",
						Enabled: true,
						Oidc: publicv1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							Issuer:           "https://example.com",
							ClientId:         "client-id",
							ClientSecret:     "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResp.GetObject()
			object.GetSpec().SetTitle("Updated Title")
			updateResp, err := publicServer.Update(ctx, publicv1.IdentityProvidersUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResp.GetObject().GetSpec().GetTitle()).To(Equal("Updated Title"))
		})

		It("deletes an identity provider", func() {
			createResp, err := publicServer.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
				Object: publicv1.IdentityProvider_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "test-idp",
						Tenant: "test-tenant",
					}.Build(),
					Spec: publicv1.IdentityProviderSpec_builder{
						Title:   "Test IDP",
						Enabled: true,
						Oidc: publicv1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							Issuer:           "https://example.com",
							ClientId:         "client-id",
							ClientSecret:     "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Delete(ctx, publicv1.IdentityProvidersDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
