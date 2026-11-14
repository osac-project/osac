/*
Copyright (c) 2025 Red Hat Inc.

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
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("REST gateway", func() {
	var (
		ctx             context.Context
		templatesClient privatev1.ClusterTemplatesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
	})

	It("Should use protobuf field names in JSON representation", func() {
		// Create a cluster template:
		templateID := newTemplateID("my")
		_, err := templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateID,
				Title:       "My template",
				Description: "My template.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Retrieve the template via REST API:
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("/api/fulfillment/v1/cluster_templates/%s", templateID),
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().UserClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		data, err := io.ReadAll(response.Body)
		Expect(err).ToNot(HaveOccurred())
		var body map[string]any
		err = json.Unmarshal(data, &body)
		Expect(err).ToNot(HaveOccurred())

		// Verify field names:
		Expect(body).NotTo(HaveKey("node_sets"))
	})

	It("Should be possible to fetch cluster templates via private REST API", func() {
		// Create a cluster template:
		templateID := newTemplateID("my")
		_, err := templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateID,
				Title:       "My private template",
				Description: "My private template.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Retrieve the template via private REST API:
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("/api/private/v1/cluster_templates/%s", templateID),
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.InternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		data, err := io.ReadAll(response.Body)
		Expect(err).ToNot(HaveOccurred())
		var body map[string]any
		err = json.Unmarshal(data, &body)
		Expect(err).ToNot(HaveOccurred())

		// Verify the response contains the expected fields:
		Expect(body).To(HaveKey("id"))
		Expect(body).To(HaveKey("title"))
		Expect(body).To(HaveKey("description"))
		Expect(body).NotTo(HaveKey("node_sets"))
		Expect(body["id"]).To(Equal(templateID))
		Expect(body["title"]).To(Equal("My private template"))
		Expect(body["description"]).To(Equal("My private template."))

	})

	It("returns an unauthenticated error for an expired Keycloak token", func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()

		_, originalClient, err := getKeycloakClient(ctx, "osac-cli")
		Expect(err).ToNot(HaveOccurred())
		shortLivedClient, err := cloneJSONMap(originalClient)
		Expect(err).ToNot(HaveOccurred())
		testClientName := "rest-expired-" + uuid.New()
		delete(shortLivedClient, "id")
		if protocolMappers, ok := shortLivedClient["protocolMappers"].([]any); ok {
			for _, value := range protocolMappers {
				if mapper, ok := value.(map[string]any); ok {
					delete(mapper, "id")
				}
			}
		}
		shortLivedClient["clientId"] = testClientName
		shortLivedClient["name"] = "Fulfillment expired-token integration test"
		attributes, ok := shortLivedClient["attributes"].(map[string]interface{})
		if !ok {
			attributes = make(map[string]interface{})
			shortLivedClient["attributes"] = attributes
		}
		attributes["access.token.lifespan"] = "1"
		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/clients", shortLivedClient)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated))
		DeferCleanup(func(cleanupCtx context.Context) {
			clientID, _, getErr := getKeycloakClient(cleanupCtx, testClientName)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(deleteKeycloakClient(cleanupCtx, clientID)).To(Succeed())
		})

		token, err := tool.simulateOIDCLogin(
			ctx, testClientName, "http://localhost", "", "", userUsername, usersPassword,
		)
		Expect(err).ToNot(HaveOccurred())
		claims, err := parseJWTClaims(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims["iss"]).To(Equal(fmt.Sprintf("https://%s/realms/osac", keycloakAddr)))
		Expect(jwtAudiences(claims)).To(ContainElement("osac-api"))
		issuedAt, err := claims.GetIssuedAt()
		Expect(err).ToNot(HaveOccurred())
		expiresAt, err := claims.GetExpirationTime()
		Expect(err).ToNot(HaveOccurred())
		Expect(expiresAt.Time.Sub(issuedAt.Time)).To(BeNumerically("<=", 3*time.Second))

		staticTokenSource, err := auth.NewStaticTokenSource().
			SetLogger(tool.logger).
			SetToken(&auth.Token{Access: token, Expiry: expiresAt.Time}).
			Build()
		Expect(err).ToNot(HaveOccurred())
		conn, err := tool.makeGrpcConn(externalServiceAddr, staticTokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(conn.Close()).To(Succeed()) })

		By("waiting for the protected gRPC method to reject the expired bearer")
		clusterTemplates := publicv1.NewClusterTemplatesClient(conn)
		Eventually(func(g Gomega) {
			_, listErr := clusterTemplates.List(ctx, publicv1.ClusterTemplatesListRequest_builder{}.Build())
			g.Expect(grpcstatus.Code(listErr)).To(Equal(grpccodes.Unauthenticated))
			g.Expect(grpcstatus.Convert(listErr).Message()).To(ContainSubstring("token expired"))
		}, 20*time.Second, 250*time.Millisecond).Should(Succeed())

		By("checking that the public REST gateway maps the expired token to HTTP 401")
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/fulfillment/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := tool.ExternalView().AnonymousClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusUnauthorized))
		body, err := io.ReadAll(response.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("token expired"))
	})

	// The dev deployment configures no billing provider, so the billing REST routes carry the disabled-mode
	// error: FAILED_PRECONDITION over gRPC maps to HTTP 400 here. The loop covers the public routes through the
	// external view and the private routes through the internal view, so both surfaces' disabled-mode contract
	// is pinned at the route level.
	It("Should reject the billing routes with 400 when billing is not configured", Label("billing"), func() {
		public := map[string]func(*http.Request) (*http.Response, error){
			"/api/fulfillment/v1/billing/accounts":                   tool.ExternalView().AdminClient().Do,
			"/api/fulfillment/v1/billing/tenant_billing_assignments": tool.ExternalView().AdminClient().Do,
			"/api/private/v1/billing/accounts":                       tool.InternalView().AdminClient().Do,
			"/api/private/v1/billing/tenant_billing_assignments":     tool.InternalView().AdminClient().Do,
		}
		for path, do := range public {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			Expect(err).ToNot(HaveOccurred())
			response, err := do(request)
			Expect(err).ToNot(HaveOccurred())
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			Expect(err).ToNot(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusBadRequest), path)
			Expect(string(data)).To(ContainSubstring("billing is not configured"), path)
		}
	})
})
