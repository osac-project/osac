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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("deployed MCP server", func() {
	It("serves MCP tools through TLS with a user-scoped OAuth token", func() {
		endpoint := strings.TrimSuffix(os.Getenv("IT_MCP_DEPLOYED_URL"), "/")
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		Expect(endpoint).To(HavePrefix("https://"))
		resourceURL := endpoint + "/"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		baseTransport := &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: tool.caPool.Pool(), MinVersion: tls.VersionTLS12,
		}}
		defer baseTransport.CloseIdleConnections()
		baseClient := &http.Client{Transport: baseTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}

		Expect(probeMCPMetadata(ctx, baseClient, endpoint)).To(Succeed())

		userToken, err := tool.SimulateMCPLogin(ctx, resourceURL, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		mcpClient := &http.Client{Transport: bearerRoundTripper{base: baseTransport, token: userToken, origin: endpoint}}
		client := mcp.NewClient(&mcp.Implementation{Name: "it-mcp-deployed-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: mcpClient}, nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(session.Close()).To(Succeed()) }()
		tools, err := session.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(tools.Tools).To(HaveLen(5))
		before, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
		})
		Expect(err).ToNot(HaveOccurred())
		connection, err := callMCPTool[mcpserver.CheckConnectionOutput](
			ctx, session, "check_connection", mcpserver.CheckConnectionInput{},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(connection.Endpoint).To(Equal(resourceURL))
		Expect(connection.Caller.Subject).ToNot(BeEmpty())
		Expect(connection.Tools).To(HaveLen(5))
		after, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(after.Items).To(Equal(before.Items))

		// The regular Fulfillment API token has a different audience and must not
		// be accepted by the MCP listener.
		apiToken, err := tool.UserTokenSource().Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		wrongAudienceReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		wrongAudienceReq.Header.Set("Authorization", "Bearer "+apiToken.Access)
		wrongAudienceResp, err := baseClient.Do(wrongAudienceReq)
		Expect(err).ToNot(HaveOccurred())
		defer wrongAudienceResp.Body.Close()
		Expect(wrongAudienceResp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("rejects a Keycloak token from a different issuer", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		token, err := keycloakMasterToken(ctx)
		Expect(err).ToNot(HaveOccurred())
		claims, err := parseJWTClaims(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims["iss"]).To(Equal(fmt.Sprintf("https://%s/realms/master", keycloakAddr)))

		statusCode, err := postMCPAuthorizationProbe(ctx, endpoint, token)
		Expect(err).ToNot(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusUnauthorized))
	})

	It("rejects an expired token issued by the MCP client", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		clientID, original, err := getKeycloakClient(ctx, "osac-mcp-client")
		Expect(err).ToNot(HaveOccurred())
		shortLived, err := cloneJSONMap(original)
		Expect(err).ToNot(HaveOccurred())
		attributes, ok := shortLived["attributes"].(map[string]interface{})
		if !ok {
			attributes = make(map[string]interface{})
			shortLived["attributes"] = attributes
		}
		attributes["access.token.lifespan"] = "1"
		Expect(updateKeycloakClient(ctx, clientID, shortLived)).To(Succeed())
		DeferCleanup(func() {
			Expect(updateKeycloakClient(context.Background(), clientID, original)).To(Succeed())
		})

		resourceURL := endpoint + "/"
		token, err := tool.SimulateMCPLogin(ctx, resourceURL, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		claims, err := parseJWTClaims(token)
		Expect(err).ToNot(HaveOccurred())
		issuedAt, err := claims.GetIssuedAt()
		Expect(err).ToNot(HaveOccurred())
		expiresAt, err := claims.GetExpirationTime()
		Expect(err).ToNot(HaveOccurred())
		Expect(expiresAt.Time.Sub(issuedAt.Time)).To(BeNumerically("<=", 3*time.Second))

		Eventually(func() int {
			statusCode, err := postMCPAuthorizationProbe(ctx, endpoint, token)
			if err != nil {
				return 0
			}
			return statusCode
		}, 20*time.Second, 250*time.Millisecond).Should(Equal(http.StatusUnauthorized))
	})

	It("rejects a token issued for a different MCP resource", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		wrongResource := endpoint + "/different/"
		wrongResourceClientID, err := createKeycloakMCPResourceClient(ctx, wrongResource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(deleteKeycloakClient(context.Background(), wrongResourceClientID)).To(Succeed())
		})

		token, err := tool.simulateOIDCLogin(
			ctx, wrongResource, "http://localhost:8091/callback", wrongResource, "",
			userUsername, usersPassword,
		)
		Expect(err).ToNot(HaveOccurred())
		claims, err := parseJWTClaims(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(jwtAudiences(claims)).To(ContainElement(wrongResource))
		Expect(jwtAudiences(claims)).ToNot(ContainElement(endpoint + "/"))

		statusCode, err := postMCPAuthorizationProbe(ctx, endpoint, token)
		Expect(err).ToNot(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusUnauthorized))
	})

	It("returns an authentication error when Keycloak denies token exchange", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		resourceURL := endpoint + "/"
		token, err := tool.SimulateMCPLogin(ctx, resourceURL, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		_, err = callDeployedMCPTool[mcpserver.CheckConnectionOutput](
			ctx, endpoint, token, "check_connection", mcpserver.CheckConnectionInput{},
		)
		Expect(err).ToNot(HaveOccurred())

		clientID, original, err := getKeycloakClient(ctx, resourceURL)
		Expect(err).ToNot(HaveOccurred())
		denied, err := cloneJSONMap(original)
		Expect(err).ToNot(HaveOccurred())
		attributes, ok := denied["attributes"].(map[string]any)
		Expect(ok).To(BeTrue())
		attributes["standard.token.exchange.enabled"] = "false"
		Expect(updateKeycloakClient(ctx, clientID, denied)).To(Succeed())
		DeferCleanup(func() {
			Expect(updateKeycloakClient(context.Background(), clientID, original)).To(Succeed())
		})

		_, err = callDeployedMCPTool[mcpserver.CheckConnectionOutput](
			ctx, endpoint, token, "check_connection", mcpserver.CheckConnectionInput{},
		)
		expectMCPToolError(err, "authentication", "Unauthenticated", false)
	})

	It("rejects a token exchange that changes the caller's tenant permissions", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		username := "mcp-contract-" + uuid.New()[24:32]
		code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{
			"username": username, "email": username + "@test.local", "emailVerified": true,
			"enabled": true, "firstName": username, "lastName": "MCPContract", "requiredActions": []string{},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated), string(body))
		userID, err := tool.keycloakEnsureUserReady(ctx, username)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			code, body, err := tool.KeycloakAdminRequest(context.Background(), http.MethodDelete,
				fmt.Sprintf("/users/%s", userID), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(code == http.StatusNoContent || code == http.StatusNotFound, string(body)).To(BeTrue())
		})
		Expect(tool.ensureUserInOrg(ctx, username, usersGroup)).To(Succeed())

		resourceURL := endpoint + "/"
		token, err := tool.SimulateMCPLogin(ctx, resourceURL, username, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		claims, err := parseJWTClaims(token)
		Expect(err).ToNot(HaveOccurred())
		Expect(jwtAudiences(claims)).To(ContainElement(resourceURL))

		Expect(tool.ensureUserInOrg(ctx, username, "tenant2")).To(Succeed())
		_, err = callDeployedMCPTool[mcpserver.CheckConnectionOutput](
			ctx, endpoint, token, "check_connection", mcpserver.CheckConnectionInput{},
		)
		expectMCPToolError(err, "authentication", "Unauthenticated", false)
	})

	It("returns a retryable unavailable error while Keycloak is down", func() {
		endpoint := deployedMCPURL()
		if endpoint == "" {
			Skip("IT_MCP_DEPLOYED_URL is not set")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		resourceURL := endpoint + "/"
		token, err := tool.SimulateMCPLogin(ctx, resourceURL, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		_, err = callDeployedMCPTool[mcpserver.CheckConnectionOutput](
			ctx, endpoint, token, "check_connection", mcpserver.CheckConnectionInput{},
		)
		Expect(err).ToNot(HaveOccurred(), "prime the MCP server's Keycloak signing-key cache")

		deploymentClient := tool.KubeClientSet().AppsV1().Deployments("keycloak")
		deployment, err := deploymentClient.Get(ctx, "keycloak-service", metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		replicas := int32(1)
		if deployment.Spec.Replicas != nil {
			replicas = *deployment.Spec.Replicas
		}
		Expect(replicas).To(BeNumerically(">", 0))
		DeferCleanup(func() {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer restoreCancel()
			Expect(setKeycloakReplicas(restoreCtx, replicas)).To(Succeed())
			Expect(tool.waitForKCRollout(restoreCtx, "keycloak", "keycloak-service")).To(Succeed())
		})

		Expect(setKeycloakReplicas(ctx, 0)).To(Succeed())
		selector := metav1.FormatLabelSelector(deployment.Spec.Selector)
		Eventually(func(g Gomega) {
			current, err := deploymentClient.Get(ctx, "keycloak-service", metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(current.Status.ReadyReplicas).To(Equal(int32(0)))
			pods, err := tool.KubeClientSet().CoreV1().Pods("keycloak").List(
				ctx, metav1.ListOptions{LabelSelector: selector},
			)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(pods.Items).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		claims, parseErr := parseJWTClaims(token)
		issuedAt, iatErr := claims.GetIssuedAt()
		expiresAt, expErr := claims.GetExpirationTime()
		switch {
		case parseErr != nil:
			logger.WarnContext(ctx, "MCP outage token timing unavailable",
				"reason", "claims_parse_failed",
			)
		case iatErr != nil || expErr != nil:
			logger.WarnContext(ctx, "MCP outage token timing unavailable",
				"reason", "invalid_timing_claims",
			)
		case issuedAt == nil || expiresAt == nil:
			logger.WarnContext(ctx, "MCP outage token timing unavailable",
				"reason", "missing_timing_claims",
			)
		default:
			now := time.Now().UTC()
			logger.InfoContext(ctx, "MCP outage token timing",
				"now", now.Format(time.RFC3339Nano),
				"iat", issuedAt.UTC(), "exp", expiresAt.UTC(),
				"lifetime_seconds", expiresAt.Sub(issuedAt.Time).Seconds(),
				"remaining_seconds", expiresAt.Sub(now).Seconds(),
			)
		}

		Eventually(func(g Gomega) {
			_, err = callDeployedMCPTool[mcpserver.CheckConnectionOutput](
				ctx, endpoint, token, "check_connection", mcpserver.CheckConnectionInput{},
			)
			expectMCPToolErrorWithGomega(g, err, "service_unavailable", "Unavailable", true)
		}, 45*time.Second, time.Second).Should(Succeed())
	})
})

func deployedMCPURL() string {
	return strings.TrimSuffix(os.Getenv("IT_MCP_DEPLOYED_URL"), "/")
}

func postMCPAuthorizationProbe(ctx context.Context, endpoint, token string) (int, error) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: tool.caPool.Pool(), MinVersion: tls.VersionTLS12,
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func callDeployedMCPTool[Out any](ctx context.Context, endpoint, token, name string, arguments any) (Out, error) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: tool.caPool.Pool(), MinVersion: tls.VersionTLS12,
	}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: bearerRoundTripper{
		base: transport, token: token, origin: endpoint,
	}}
	client := mcp.NewClient(&mcp.Implementation{Name: "it-mcp-contract-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}, nil)
	if err != nil {
		var output Out
		return output, err
	}
	defer func() { Expect(session.Close()).To(Succeed()) }()
	return callMCPTool[Out](ctx, session, name, arguments)
}

func keycloakMasterToken(ctx context.Context) (string, error) {
	store, err := auth.NewMemoryTokenStore().SetLogger(logger).Build()
	if err != nil {
		return "", err
	}
	source, err := oauth.NewTokenSource().
		SetLogger(logger).
		SetStore(store).
		SetCaPool(tool.CaPool()).
		SetIssuer(fmt.Sprintf("https://%s/realms/master", keycloakAddr)).
		SetFlow(oauth.PasswordFlow).
		SetClientId("admin-cli").
		SetUsername("admin").
		SetPassword("admin").
		SetScopes("openid").
		Build()
	if err != nil {
		return "", err
	}
	token, err := source.Token(ctx)
	if err != nil {
		return "", err
	}
	return token.Access, nil
}

func parseJWTClaims(token string) (jwt.MapClaims, error) {
	parsed, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected JWT claims type %T", parsed.Claims)
	}
	return claims, nil
}

func jwtAudiences(claims jwt.MapClaims) []string {
	var audiences []string
	switch value := claims["aud"].(type) {
	case string:
		audiences = append(audiences, value)
	case []any:
		for _, audience := range value {
			if value, ok := audience.(string); ok {
				audiences = append(audiences, value)
			}
		}
	}
	return audiences
}

func getKeycloakClient(ctx context.Context, clientID string) (string, map[string]any, error) {
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
		"/clients?clientId="+url.QueryEscape(clientID), nil)
	if err != nil {
		return "", nil, err
	}
	if code != http.StatusOK {
		return "", nil, fmt.Errorf("get Keycloak client %q: HTTP %d", clientID, code)
	}
	var clients []map[string]any
	if err := json.Unmarshal(body, &clients); err != nil {
		return "", nil, fmt.Errorf("decode Keycloak client %q: %w", clientID, err)
	}
	if len(clients) != 1 {
		return "", nil, fmt.Errorf("expected one Keycloak client %q, got %d", clientID, len(clients))
	}
	id, ok := clients[0]["id"].(string)
	if !ok || id == "" {
		return "", nil, fmt.Errorf("Keycloak client %q has no internal ID", clientID)
	}
	return id, clients[0], nil
}

func createKeycloakMCPResourceClient(ctx context.Context, resourceURL string) (string, error) {
	payload := map[string]any{
		"clientId":                  resourceURL,
		"name":                      "OSAC MCP wrong-resource test client",
		"enabled":                   true,
		"protocol":                  "openid-connect",
		"publicClient":              true,
		"standardFlowEnabled":       true,
		"directAccessGrantsEnabled": false,
		"serviceAccountsEnabled":    false,
		"fullScopeAllowed":          true,
		"redirectUris":              []string{"http://localhost:8091/callback"},
		"defaultClientScopes":       []string{"groups", "basic", "username", "organization", "roles", "osac-api"},
		"attributes": map[string]string{
			"standard.token.exchange.enabled": "true",
			"resource_url":                    resourceURL,
		},
		"protocolMappers": []map[string]any{{
			"name":            "wrong-resource-audience",
			"protocol":        "openid-connect",
			"protocolMapper":  "oidc-audience-mapper",
			"consentRequired": false,
			"config": map[string]string{
				"included.client.audience": resourceURL,
				"id.token.claim":           "false",
				"access.token.claim":       "true",
			},
		}},
	}
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/clients", payload)
	if err != nil {
		return "", err
	}
	if code != http.StatusCreated {
		return "", fmt.Errorf("create Keycloak resource client: HTTP %d: %s", code, string(body))
	}
	clientID, _, err := getKeycloakClient(ctx, resourceURL)
	return clientID, err
}

func deleteKeycloakClient(ctx context.Context, clientID string) error {
	code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodDelete, "/clients/"+clientID, nil)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent && code != http.StatusNotFound {
		return fmt.Errorf("delete Keycloak client: HTTP %d: %s", code, string(body))
	}
	return nil
}

func cloneJSONMap(original map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func updateKeycloakClient(ctx context.Context, clientID string, client map[string]any) error {
	code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPut, "/clients/"+clientID, client)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent && code != http.StatusOK {
		return fmt.Errorf("update Keycloak client: HTTP %d", code)
	}
	return nil
}

func expectMCPToolError(err error, category, grpcCode string, retryable bool) {
	expectMCPToolErrorWithGomega(Default, err, category, grpcCode, retryable)
}

func expectMCPToolErrorWithGomega(g Gomega, err error, category, grpcCode string, retryable bool) {
	g.Expect(err).To(HaveOccurred())
	message := err.Error()
	jsonStart := strings.Index(message, "{")
	g.Expect(jsonStart).To(BeNumerically(">=", 0), message)
	var envelope struct {
		Category  string `json:"category"`
		GRPCCode  string `json:"grpc_code"`
		Retryable bool   `json:"retryable"`
	}
	g.Expect(json.Unmarshal([]byte(message[jsonStart:]), &envelope)).To(Succeed())
	g.Expect(envelope.Category).To(Equal(category))
	g.Expect(envelope.GRPCCode).To(Equal(grpcCode))
	g.Expect(envelope.Retryable).To(Equal(retryable))
}

func setKeycloakReplicas(ctx context.Context, replicas int32) error {
	deployments := tool.KubeClientSet().AppsV1().Deployments("keycloak")
	deployment, err := deployments.Get(ctx, "keycloak-service", metav1.GetOptions{})
	if err != nil {
		return err
	}
	deployment.Spec.Replicas = &replicas
	_, err = deployments.Update(ctx, deployment, metav1.UpdateOptions{})
	return err
}

type bearerRoundTripper struct {
	base   http.RoundTripper
	token  string
	origin string
}

func (t bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme+"://"+request.URL.Host != t.origin {
		return nil, fmt.Errorf("refusing to send MCP bearer token to another origin")
	}
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(request)
}
