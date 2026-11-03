package it

import (
	"context"
	"net/http/httptest"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
)

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
