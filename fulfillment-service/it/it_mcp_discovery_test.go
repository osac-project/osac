/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("MCP authorized discovery", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns caller-scoped Projects and reference-only Secret metadata", func() {
		revokeAdamTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, "adam")
		revokeBenTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, "ben")

		adamProjectConn, err := tool.MakeUserConnection(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(adamProjectConn.Close()).To(Succeed()) })
		_, err = publicv1.NewProjectsClient(adamProjectConn).List(ctx, publicv1.ProjectsListRequest_builder{
			Limit: new(int32(1)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		benProjectConn, err := tool.MakeUserConnection(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(benProjectConn.Close()).To(Succeed()) })
		_, err = publicv1.NewProjectsClient(benProjectConn).List(ctx, publicv1.ProjectsListRequest_builder{
			Limit: new(int32(1)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		adminConn := tool.InternalView().AdminConn()
		adminProjects := privatev1.NewProjectsClient(adminConn)
		adminMemberships := privatev1.NewProjectMembershipsClient(adminConn)
		adminUsers := privatev1.NewUsersClient(adminConn)
		adamProjectID := createMCPDiscoveryProject(
			ctx, publicv1.NewProjectsClient(adamProjectConn), adminProjects, adminMemberships, adminUsers, "adam", "engineering",
		)
		benProjectID := createMCPDiscoveryProject(
			ctx, publicv1.NewProjectsClient(benProjectConn), adminProjects, adminMemberships, adminUsers, "ben", "development",
		)
		revokeAdamTenantAdmin(ctx)
		revokeBenTenantAdmin(ctx)

		adamConn, err := tool.MakeUserConnection(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(adamConn.Close()).To(Succeed()) })
		benConn, err := tool.MakeUserConnection(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(benConn.Close()).To(Succeed()) })

		adamProjects := publicv1.NewProjectsClient(adamConn)
		waitForMCPDiscoveryProject(ctx, adamProjects, adamProjectID)
		waitForMCPDiscoveryProject(ctx, publicv1.NewProjectsClient(benConn), benProjectID)
		adamSecretID, adamSecretBytes := createMCPDiscoverySecret(ctx, adamConn, "adam")
		benSecretID, benSecretBytes := createMCPDiscoverySecret(ctx, benConn, "ben")

		jwksCache, err := auth.NewJwksCache().
			SetLogger(logger).
			SetCaPool(tool.CaPool()).
			AddIssuers(keycloakIssuer(ctx)).
			Build()
		Expect(err).ToNot(HaveOccurred())
		jwtValidator, err := auth.NewJwtValidator().
			SetLogger(logger).
			SetJwksCache(jwksCache).
			SetExpirationLeeway(5 * time.Second).
			Build()
		Expect(err).ToNot(HaveOccurred())
		grpcConn, err := network.NewGrpcClient().
			SetLogger(logger).
			SetCaPool(tool.CaPool()).
			SetAddress(externalServiceAddr).
			SetUserAgent("fulfillment-mcp-discovery-it").
			Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(grpcConn.Close()).To(Succeed()) })
		handler, err := mcpserver.NewHandler(
			mcpserver.NewServerDeps(grpcConn), jwtValidator, "", "",
			mcpserver.TokenExchangerFunc(func(_ context.Context, subjectToken string, _ *jwt.Token) (string, error) {
				return subjectToken, nil
			}),
		)
		Expect(err).ToNot(HaveOccurred())
		httpServer := httptest.NewServer(handler)
		DeferCleanup(httpServer.Close)

		adamSession := connectMCPDiscoveryCaller(ctx, httpServer.URL, "adam")
		DeferCleanup(func() { Expect(adamSession.Close()).To(Succeed()) })
		benSession := connectMCPDiscoveryCaller(ctx, httpServer.URL, "ben")
		DeferCleanup(func() { Expect(benSession.Close()).To(Succeed()) })

		adamProjectsList, _ := listMCPDiscoveryResource(ctx, adamSession, mcpserver.ResourceTypeProject, adamProjectID)
		Expect(resourceIDs(adamProjectsList)).To(ContainElement(adamProjectID))
		Expect(resourceIDs(adamProjectsList)).ToNot(ContainElement(benProjectID))
		adamProjectsOther, _ := listMCPDiscoveryResource(ctx, adamSession, mcpserver.ResourceTypeProject, benProjectID)
		Expect(adamProjectsOther.Items).To(BeEmpty())
		Expect(adamProjectsOther.Size).To(BeZero())
		Expect(adamProjectsOther.Total).To(BeZero())
		benProjectsList, _ := listMCPDiscoveryResource(ctx, benSession, mcpserver.ResourceTypeProject, benProjectID)
		Expect(resourceIDs(benProjectsList)).To(ContainElement(benProjectID))
		Expect(resourceIDs(benProjectsList)).ToNot(ContainElement(adamProjectID))
		benProjectsOther, _ := listMCPDiscoveryResource(ctx, benSession, mcpserver.ResourceTypeProject, adamProjectID)
		Expect(benProjectsOther.Items).To(BeEmpty())
		Expect(benProjectsOther.Size).To(BeZero())
		Expect(benProjectsOther.Total).To(BeZero())

		adamSecretsList, adamSecretsResult := listMCPDiscoveryResource(
			ctx, adamSession, mcpserver.ResourceTypeSecretReference, adamSecretID,
		)
		Expect(resourceIDs(adamSecretsList)).To(ContainElement(adamSecretID))
		Expect(resourceIDs(adamSecretsList)).ToNot(ContainElement(benSecretID))
		adamSecretsOther, _ := listMCPDiscoveryResource(ctx, adamSession, mcpserver.ResourceTypeSecretReference, benSecretID)
		Expect(adamSecretsOther.Items).To(BeEmpty())
		Expect(adamSecretsOther.Size).To(BeZero())
		Expect(adamSecretsOther.Total).To(BeZero())
		benSecretsList, benSecretsResult := listMCPDiscoveryResource(
			ctx, benSession, mcpserver.ResourceTypeSecretReference, benSecretID,
		)
		Expect(resourceIDs(benSecretsList)).To(ContainElement(benSecretID))
		Expect(resourceIDs(benSecretsList)).ToNot(ContainElement(adamSecretID))
		benSecretsOther, _ := listMCPDiscoveryResource(ctx, benSession, mcpserver.ResourceTypeSecretReference, adamSecretID)
		Expect(benSecretsOther.Items).To(BeEmpty())
		Expect(benSecretsOther.Size).To(BeZero())
		Expect(benSecretsOther.Total).To(BeZero())

		adamSecretsResultJSON, err := json.Marshal(adamSecretsResult)
		Expect(err).To(Succeed())
		Expect(string(adamSecretsResultJSON)).ToNot(ContainSubstring(adamSecretBytes))
		Expect(string(adamSecretsResultJSON)).ToNot(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(adamSecretBytes))))
		benSecretsResultJSON, err := json.Marshal(benSecretsResult)
		Expect(err).To(Succeed())
		Expect(string(benSecretsResultJSON)).ToNot(ContainSubstring(benSecretBytes))
		Expect(string(benSecretsResultJSON)).ToNot(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(benSecretBytes))))

		encodedAdamSecrets, err := json.Marshal(adamSecretsList)
		Expect(err).To(Succeed())
		encodedBenSecrets, err := json.Marshal(benSecretsList)
		Expect(err).To(Succeed())
		Expect(string(encodedAdamSecrets)).ToNot(ContainSubstring(adamSecretBytes))
		Expect(string(encodedBenSecrets)).ToNot(ContainSubstring(benSecretBytes))
		for _, encoded := range [][]byte{encodedAdamSecrets, encodedBenSecrets} {
			var decoded struct {
				Items []map[string]any `json:"items"`
			}
			Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
			Expect(decoded.Items).ToNot(BeEmpty())
			for _, item := range decoded.Items {
				Expect(item).To(HaveKey("id"))
				Expect(item).To(HaveKey("name"))
				Expect(item).To(HaveKey("type"))
				Expect(item).To(HaveLen(3))
			}
		}

		getSecretResult, err := adamSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "get_resource",
			Arguments: mcpserver.GetResourceInput{
				ResourceType: mcpserver.ResourceTypeSecretReference,
				ID:           adamSecretID,
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getSecretResult.IsError).To(BeTrue())
		var getSecretErrorText string
		for _, content := range getSecretResult.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				getSecretErrorText += textContent.Text
			}
		}
		Expect(getSecretErrorText).To(ContainSubstring(`"category":"invalid_request"`))
		Expect(getSecretErrorText).To(ContainSubstring(`"grpc_code":"InvalidArgument"`))
		getSecretResultJSON, err := json.Marshal(getSecretResult)
		Expect(err).To(Succeed())
		Expect(string(getSecretResultJSON)).ToNot(ContainSubstring(adamSecretBytes))
		Expect(string(getSecretResultJSON)).ToNot(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(adamSecretBytes))))

		adamProject, err := adamProjects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: adamProjectID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(adamProject.GetObject().GetId()).To(Equal(adamProjectID))
		adamSecret, err := publicv1.NewSecretsClient(adamConn).Get(
			ctx, publicv1.SecretsGetRequest_builder{Id: adamSecretID}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(adamSecret.GetObject().GetId()).To(Equal(adamSecretID))
	})
})

func grantMCPDiscoveryTenantAdmin(ctx context.Context, username string) func(context.Context) {
	GinkgoHelper()

	roleStatus, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
	Expect(err).ToNot(HaveOccurred())
	if roleStatus == http.StatusNotFound {
		code, _, createErr := tool.KeycloakAdminRequest(
			ctx, http.MethodPost, "/roles", map[string]any{"name": "tenant-admin"},
		)
		Expect(createErr).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated))
		roleStatus, body, err = tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(roleStatus).To(Equal(http.StatusOK))

	var role map[string]any
	Expect(json.Unmarshal(body, &role)).To(Succeed())
	roleID, ok := role["id"].(string)
	Expect(ok).To(BeTrue())
	Expect(roleID).ToNot(BeEmpty())

	keycloakUserID, err := tool.keycloakEnsureUserReady(ctx, username)
	Expect(err).ToNot(HaveOccurred())
	mappingPath := fmt.Sprintf("/users/%s/role-mappings/realm", keycloakUserID)
	mappingStatus, mappingBody, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, mappingPath, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(mappingStatus).To(Equal(http.StatusOK))

	var mappings []map[string]any
	Expect(json.Unmarshal(mappingBody, &mappings)).To(Succeed())
	for _, mapping := range mappings {
		mappingID, ok := mapping["id"].(string)
		if ok && mappingID == roleID {
			return func(context.Context) {}
		}
	}

	code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, mappingPath, []map[string]any{role})
	Expect(err).ToNot(HaveOccurred())
	Expect(code).To(Equal(http.StatusNoContent))

	var revoked bool
	revoke := func(ctx context.Context) {
		if revoked {
			return
		}
		revoked = true
		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodDelete, mappingPath, []map[string]any{role})
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNoContent))
	}
	DeferCleanup(revoke)
	return revoke
}

func createMCPDiscoveryProject(
	ctx context.Context,
	projects publicv1.ProjectsClient,
	adminProjects privatev1.ProjectsClient,
	adminMemberships privatev1.ProjectMembershipsClient,
	users privatev1.UsersClient,
	caller,
	tenant string,
) string {
	name := fmt.Sprintf("mcp-%s-%s", caller, uuid.New()[24:32])
	userFilter := fmt.Sprintf("this.spec.username == %q", caller)
	var userID string
	EventuallyWithOffset(1, func(g Gomega) {
		userResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{
			Filter: &userFilter,
			Limit:  new(int32(1)),
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(userResponse.GetSize()).To(Equal(int32(1)))
		userID = userResponse.GetItems()[0].GetId()
	}, time.Minute, time.Second).Should(Succeed())

	response, err := projects.Create(ctx, publicv1.ProjectsCreateRequest_builder{
		Object: publicv1.Project_builder{
			Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		}.Build(),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, response.GetObject().GetMetadata().GetCreator()).To(Equal(userID))
	projectID := response.GetObject().GetId()
	_, err = adminProjects.Signal(ctx, privatev1.ProjectsSignalRequest_builder{Id: projectID}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		_, err := adminProjects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: projectID}.Build())
		if status.Code(err) != codes.NotFound {
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
	})
	EventuallyWithOffset(1, func(g Gomega) {
		getResponse, err := adminProjects.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: projectID}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectState_PROJECT_STATE_ACTIVE,
		))
	}, time.Minute, time.Second).Should(Succeed())

	membershipFilter := fmt.Sprintf(
		"this.metadata.tenant == %q && this.metadata.project == %q", tenant, name,
	)
	EventuallyWithOffset(1, func(g Gomega) {
		listResponse, err := adminMemberships.List(ctx, privatev1.ProjectMembershipsListRequest_builder{
			Filter: &membershipFilter,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		var membershipID string
		for _, membership := range listResponse.GetItems() {
			if membership.GetSpec().GetRole() != privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_MANAGER {
				continue
			}
			for _, user := range membership.GetSpec().GetUsers() {
				if user.GetId() == userID {
					membershipID = membership.GetId()
					break
				}
			}
		}
		g.Expect(membershipID).ToNot(BeEmpty())
		getResponse, err := adminMemberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: membershipID,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		))
	}, time.Minute, time.Second).Should(Succeed())

	return projectID
}

func waitForMCPDiscoveryProject(ctx context.Context, projects publicv1.ProjectsClient, projectID string) {
	EventuallyWithOffset(1, func(g Gomega) {
		response, err := projects.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: projectID}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetId()).To(Equal(projectID))
	}, time.Minute, time.Second).Should(Succeed())
}

func createMCPDiscoverySecret(ctx context.Context, conn *grpc.ClientConn, caller string) (string, string) {
	client := publicv1.NewSecretsClient(conn)
	name := fmt.Sprintf("mcp-%s-%s", caller, uuid.New()[24:32])
	secretBytes := fmt.Sprintf("%s-sensitive-reference-bytes", caller)
	response, err := client.Create(ctx, publicv1.SecretsCreateRequest_builder{
		Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
			Data:     map[string][]byte{"value": []byte(secretBytes)},
			Type:     publicv1.SecretType_SECRET_TYPE_VALUE,
		}.Build(),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	secretID := response.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: secretID}.Build())
		if status.Code(err) != codes.NotFound {
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}
	})
	return secretID, secretBytes
}

func connectMCPDiscoveryCaller(ctx context.Context, endpoint, username string) *mcp.ClientSession {
	tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	httpClient := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		token, err := tokenSource.Token(request.Context())
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
		return http.DefaultTransport.RoundTrip(request)
	})}
	client := mcp.NewClient(&mcp.Implementation{
		Name: fmt.Sprintf("it-mcp-%s-discovery", username), Version: "0.1.0",
	}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: httpClient,
	}, nil)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return session
}

func listMCPDiscoveryResource(
	ctx context.Context, session *mcp.ClientSession, resourceType mcpserver.ResourceType, id string,
) (mcpserver.ListResourcesOutput, *mcp.CallToolResult) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_resources",
		Arguments: mcpserver.ListResourcesInput{
			ResourceType: resourceType,
			Filter:       fmt.Sprintf("this.id == %q", id),
			PageSize:     10,
		},
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, result.IsError).To(BeFalse())
	raw, err := json.Marshal(result.StructuredContent)
	ExpectWithOffset(1, err).To(Succeed())
	var output mcpserver.ListResourcesOutput
	ExpectWithOffset(1, json.Unmarshal(raw, &output)).To(Succeed())
	return output, result
}

func resourceIDs(output mcpserver.ListResourcesOutput) []string {
	ids := make([]string, len(output.Items))
	for i, item := range output.Items {
		ids[i] = item.ID
	}
	return ids
}
