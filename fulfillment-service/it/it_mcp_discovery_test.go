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
		provisionConn, err := tool.MakeUserConnection(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		_, err = publicv1.NewProjectsClient(provisionConn).List(ctx, publicv1.ProjectsListRequest_builder{
			Limit: new(int32(1)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(provisionConn.Close()).To(Succeed())

		adminConn := tool.InternalView().AdminConn()
		adminProjects := privatev1.NewProjectsClient(adminConn)
		adminMemberships := privatev1.NewProjectMembershipsClient(adminConn)
		adminUsers := privatev1.NewUsersClient(adminConn)
		adamProjectID := createMCPDiscoveryProject(
			ctx, adminProjects, adminMemberships, adminUsers, "adam", "engineering",
		)
		benProjectID := createMCPDiscoveryProject(
			ctx, adminProjects, adminMemberships, adminUsers, "ben", "development",
		)

		adamConn, err := tool.MakeUserConnection(ctx, "adam", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(adamConn.Close()).To(Succeed()) })
		benConn, err := tool.MakeUserConnection(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(benConn.Close()).To(Succeed()) })

		adamProjects := publicv1.NewProjectsClient(adamConn)
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

		adamProjectsList := listMCPDiscoveryResource(ctx, adamSession, mcpserver.ResourceTypeProject, adamProjectID)
		Expect(resourceIDs(adamProjectsList)).To(ContainElement(adamProjectID))
		Expect(resourceIDs(adamProjectsList)).ToNot(ContainElement(benProjectID))
		benProjectsList := listMCPDiscoveryResource(ctx, benSession, mcpserver.ResourceTypeProject, benProjectID)
		Expect(resourceIDs(benProjectsList)).To(ContainElement(benProjectID))
		Expect(resourceIDs(benProjectsList)).ToNot(ContainElement(adamProjectID))

		adamSecretsList := listMCPDiscoveryResource(ctx, adamSession, mcpserver.ResourceTypeSecretReference, adamSecretID)
		Expect(resourceIDs(adamSecretsList)).To(ContainElement(adamSecretID))
		Expect(resourceIDs(adamSecretsList)).ToNot(ContainElement(benSecretID))
		benSecretsList := listMCPDiscoveryResource(ctx, benSession, mcpserver.ResourceTypeSecretReference, benSecretID)
		Expect(resourceIDs(benSecretsList)).To(ContainElement(benSecretID))
		Expect(resourceIDs(benSecretsList)).ToNot(ContainElement(adamSecretID))

		encodedAdamSecrets, err := json.Marshal(adamSecretsList)
		Expect(err).To(Succeed())
		encodedBenSecrets, err := json.Marshal(benSecretsList)
		Expect(err).To(Succeed())
		Expect(string(encodedAdamSecrets)).ToNot(ContainSubstring(adamSecretBytes))
		Expect(string(encodedBenSecrets)).ToNot(ContainSubstring(benSecretBytes))
		Expect(string(encodedAdamSecrets)).ToNot(ContainSubstring("data"))
		Expect(string(encodedBenSecrets)).ToNot(ContainSubstring("data"))

		_, err = callMCPTool[struct{}](ctx, adamSession, "get_resource", mcpserver.GetResourceInput{
			ResourceType: mcpserver.ResourceTypeSecretReference,
			ID:           adamSecretID,
		})
		Expect(err).To(MatchError(ContainSubstring(`"category":"invalid_request"`)))
		Expect(err).To(MatchError(ContainSubstring(`"grpc_code":"InvalidArgument"`)))

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

func createMCPDiscoveryProject(
	ctx context.Context,
	projects privatev1.ProjectsClient,
	memberships privatev1.ProjectMembershipsClient,
	users privatev1.UsersClient,
	caller,
	tenant string,
) string {
	name := fmt.Sprintf("mcp-%s-%s", caller, uuid.New()[24:32])
	userFilter := fmt.Sprintf("this.spec.username == %q", caller)
	userResponse, err := users.List(ctx, privatev1.UsersListRequest_builder{
		Filter: &userFilter,
		Limit:  new(int32(1)),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, userResponse.GetSize()).To(Equal(int32(1)))
	userID := userResponse.GetItems()[0].GetId()

	response, err := projects.Create(ctx, privatev1.ProjectsCreateRequest_builder{
		Object: privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		}.Build(),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	projectID := response.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := projects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: projectID}.Build())
		if err != nil {
			ExpectWithOffset(1, err).To(HaveOccurred())
		}
	})
	EventuallyWithOffset(1, func(g Gomega) {
		getResponse, err := projects.Get(ctx, privatev1.ProjectsGetRequest_builder{Id: projectID}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectState_PROJECT_STATE_ACTIVE,
		))
	}, time.Minute, time.Second).Should(Succeed())

	membershipResponse, err := memberships.Create(ctx, privatev1.ProjectMembershipsCreateRequest_builder{
		Object: privatev1.ProjectMembership_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    fmt.Sprintf("%s-membership", name),
				Project: name,
				Tenant:  tenant,
			}.Build(),
			Spec: privatev1.ProjectMembershipSpec_builder{
				Role: privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
				Users: []*privatev1.UserReference{{
					Id: userID,
				}},
			}.Build(),
		}.Build(),
	}.Build())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	membershipID := membershipResponse.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		_, err := memberships.Delete(ctx, privatev1.ProjectMembershipsDeleteRequest_builder{
			Id: membershipID,
		}.Build())
		if err != nil {
			ExpectWithOffset(1, err).To(HaveOccurred())
		}
	})
	EventuallyWithOffset(1, func(g Gomega) {
		getResponse, err := memberships.Get(ctx, privatev1.ProjectMembershipsGetRequest_builder{
			Id: membershipID,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ProjectMembershipState_PROJECT_MEMBERSHIP_STATE_READY,
		))
	}, time.Minute, time.Second).Should(Succeed())
	return projectID
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
		if err != nil {
			ExpectWithOffset(1, err).To(HaveOccurred())
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
) mcpserver.ListResourcesOutput {
	output, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
		ResourceType: resourceType,
		Filter:       fmt.Sprintf("this.id == %q", id),
		PageSize:     10,
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return output
}

func resourceIDs(output mcpserver.ListResourcesOutput) []string {
	ids := make([]string, len(output.Items))
	for i, item := range output.Items {
		ids[i] = item.ID
	}
	return ids
}
