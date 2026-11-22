/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcoauth "google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/tlsconfig"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestRealmAuthorization(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shipped CSI realm authorization")
}

var _ = Describe("Client credentials from a shipped realm", func() {
	for _, source := range []string{"chart", "prerequisites"} {
		Context(source, Ordered, ContinueOnFailure, func() {
			var (
				fixture    keycloakFixture
				logger     *slog.Logger
				caPool     *trust.CertPool
				tlsConfig  *tls.Config
				httpClient *http.Client
			)

			// One fresh import per artifact. Cases only read the imported realm;
			// each owns its tokens, RPC connection, server and handler counters.
			// SpecTimeout includes BeforeAll, including when a single case is selected.
			BeforeAll(func(ctx SpecContext) {
				var err error
				logger, err = logging.NewLogger().SetWriter(GinkgoWriter).Build()
				Expect(err).ToNot(HaveOccurred())
				fixture = startKeycloak(ctx, source)
				caPool, err = trust.NewCertPool().SetLogger(logger).AddFile(fixture.certFile).Build()
				Expect(err).ToNot(HaveOccurred())
				tlsConfig = tlsconfig.NewClientTLSConfig()
				tlsConfig.RootCAs = caPool.Pool()
				transport := &http.Transport{TLSClientConfig: tlsConfig}
				DeferCleanup(transport.CloseIdleConnections)
				httpClient = &http.Client{Transport: transport, Timeout: 10 * time.Second}
				Eventually(func(g Gomega) {
					request, err := http.NewRequestWithContext(ctx, http.MethodGet,
						fixture.issuer+"/.well-known/openid-configuration", nil)
					g.Expect(err).ToNot(HaveOccurred())
					response, err := httpClient.Do(request)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(response.Body.Close()).To(Succeed())
					g.Expect(response.StatusCode).To(Equal(http.StatusOK))
				}, 3*time.Minute, time.Second).Should(Succeed(), "Keycloak must import the shipped realm")
			}, NodeTimeout(5*time.Minute))

			It("authorizes CSI private Volumes/List and denies Clusters/List", func(ctx SpecContext) {
				// Match the driver's request: no added roles or optional scopes.
				config := clientcredentials.Config{
					ClientID:     "osac-csi-driver",
					ClientSecret: fixture.secrets.clients["osac-csi-driver"],
					TokenURL:     fixture.issuer + "/protocol/openid-connect/token",
				}
				tokenSource := config.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, httpClient))
				token, err := tokenSource.Token()
				// OAuth errors can carry response bodies. Never print them or the bearer token.
				if err != nil {
					Fail("client_credentials issuance must succeed (response withheld)")
				}
				Expect(token.AccessToken).ToNot(BeEmpty(), "Keycloak must issue an access token")

				jwks, err := auth.NewJwksCache().SetLogger(logger).SetCaPool(caPool).AddIssuer(fixture.issuer).Build()
				Expect(err).ToNot(HaveOccurred())
				validator, err := auth.NewJwtValidator().SetLogger(logger).SetJwksCache(jwks).Build()
				Expect(err).ToNot(HaveOccurred())
				authn, err := auth.NewGrpcAuthnInterceptor().SetLogger(logger).SetJwtValidator(validator).Build()
				Expect(err).ToNot(HaveOccurred())
				evaluator, err := auth.NewEvaluator().Build()
				Expect(err).ToNot(HaveOccurred())
				authz, err := auth.NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).Build()
				Expect(err).ToNot(HaveOccurred())

				server := itesting.NewServer(
					grpc.Creds(credentials.NewTLS(tlsconfig.NewServerTLSConfig(itesting.LocalhostCertificate(), nil))),
					grpc.ChainUnaryInterceptor(authn.UnaryServer, authz.UnaryServer),
				)
				DeferCleanup(server.Stop)
				volumes := &volumeHandler{}
				clusters := &clusterHandler{}
				privatev1.RegisterVolumesServer(server.Registrar(), volumes)
				privatev1.RegisterClustersServer(server.Registrar(), clusters)
				server.Start()
				conn, err := grpc.NewClient(server.Address(),
					grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
					grpc.WithPerRPCCredentials(grpcoauth.TokenSource{TokenSource: tokenSource}),
				)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(conn.Close)

				// Exercise both generated RPCs BEFORE asserting the expected role or success.
				// This records an actual authorization failure even when the role is absent.
				rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				_, listErr := privatev1.NewVolumesClient(conn).List(rpcCtx, &privatev1.VolumesListRequest{})
				_, unrelatedErr := privatev1.NewClustersClient(conn).List(rpcCtx, &privatev1.ClustersListRequest{})
				validated, err := validator.Validate(ctx, token.AccessToken)
				Expect(err).ToNot(HaveOccurred(), "the issued token must pass production JWT verification")
				identity, err := auth.ExtractAuthContext(validated)
				Expect(err).ToNot(HaveOccurred())
				roles, _ := identity.RealmAccess["roles"].([]any)
				hasRole := false
				for _, role := range roles {
					if role == "osac-csi-driver" {
						hasRole = true
					}
				}
				_, err = fmt.Fprintf(GinkgoWriter,
					"realm=%s token_issued=true jwt_verified=true csi_role=%t fullScopeAllowed=%t "+
						"Volumes/List=%s Clusters/List=%s volume_handler_calls=%d cluster_handler_calls=%d\n",
					source, hasRole, fixture.fullScopeAllowed, status.Code(listErr), status.Code(unrelatedErr),
					volumes.calls.Load(), clusters.calls.Load())
				Expect(err).ToNot(HaveOccurred())

				Expect(fixture.fullScopeAllowed).To(BeFalse(), "CSI must retain explicit role scope")
				Expect(status.Code(unrelatedErr)).To(Equal(codes.PermissionDenied))
				Expect(clusters.calls.Load()).To(BeZero(), "unrelated private RPC must not reach its handler")
				Expect(listErr).ToNot(HaveOccurred(), "the bundled CSI identity must be allowed to list private volumes")
				Expect(volumes.calls.Load()).To(Equal(int32(1)))
				Expect(hasRole).To(BeTrue(), "the issued token must contain the osac-csi-driver realm role")
			}, SpecTimeout(5*time.Minute))

			It("rejects the controller lookup value as a CSI client secret", func(ctx SpecContext) {
				// Read the other client's actual lookup value, independently of the resolved realm.
				config := clientcredentials.Config{
					ClientID:     "osac-csi-driver",
					ClientSecret: fixture.secrets.read(ctx, "osac-controller"),
					TokenURL:     fixture.issuer + "/protocol/openid-connect/token",
				}
				token, err := config.Token(context.WithValue(ctx, oauth2.HTTPClient, httpClient))
				issued := err == nil && token != nil && token.AccessToken != ""
				var retrieval *oauth2.RetrieveError
				httpStatus, errorCode := 0, "unclassified"
				if errors.As(err, &retrieval) && retrieval.Response != nil {
					httpStatus = retrieval.Response.StatusCode
					// Only log recognized constant error codes, never arbitrary response content.
					switch retrieval.ErrorCode {
					case "invalid_client", "unauthorized_client":
						errorCode = retrieval.ErrorCode
					}
				}
				credentialRejected := httpStatus == http.StatusUnauthorized && errorCode == "unauthorized_client"
				_, err = fmt.Fprintf(GinkgoWriter,
					"realm=%s wrong_client_secret_issued=%t credential_rejected=%t oauth_status=%d credential_error=%s\n",
					source, issued, credentialRejected, httpStatus, errorCode)
				Expect(err).ToNot(HaveOccurred())
				Expect(issued).To(BeFalse(), "the controller lookup value must not authenticate the CSI client")
				Expect(credentialRejected).To(BeTrue(), "Keycloak must reject the wrong client secret with HTTP 401 / unauthorized_client")
			}, SpecTimeout(5*time.Minute))
		})
	}
})

// These handlers only make successful authorization observable. Persistence,
// tenant filtering and storage provisioning are outside this contract test.
type volumeHandler struct {
	privatev1.UnimplementedVolumesServer
	calls atomic.Int32
}

func (h *volumeHandler) List(context.Context, *privatev1.VolumesListRequest) (*privatev1.VolumesListResponse, error) {
	h.calls.Add(1)
	return &privatev1.VolumesListResponse{}, nil
}

type clusterHandler struct {
	privatev1.UnimplementedClustersServer
	calls atomic.Int32
}

func (h *clusterHandler) List(context.Context, *privatev1.ClustersListRequest) (*privatev1.ClustersListResponse, error) {
	h.calls.Add(1)
	return &privatev1.ClustersListResponse{}, nil
}
