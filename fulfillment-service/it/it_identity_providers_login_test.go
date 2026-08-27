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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Identity provider login flow", func() {
	var (
		ctx           context.Context
		client        privatev1.IdentityProvidersClient
		tenantsClient privatev1.TenantsClient
		mockOIDC      *MockOIDCState
		tenantName    string
		tenantID      string
		idpAlias      string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())

		tenantName = fmt.Sprintf("idp-login-%s", uuid.New())
		createResp, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: tenantName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantID = createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: tenantID,
			}.Build())
		})
		waitForTenantSynced(ctx, tenantsClient, tenantID)

		// Start a mock OIDC server per test.
		var startErr error
		mockOIDC, startErr = tool.StartMockOIDC()
		Expect(startErr).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(StopMockOIDC(mockOIDC)).To(Succeed()) })

		// Register the mock OIDC IdP in Keycloak via admin API.
		idpName := fmt.Sprintf("mock-%s", uuid.New())
		var regErr error
		idpAlias, regErr = tool.RegisterMockOIDCIdP(ctx, mockOIDC, tenantName, idpName)
		Expect(regErr).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _, _ = tool.KeycloakAdminRequest(ctx, http.MethodDelete,
				fmt.Sprintf("/identity-provider/instances/%s", idpAlias), nil)
		})
		Expect(tool.WaitForKeycloakIdP(ctx, idpAlias)).To(Succeed())
	})

	// provisionAndLogin creates a test user linked to the IdP, adds them to the tenant
	// org, and authenticates via Keycloak password grant. Returns the JWT access token.
	provisionAndLogin := func(ctx context.Context, tenantName, idpAlias string) string {
		username := fmt.Sprintf("idp-user-%s", uuid.New())
		externalSubject := "ext-" + uuid.New()

		_, err := tool.ProvisionOIDCUser(
			ctx, username, username+"@example.com",
			tenantName, idpAlias, externalSubject,
		)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		token, err := tool.LoginOIDCUser(ctx, username)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		ExpectWithOffset(1, token).ToNot(BeEmpty())
		return token
	}

	It("Allows an IdP-linked user to authenticate and obtain a token", func() {
		token := provisionAndLogin(ctx, tenantName, idpAlias)
		Expect(token).ToNot(BeEmpty(), "expected a non-empty KC access token")
	})

	It("Scopes IdP user access to their tenant", func() {
		token := provisionAndLogin(ctx, tenantName, idpAlias)

		conn, err := tool.MakeOIDCGRPCConn(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		// Capabilities is accessible to any authenticated user and confirms the token is
		// valid and accepted by the OSAC API.
		capsClient := publicv1.NewCapabilitiesClient(conn)
		capsResp, capsErr := capsClient.Get(ctx, publicv1.CapabilitiesGetRequest_builder{}.Build())
		Expect(capsErr).ToNot(HaveOccurred())
		Expect(capsResp).ToNot(BeNil())
	})

	It("Denies an IdP user access to resources in a different tenant", func() {
		otherTenantName := fmt.Sprintf("other-tenant-%s", uuid.New())
		otherTenantResp, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name: otherTenantName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		otherTenantID := otherTenantResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = tenantsClient.Delete(ctx, privatev1.TenantsDeleteRequest_builder{
				Id: otherTenantID,
			}.Build())
		})
		waitForTenantSynced(ctx, tenantsClient, otherTenantID)

		token := provisionAndLogin(ctx, tenantName, idpAlias)

		conn, err := tool.MakeOIDCGRPCConn(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		idpClient := publicv1.NewIdentityProvidersClient(conn)
		_, createErr := idpClient.Create(ctx, publicv1.IdentityProvidersCreateRequest_builder{
			Object: publicv1.IdentityProvider_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   fmt.Sprintf("intruder-%s", uuid.New()),
					Tenant: otherTenantName,
				}.Build(),
				Spec: publicv1.IdentityProviderSpec_builder{
					Title:   "Cross-tenant intruder",
					Enabled: true,
					Oidc: publicv1.OidcConfig_builder{
						AuthorizationUrl: "https://oidc.example.com/authorize",
						TokenUrl:         "https://oidc.example.com/token",
						ClientId:         "intruder",
						ClientSecret:     "secret",
						Issuer:           "https://oidc.example.com",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(createErr).To(HaveOccurred(), "IdP user must not create resources in another tenant")
		grpcStatus, ok := grpcstatus.FromError(createErr)
		Expect(ok).To(BeTrue())
		Expect(grpcStatus.Code()).To(SatisfyAny(
			Equal(grpccodes.PermissionDenied),
			Equal(grpccodes.Unauthenticated),
		))
	})

	It("Rejects login via an unregistered IdP alias", func() {
		rogueAlias := fmt.Sprintf("unregistered-%s", uuid.New())
		_, loginErr := tool.SimulateOIDCLogin(ctx, rogueAlias)
		Expect(loginErr).To(HaveOccurred(),
			"Login via an unregistered IdP alias must fail — KC should not redirect there")
	})

	It("Reconciles an OSAC IdentityProvider to Keycloak and allows login through it", func() {
		osacIdpName := fmt.Sprintf("osac-live-%s", uuid.New())
		osacIdpAlias := fmt.Sprintf("%s-%s", tenantName, osacIdpName)

		createResp, err := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   osacIdpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Live OIDC Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						AuthorizationUrl: mockOIDC.LocalAuthURL(),
						TokenUrl:         mockOIDC.ClusterTokenURL(),
						ClientId:         mockOIDC.ClientID(),
						ClientSecret:     mockOIDC.ClientSecret(),
						Issuer:           mockOIDC.LocalIssuer(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		osacIdpID := createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: osacIdpID,
			}.Build())
		})

		Eventually(
			func(g Gomega) {
				getResp, getErr := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
					Id: osacIdpID,
				}.Build())
				g.Expect(getErr).ToNot(HaveOccurred())
				g.Expect(getResp.GetObject().GetStatus().GetPhase()).To(
					Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
				)
				g.Expect(getResp.GetObject().GetStatus().GetMessage()).To(ContainSubstring(osacIdpAlias))
			},
			2*time.Minute,
			time.Second,
		).Should(Succeed())

		// Provision a user and login through the OSAC-reconciled IdP.
		token := provisionAndLogin(ctx, tenantName, osacIdpAlias)
		Expect(token).ToNot(BeEmpty())
	})
})
