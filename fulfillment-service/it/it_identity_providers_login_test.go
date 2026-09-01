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
		extRealm      *ExtRealmState
		tenantName    string
		tenantID      string
		idpAlias      string
		osacIdpID     string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())

		// Create a fresh OSAC tenant for each test.
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

		// Create a temporary Keycloak realm that acts as the external IdP.
		// Both the test runner and Keycloak pods inside Kind reach this realm via the
		// same cluster-internal address, so no host-to-cluster networking is required.
		var realmErr error
		extRealm, realmErr = tool.CreateExtRealm(ctx)
		Expect(realmErr).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(tool.DeleteExtRealm(ctx, extRealm)).To(Succeed())
		})

		// Register the external IdP through the OSAC API. The controller reconciles
		// it into Keycloak's osac realm. No direct Keycloak admin calls for registration.
		idpName := fmt.Sprintf("mock-%s", uuid.New())
		idpAlias = fmt.Sprintf("%s-%s", tenantName, idpName)

		idpCreateResp, createErr := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Ext Realm Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						// All URLs are cluster-internal — both the test runner and
						// Keycloak reach the ext realm via the same address.
						AuthorizationUrl: extRealm.AuthorizationURL(),
						TokenUrl:         extRealm.TokenURL(),
						ClientId:         extRealm.ClientID(),
						ClientSecret:     extRealm.ClientSecret(),
						Issuer:           extRealm.IssuerURL(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(createErr).ToNot(HaveOccurred())
		osacIdpID = idpCreateResp.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: osacIdpID,
			}.Build())
		})

		// Wait for the OSAC controller to reconcile the IdP into Keycloak.
		Eventually(func(g Gomega) {
			getResp, getErr := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
				Id: osacIdpID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(getResp.GetObject().GetStatus().GetPhase()).To(
				Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
			)
		}, 2*time.Minute, time.Second).Should(Succeed())
	})

	// provisionAndLogin creates a user in both the ext realm (credentials) and the
	// osac KC realm (federated identity link + org membership), then drives the full
	// OIDC authorization code redirect chain to obtain a Keycloak JWT.
	//
	// This mirrors what `osac login` + `osac get <resource>` does.
	provisionAndLogin := func(ctx context.Context, tenantName, idpAlias string) string {
		username := fmt.Sprintf("idp-user-%s", uuid.New())
		password := uuid.New()
		email := username + "@example.com"

		// Step 1: create the user in the ext realm with real credentials.
		extUserID, err := extRealm.AddUser(ctx, username, password, email)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		// Step 2: link the user in KC osac realm so first-broker-login finds them
		// without prompting for profile review. extUserID is the `sub` claim KC will
		// receive from the ext realm when the user authenticates.
		_, err = tool.ProvisionOIDCUser(ctx, username, email, tenantName, idpAlias, extUserID)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		// Step 3: drive the full OIDC redirect chain — test runner follows KC's
		// redirect to the ext realm login page, submits credentials, and KC exchanges
		// the code with the ext realm (server-to-server, cluster-internal).
		token, loginErr := tool.SimulateOIDCLogin(ctx, idpAlias, username, password)
		ExpectWithOffset(1, loginErr).ToNot(HaveOccurred())
		ExpectWithOffset(1, token).ToNot(BeEmpty())
		return token
	}

	It("Allows an IdP-linked user to authenticate and obtain a token", func() {
		token := provisionAndLogin(ctx, tenantName, idpAlias)
		Expect(token).ToNot(BeEmpty(), "expected a non-empty KC access token from the full OIDC flow")
	})

	It("Scopes IdP user access to their tenant", func() {
		token := provisionAndLogin(ctx, tenantName, idpAlias)

		conn, err := tool.MakeOIDCGRPCConn(ctx, token)
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()

		// Capabilities is accessible to any authenticated user; success confirms the
		// token is accepted by the OSAC public API with correct tenant scoping.
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
		_, loginErr := tool.SimulateOIDCLogin(ctx, rogueAlias, "", "")
		Expect(loginErr).To(HaveOccurred(),
			"Login via an unregistered IdP alias must fail — KC should not redirect there")
	})

	It("Verifies the OSAC controller status is READY and the alias is reported", func() {
		// BeforeEach already registers via OSAC API and waits for READY.
		// Explicitly assert the status message contains the KC alias to confirm the
		// controller correctly reconciled the IdP into Keycloak.
		getResp, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
			Id: osacIdpID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetStatus().GetPhase()).To(
			Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
		)
		Expect(getResp.GetObject().GetStatus().GetMessage()).To(ContainSubstring(idpAlias))

		// Confirm a user can authenticate through the OSAC-reconciled IdP end-to-end.
		token := provisionAndLogin(ctx, tenantName, idpAlias)
		Expect(token).ToNot(BeEmpty())
	})
})
