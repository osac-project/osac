package it

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var expectedMCPServerToolNames = []string{
	"check_connection",
	"list_resources",
	"get_resource",
	"create_compute_instance",
	"update_compute_instance",
	"set_compute_instance_power",
	"restart_compute_instance",
	"delete_compute_instance",
	"create_network_resource",
	"expose_network_resource",
	"delete_network_resource",
	"create_project",
	"update_project",
	"create_catalog_offering",
	"update_catalog_offering",
	"publish_catalog_offering",
	"delete_catalog_offering",
}

func connectMCPTokenSource(
	ctx context.Context, endpoint, clientName string, tokenSource auth.TokenSource,
) *mcp.ClientSession {
	GinkgoHelper()
	httpClient := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		token, err := tokenSource.Token(request.Context())
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
		return http.DefaultTransport.RoundTrip(request)
	})}
	client := mcp.NewClient(&mcp.Implementation{
		Name: fmt.Sprintf("it-mcp-%s", clientName), Version: "0.1.0",
	}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: httpClient,
	}, nil)
	Expect(err).ToNot(HaveOccurred())
	return session
}

func mcpToolNames(tools []*mcp.Tool) []string {
	names := make([]string, len(tools))
	for index, registeredTool := range tools {
		names[index] = registeredTool.Name
	}
	return names
}

func assertMCPResourceDiscoverable(
	ctx context.Context, session *mcp.ClientSession, resourceType mcpserver.ResourceType, id string,
) map[string]any {
	GinkgoHelper()
	listOutput, err := callMCPTool[mcpserver.ListResourcesOutput](
		ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: resourceType,
			Filter:       fmt.Sprintf("this.id == %q", id),
		},
	)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, listOutput.Items).To(HaveLen(1))
	ExpectWithOffset(1, listOutput.Items[0].ID).To(Equal(id))

	resource, err := callMCPTool[mcpserver.GetResourceOutput](
		ctx, session, "get_resource", mcpserver.GetResourceInput{
			ResourceType: resourceType,
			ID:           id,
		},
	)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, resource.Resource).To(HaveKeyWithValue("id", id))
	ExpectWithOffset(1, resource.Resource).To(HaveKey("name"))
	ExpectWithOffset(1, resource.Resource).To(HaveKey("version"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("title"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("description"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("annotations"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("status"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("conditions"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("specDefaults"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("spec"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("fields"))
	ExpectWithOffset(1, resource.Resource).ToNot(HaveKey("userData"))
	return resource.Resource
}

func createMCPVirtualNetworkReplacement(
	ctx context.Context,
	session *mcp.ClientSession,
	virtualNetworks privatev1.VirtualNetworksClient,
	virtualNetworkID string,
	name string,
	ipv4CIDR string,
) string {
	GinkgoHelper()
	replacement, err := callMCPTool[mcpserver.CreateNetworkResourceOutput](
		ctx, session, "create_network_resource", mcpserver.CreateNetworkResourceInput{
			ResourceType: mcpserver.NetworkResourceTypeVirtualNetwork,
			Name:         name,
			IPv4Cidr:     ipv4CIDR,
		},
	)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	replacementID := replacement.ID
	DeferCleanup(func(cleanupCtx context.Context) {
		if replacementID != "" {
			ExpectWithOffset(1, cleanupVirtualNetworkFixture(
				cleanupCtx, virtualNetworks, replacementID,
			)).To(Succeed())
		}
	})
	ExpectWithOffset(1, replacementID).ToNot(BeEmpty())
	ExpectWithOffset(1, replacementID).ToNot(Equal(virtualNetworkID))
	return replacementID
}

func startInProcessMCPServer(ctx context.Context, userAgent string) *httptest.Server {
	return startInProcessMCPServerWithDeps(ctx, userAgent, nil)
}

func startInProcessMCPServerWithDeps(
	ctx context.Context,
	userAgent string,
	configureDeps func(mcpserver.ServerDeps) mcpserver.ServerDeps,
) *httptest.Server {
	jwksCache, err := auth.NewJwksCache().
		SetLogger(logger).
		SetCaPool(tool.CaPool()).
		AddIssuers(keycloakIssuer(ctx)).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	jwtValidator, err := auth.NewJwtValidator().
		SetLogger(logger).
		SetJwksCache(jwksCache).
		SetExpirationLeeway(5 * time.Second).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	grpcConn, err := network.NewGrpcClient().
		SetLogger(logger).
		SetCaPool(tool.CaPool()).
		SetAddress(externalServiceAddr).
		SetUserAgent(userAgent).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	DeferCleanup(func() { ExpectWithOffset(1, grpcConn.Close()).To(Succeed()) })
	deps := mcpserver.NewServerDeps(grpcConn)
	if configureDeps != nil {
		deps = configureDeps(deps)
	}
	handler, err := mcpserver.NewHandler(
		deps, jwtValidator, "", "",
		mcpserver.TokenExchangerFunc(func(_ context.Context, subjectToken string, _ *jwt.Token) (string, error) {
			return subjectToken, nil
		}),
	)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	return server
}

func connectMCPCaller(ctx context.Context, endpoint, clientName, username string) *mcp.ClientSession {
	tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
	ExpectWithOffset(2, err).ToNot(HaveOccurred())
	return connectMCPCallerWithTokenSource(ctx, endpoint, clientName, tokenSource)
}

func connectMCPCallerWithTokenSource(
	ctx context.Context, endpoint, clientName string, tokenSource auth.TokenSource,
) *mcp.ClientSession {
	httpClient := &http.Client{Transport: ghttp.RoundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		token, err := tokenSource.Token(request.Context())
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
		return http.DefaultTransport.RoundTrip(request)
	})}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: httpClient,
	}, nil)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	DeferCleanup(func() { ExpectWithOffset(1, session.Close()).To(Succeed()) })
	return session
}

func expectMCPSafeError(err error, category, grpcCode string, fragments ...string) {
	GinkgoHelper()
	ExpectWithOffset(1, err).To(HaveOccurred())
	errorText := err.Error()
	ExpectWithOffset(1, errorText).To(ContainSubstring(fmt.Sprintf(`"category":"%s"`, category)))
	ExpectWithOffset(1, errorText).To(ContainSubstring(fmt.Sprintf(`"grpc_code":"%s"`, grpcCode)))
	ExpectWithOffset(1, errorText).To(ContainSubstring(`"retryable":false`))
	for _, fragment := range fragments {
		ExpectWithOffset(1, errorText).To(ContainSubstring(fragment))
	}
}
