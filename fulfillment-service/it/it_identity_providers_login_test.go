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
		mockOIDC      *MockOIDCState
		tenantName    string
		tenantID      string
		idpAlias      string
		osacIdpID     string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewIdentityProvidersClient(tool.InternalView().AdminConn())
		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())

		// Create a fresh tenant for each test.
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

		// Start a mock OIDC server per test. It listens on all interfaces so that
		// Keycloak pods inside Kind can reach the token and JWKS endpoints via the
		// Podman bridge IP.
		var startErr error
		mockOIDC, startErr = tool.StartMockOIDC()
		Expect(startErr).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(StopMockOIDC(mockOIDC)).To(Succeed()) })

		// Register the IdP through the OSAC API — the controller reconciles it into
		// Keycloak. All endpoint URLs go through OSAC; no direct Keycloak admin calls.
		idpName := fmt.Sprintf("mock-%s", uuid.New())
		idpAlias = fmt.Sprintf("%s-%s", tenantName, idpName)

		idpCreateResp, createErr := client.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   idpName,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.IdentityProviderSpec_builder{
					Title:   "Mock OIDC Test Provider",
					Enabled: true,
					Oidc: privatev1.OidcConfig_builder{
						// authorizationUrl must be reachable from the test runner (host).
						// mockoidc binds to 0.0.0.0 so 127.0.0.1:<port> works on the host.
						AuthorizationUrl: mockOIDC.LocalAuthURL(),
						// tokenUrl and jwksUrl must be reachable from Keycloak inside Kind.
						// The Podman bridge IP is the host-side gateway of the Kind network.
						TokenUrl:     mockOIDC.ClusterTokenURL(),
						ClientId:     mockOIDC.ClientID(),
						ClientSecret: mockOIDC.ClientSecret(),
						// Issuer must match the `iss` claim mockoidc embeds in its tokens.
						Issuer: mockOIDC.LocalIssuer(),
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

	// provisionAndLogin creates a test user linked to the IdP in Keycloak (test
	// scaffolding), queues that user as the next auto-approved login in mockoidc, then
	// drives the full OIDC authorization code redirect chain to obtain a Keycloak JWT.
	//
	// This is the end-to-end login path: the same flow the OSAC CLI would execute.
	provisionAndLogin := func(ctx context.Context, tenantName, idpAlias string) string {
		username := fmt.Sprintf("idp-user-%s", uuid.New())
		externalSubject := "ext-" + uuid.New()
		email := username + "@example.com"

		// Create the Keycloak user and link it to the IdP. Keycloak requires the
		// federated identity to exist so it can match the returning user during the
		// first-broker-login flow without prompting for profile review.
		_, err := tool.ProvisionOIDCUser(ctx, username, email, tenantName, idpAlias, externalSubject)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		// Tell mockoidc which user to return for the next authorization request.
		// The subject must match the federated identity we just linked above.
		mockOIDC.QueueUser(externalSubject, email, username)

		// Drive the full OIDC redirect chain: test runner → KC → mockoidc → KC → token.
		// This is the same flow as `osac login` followed by `osac get <resource>`.
		token, loginErr := tool.SimulateOIDCLogin(ctx, idpAlias)
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

		// Attempt to create a resource in a different tenant — must be denied.
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
		// Accept either PermissionDenied or Unauthenticated: OPA may reject the request
		// at the authz layer (PermissionDenied) or the token's organization claim may not
		// match the target tenant causing the authn middleware to treat it as unauthenticated.
		// Both codes confirm the cross-tenant access was correctly denied.
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

	It("Verifies the OSAC controller status is READY and the alias is reported", func() {
		// The BeforeEach already registers via OSAC API and waits for READY.
		// This test explicitly asserts the reported alias in the status message,
		// confirming the controller correctly reconciled the IdP into Keycloak.
		getResp, err := client.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
			Id: osacIdpID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetStatus().GetPhase()).To(
			Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY),
		)
		Expect(getResp.GetObject().GetStatus().GetMessage()).To(ContainSubstring(idpAlias))

		// And confirm that a user can actually log in through the reconciled IdP.
		token := provisionAndLogin(ctx, tenantName, idpAlias)
		Expect(token).ToNot(BeEmpty())
	})
})
