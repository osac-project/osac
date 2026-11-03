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
	"create_project",
	"update_project",
}

func connectMCPTokenSource(
	ctx context.Context, endpoint, clientName string, tokenSource auth.TokenSource,
) *mcp.ClientSession {
	GinkgoHelper()
	httpClient := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		token, err := tokenSource.Token(request.Context())
		Expect(err).ToNot(HaveOccurred())
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

func startInProcessMCPServer(ctx context.Context, userAgent string) *httptest.Server {
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
	handler, err := mcpserver.NewHandler(
		mcpserver.NewServerDeps(grpcConn), jwtValidator, "", "",
		mcpserver.TokenExchangerFunc(func(_ context.Context, subjectToken string, _ *jwt.Token) (string, error) {
			return subjectToken, nil
		}),
	)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	return server
}
