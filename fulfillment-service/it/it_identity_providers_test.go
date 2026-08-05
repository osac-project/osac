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
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Identity provider lifecycle", func() {
	var (
		ctx           context.Context
		client        privatev1.IdentityProvidersClient
		tenantsClient privatev1.TenantsClient
		tenantName    string
		tenantID      string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())

		tenantName = fmt.Sprintf("idp-test-%s", uuid.New())
		createResponse, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: tenantName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantID = createResponse.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenantID,
			}.Build())
		})

		waitForTenantSynced(ctx, tenantsClient, tenantID)
	})

	It("Creates an OIDC identity provider and reconciles to READY", func() {
		idpName := fmt.Sprintf("test-oidc-%s", uuid.New())
		expectedAlias := fmt.Sprintf("%s-%s", tenantName, idpName)

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Test OIDC Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: idpID,
			}.Build())
		})

		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				object := getResponse.GetObject()
				g.Expect(object.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				g.Expect(object.GetStatus().GetPhase()).To(
					Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
				)
				g.Expect(object.GetStatus().GetMessage()).To(ContainSubstring(expectedAlias))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", expectedAlias), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusOK))
	})

	It("Deletes an identity provider and removes it from Keycloak", func() {
		idpName := fmt.Sprintf("test-del-%s", uuid.New())
		expectedAlias := fmt.Sprintf("%s-%s", tenantName, idpName)

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Delete Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()

		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getResponse.GetObject().GetStatus().GetPhase()).To(
					Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
				)
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		_, err = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
			Id: idpID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(
			func(g Gomega) {
				_, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", expectedAlias), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("Denies regular users and allows admin to create identity providers", func() {
		userClient := publicv1.NewIdentityProvidersClient(tool.ExternalView().UserConn())
		idpName := fmt.Sprintf("rbac-test-%s", uuid.New())

		_, err := userClient.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
			Object: publicv1.IdentityProvider_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: publicv1.IdentityProviderSpec_builder{
					Title:   "RBAC Test Provider",
					Enabled: true,
					Oidc: publicv1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(SatisfyAny(
			Equal(grpccodes.PermissionDenied),
			Equal(grpccodes.Unauthenticated),
		))

		adminClient := publicv1.NewIdentityProvidersClient(tool.ExternalView().AdminConn())
		adminIdpName := fmt.Sprintf("admin-rbac-%s", uuid.New())
		createResponse, err := adminClient.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
			Object: publicv1.IdentityProvider_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   adminIdpName,
					Tenant: tenantName,
				}.Build(),
				Spec: publicv1.IdentityProviderSpec_builder{
					Title:   "Admin RBAC Test Provider",
					Enabled: true,
					Oidc: publicv1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		if err == nil && createResponse != nil {
			DeferCleanup(func() {
				_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
					Id: createResponse.GetObject().GetId(),
				}.Build())
			})
		} else if err != nil {
			adminStatus, adminOk := grpcstatus.FromError(err)
			Expect(adminOk).To(BeTrue())
			Expect(adminStatus.Code()).ToNot(SatisfyAny(
				Equal(grpccodes.PermissionDenied),
				Equal(grpccodes.Unauthenticated),
			), "Admin should pass authorization even if request fails for other reasons")
		}
	})

	It("Enforces tenant isolation between identity providers", func() {
		tenantBName := fmt.Sprintf("idp-iso-b-%s", uuid.New())
		createTenantBResp, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: tenantBName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantBID := createTenantBResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenantBID,
			}.Build())
		})
		waitForTenantSynced(ctx, tenantsClient, tenantBID)

		idpName := fmt.Sprintf("iso-idp-%s", uuid.New())
		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Isolation Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: idpID,
			}.Build())
		})

		filterTenantA := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
		listResponseA, err := client.List(ctx, privatev1.IdentityProvidersListRequest_builder{
			Filter: &filterTenantA,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponseA.GetTotal()).To(BeNumerically(">=", 1))
		found := false
		for _, item := range listResponseA.GetItems() {
			if item.GetId() == idpID {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "IdP should be visible when filtering by its own tenant")

		filterTenantB := fmt.Sprintf("this.metadata.tenant == %q", tenantBName)
		listResponseB, err := client.List(ctx, privatev1.IdentityProvidersListRequest_builder{
			Filter: &filterTenantB,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		for _, item := range listResponseB.GetItems() {
			Expect(item.GetId()).ToNot(Equal(idpID),
				"IdP should NOT be visible when filtering by a different tenant")
		}
	})

	It("Handles early delete before reconciliation completes", func() {
		idpName := fmt.Sprintf("early-del-%s", uuid.New())
		expectedAlias := fmt.Sprintf("%s-%s", tenantName, idpName)

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Early Delete Test",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		idpID := createResponse.GetObject().GetId()

		_, err = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
			Id: idpID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(
			func(g Gomega) {
				_, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: idpID,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", expectedAlias), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("Rejects identity provider creation with invalid tenants", func() {
		invalidTenants := []string{"shared", "system", ""}

		for _, invalidTenant := range invalidTenants {
			idpName := fmt.Sprintf("invalid-tenant-%s", uuid.New())
			_, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   idpName,
						Tenant: invalidTenant,
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Invalid Tenant Test",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://oidc.example.com/authorize",
							TokenUrl:         "https://oidc.example.com/token",
							ClientId:         "test-client",
							ClientSecret:     "test-secret",
							Issuer:           "https://oidc.example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred(),
				"Creating IdP with tenant %q should fail", invalidTenant)
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument),
				"Expected InvalidArgument for tenant %q, got %v", invalidTenant, status.Code())
		}
	})

	It("Rejects duplicate identity provider name within the same tenant", func() {
		idpName := fmt.Sprintf("dup-test-%s", uuid.New())

		createResponse, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "First Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "test-client",
						ClientSecret:     "test-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
		})

		_, err = client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Duplicate Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "other-client",
						ClientSecret:     "other-secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.AlreadyExists),
			"Duplicate name in same tenant should return AlreadyExists, got %v", status.Code())
	})

	It("Handles concurrent IdP creation across multiple tenants", func() {
		tenantBName := fmt.Sprintf("idp-conc-b-%s", uuid.New())
		createTenantBResp, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: tenantBName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantBID := createTenantBResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenantBID,
			}.Build())
		})
		waitForTenantSynced(ctx, tenantsClient, tenantBID)

		type idpEntry struct {
			id    string
			alias string
		}
		idps := make([]idpEntry, 3)

		names := []string{
			fmt.Sprintf("conc-a1-%s", uuid.New()),
			fmt.Sprintf("conc-a2-%s", uuid.New()),
			fmt.Sprintf("conc-b1-%s", uuid.New()),
		}
		tenants := []string{tenantName, tenantName, tenantBName}

		for i := range 3 {
			resp, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   names[i],
						Tenant: tenants[i],
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   fmt.Sprintf("Concurrent Test %d", i+1),
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://oidc.example.com/authorize",
							TokenUrl:         "https://oidc.example.com/token",
							ClientId:         fmt.Sprintf("client-%d", i),
							ClientSecret:     "test-secret",
							Issuer:           "https://oidc.example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			idps[i] = idpEntry{
				id:    resp.GetObject().GetId(),
				alias: fmt.Sprintf("%s-%s", tenants[i], names[i]),
			}
			DeferCleanup(func() {
				_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
					Id: idps[i].id,
				}.Build())
			})
		}

		for _, entry := range idps {
			entry := entry
			Eventually(
				func(g Gomega) {
					getResponse, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
						Id: entry.id,
					}.Build())
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(getResponse.GetObject().GetStatus().GetPhase()).To(
						Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
					)
				},
				2*time.Minute,
				time.Second,
			).Should(Succeed())
		}

		for _, entry := range idps {
			code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
				fmt.Sprintf("/identity-provider/instances/%s", entry.alias), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusOK),
				"IdP with alias %q should exist in Keycloak", entry.alias)
		}
	})
})
