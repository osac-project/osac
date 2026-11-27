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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcoauth "google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/tlsconfig"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func machineTokenConfig(fixture keycloakFixture, c clientContract, secret string) clientcredentials.Config {
	return clientcredentials.Config{
		ClientID: c.clientID, ClientSecret: secret, Scopes: c.requestedScopes,
		TokenURL: fixture.issuer + "/protocol/openid-connect/token",
		// Controller/CLI and UI consumers post their credentials in the form.
		// The original CSI case retains the driver's default Config auth style.
		AuthStyle: oauth2.AuthStyleInParams,
	}
}

func verifyClientClaims(source, issuer string, c clientContract, token *jwt.Token) {
	GinkgoHelper()
	claims, ok := token.Claims.(jwt.MapClaims)
	Expect(ok && token.Valid).To(BeTrue(), "only a production-validated JWT may enter the runtime contract")
	Expect(claimMismatches(claims, issuer, c)).To(BeEmpty(), "source-defined claim contract mismatch (values withheld)")
	identity, err := auth.ExtractAuthContext(token)
	if err != nil {
		Fail("production identity extraction failed (details withheld)")
	}
	principalMatches := identity.Username == "service-account-"+c.clientID && identity.AuthMethod == "jwt"
	Expect(principalMatches).To(BeTrue(), "production extraction must retain the expected service-account principal")
	// Emit reviewed constants only after checking equality. Unknown claims,
	// bearer strings and response bodies never reach diagnostics.
	proof, err := json.Marshal(map[string]any{
		"artifact": source, "client": c.clientID, "source_contract_verified": true,
		"issuer_matches_fixture": true, "azp": c.clientID, "sub": c.subject,
		"principal": "service-account-" + c.clientID, "requested_scopes": c.requestedScopes,
		"audiences": c.audiences, "scopes": c.scopes, "realm_roles": c.realmRoles, "client_roles": c.clientRoles,
		"privilege_drift": false, "credentials_and_bearer": "withheld",
	})
	Expect(err).ToNot(HaveOccurred())
	_, err = fmt.Fprintf(GinkgoWriter, "matrix_claims=%s\n", proof)
	Expect(err).ToNot(HaveOccurred())
}

func rejectMachineSecret(ctx context.Context, source string, fixture keycloakFixture, clientID, otherClient string,
	httpClient *http.Client, knownGap bool) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	secret := fixture.secrets.read(ctx, otherClient)
	if !knownGap {
		distinct := secret != fixture.secrets.read(ctx, clientID)
		Expect(distinct).To(BeTrue(), "negative credential must be the other client's distinct real lookup value")
	}
	config := machineTokenConfig(fixture, contractFor(clientID), secret)
	token, tokenErr := config.Token(context.WithValue(ctx, oauth2.HTTPClient, httpClient))
	issued := tokenErr == nil && token != nil && token.AccessToken != ""
	var retrieval *oauth2.RetrieveError
	httpStatus, errorCode := 0, "unclassified"
	if errors.As(tokenErr, &retrieval) && retrieval.Response != nil {
		httpStatus = retrieval.Response.StatusCode
		switch retrieval.ErrorCode {
		case "invalid_client", "unauthorized_client":
			errorCode = retrieval.ErrorCode
		}
	}
	rejected := httpStatus == http.StatusUnauthorized && errorCode == "unauthorized_client"
	_, err := fmt.Fprintf(GinkgoWriter,
		"matrix_rejection artifact=%s client=%s lookup_client=%s known_ui_gap=%t "+
			"token_issued=%t oauth_status=%d oauth_error=%s credentials_and_body=withheld\n",
		source, clientID, otherClient, knownGap, issued, httpStatus, errorCode)
	Expect(err).ToNot(HaveOccurred())
	Expect(issued).To(BeFalse(), "credential rejection must not issue a bearer")
	Expect(rejected).To(BeTrue(), "Keycloak must reject the lookup value with HTTP 401 / unauthorized_client")
}

// Called only from the existing per-artifact Ordered fixture; this function
// never starts Keycloak, modifies the import or repairs any client.
func exerciseMachineClient(ctx context.Context, source string, fixture keycloakFixture, clientID string,
	logger *slog.Logger, caPool *trust.CertPool, tlsConfig *tls.Config, httpClient *http.Client) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	c := contractFor(clientID)
	config := machineTokenConfig(fixture, c, fixture.secrets.read(ctx, clientID))
	token, tokenErr := config.Token(context.WithValue(ctx, oauth2.HTTPClient, httpClient))
	if tokenErr != nil || token == nil || token.AccessToken == "" {
		Fail("machine-client issuance must succeed (credentials and response withheld)")
	}
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
	var reflectionCalls atomic.Int32
	server := itesting.NewServer(
		grpc.Creds(credentials.NewTLS(tlsconfig.NewServerTLSConfig(itesting.LocalhostCertificate(), nil))),
		grpc.ChainUnaryInterceptor(authn.UnaryServer, authz.UnaryServer),
		grpc.ChainStreamInterceptor(authn.StreamServer, authz.StreamServer,
			func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				reflectionCalls.Add(1) // Counts streams reaching the real reflection backend after both interceptors.
				return handler(srv, stream)
			}),
	)
	DeferCleanup(server.Stop)
	volumes, privateClusters := &volumeHandler{}, &clusterHandler{}
	publicClusters, users := &publicClusterHandler{}, &publicUserHandler{}
	privatev1.RegisterVolumesServer(server.Registrar(), volumes)
	privatev1.RegisterClustersServer(server.Registrar(), privateClusters)
	publicv1.RegisterClustersServer(server.Registrar(), publicClusters)
	publicv1.RegisterUsersServer(server.Registrar(), users)
	reflectionServer, ok := server.Registrar().(reflection.GRPCServer)
	Expect(ok).To(BeTrue())
	reflection.RegisterV1(reflectionServer)
	server.Start()
	conn, err := grpc.NewClient(server.Address(),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(grpcoauth.TokenSource{TokenSource: oauth2.StaticTokenSource(token)}),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(conn.Close)

	_, volumeErr := privatev1.NewVolumesClient(conn).List(ctx, &privatev1.VolumesListRequest{})
	_, privateClusterErr := privatev1.NewClustersClient(conn).List(ctx, &privatev1.ClustersListRequest{})
	_, publicClusterErr := publicv1.NewClustersClient(conn).List(ctx, &publicv1.ClustersListRequest{})
	_, userErr := publicv1.NewUsersClient(conn).Create(ctx, &publicv1.UsersCreateRequest{})
	// These are method authorization probes, not DB-backed business operations.
	// Both named admin accounts have all-method grants; no fake forbidden
	// business method is invented for them. UI has the generic public list grant.
	privateStatus, userStatus, privateCalls, userCalls := codes.OK, codes.OK, int32(1), int32(1)
	if clientID == "osac-ui-backend" {
		privateStatus, userStatus, privateCalls, userCalls = codes.PermissionDenied, codes.PermissionDenied, 0, 0
	}
	for _, probe := range []struct {
		method   string
		err      error
		calls    int32
		expected codes.Code
		count    int32
	}{
		{"private.Volumes/List", volumeErr, volumes.calls.Load(), privateStatus, privateCalls},
		{"private.Clusters/List", privateClusterErr, privateClusters.calls.Load(), privateStatus, privateCalls},
		{"public.Clusters/List", publicClusterErr, publicClusters.calls.Load(), codes.OK, 1},
		{"public.Users/Create", userErr, users.calls.Load(), userStatus, userCalls},
	} {
		_, err = fmt.Fprintf(GinkgoWriter, "matrix_rpc artifact=%s client=%s method=%s status=%s handler_calls=%d\n",
			source, clientID, probe.method, status.Code(probe.err), probe.calls)
		Expect(err).ToNot(HaveOccurred())
		Expect(status.Code(probe.err)).To(Equal(probe.expected), probe.method)
		Expect(probe.calls).To(Equal(probe.count), probe.method)
	}
	validated, err := validator.Validate(ctx, token.AccessToken)
	if err != nil {
		Fail("issued token must pass production JWKS/JWT verification (details withheld)")
	}
	verifyClientClaims(source, fixture.issuer, c, validated)
	if clientID == "osac-ui-backend" {
		exerciseUIReflection(ctx, source, server.Address(), tlsConfig, token.AccessToken, &reflectionCalls)
	}
}

func exerciseUIReflection(ctx context.Context, source, address string, tlsConfig *tls.Config, bearer string,
	calls *atomic.Int32) {
	GinkgoHelper()
	// The actual UI proxy puts its real token on reflection stream metadata,
	// not on the generic connection. Reuse this connection without metadata for
	// the unauthenticated control, which must not reach reflection.
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(conn.Close)
	authenticated := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+bearer)
	code, listed, described := reflectionProbe(authenticated, conn)
	Expect(code).To(Equal(codes.OK))
	Expect(listed && described).To(BeTrue(), "real v1 reflection must list and describe the generated public Clusters service")
	Expect(calls.Load()).To(Equal(int32(1)))
	_, err = fmt.Fprintf(GinkgoWriter,
		"matrix_stream artifact=%s client=osac-ui-backend method=grpc.reflection.v1.ServerReflection/ServerReflectionInfo "+
			"status=%s listed=true descriptor=true handler_calls=%d\n", source, code, calls.Load())
	Expect(err).ToNot(HaveOccurred())
	code, _, _ = reflectionProbe(ctx, conn)
	Expect(code).To(Equal(codes.Unauthenticated))
	Expect(calls.Load()).To(Equal(int32(1)), "anonymous reflection must not reach its handler")
	_, err = fmt.Fprintf(GinkgoWriter, "matrix_stream artifact=%s anonymous=true status=%s additional_handler_calls=0\n", source, code)
	Expect(err).ToNot(HaveOccurred())
}

func reflectionProbe(ctx context.Context, conn *grpc.ClientConn) (codes.Code, bool, bool) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return status.Code(err), false, false
	}
	defer func() {
		err := stream.CloseSend()
		if err != nil && !errors.Is(err, io.EOF) {
			Fail("closing reflection send stream failed (details withheld)")
		}
	}()
	err = stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return status.Code(err), false, false
	}
	response, err := stream.Recv()
	if err != nil {
		return status.Code(err), false, false
	}
	var names []string
	for _, service := range response.GetListServicesResponse().GetService() {
		names = append(names, service.GetName())
	}
	listed := slices.Contains(names, "osac.public.v1.Clusters")
	err = stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: "osac.public.v1.Clusters"},
	})
	if err != nil {
		return status.Code(err), listed, false
	}
	response, err = stream.Recv()
	if err != nil {
		return status.Code(err), listed, false
	}
	described := false
	for _, encoded := range response.GetFileDescriptorResponse().GetFileDescriptorProto() {
		file := &descriptorpb.FileDescriptorProto{}
		if proto.Unmarshal(encoded, file) != nil {
			Fail("reflection descriptor must decode (response withheld)")
		}
		for _, service := range file.GetService() {
			described = described || (file.GetPackage() == "osac.public.v1" && service.GetName() == "Clusters")
		}
	}
	return codes.OK, listed, described
}

// Empty authorization-independent handlers. These never filter by identity or
// simulate persistence, tenant filtering, business validation or provider calls.
type publicClusterHandler struct {
	publicv1.UnimplementedClustersServer
	calls atomic.Int32
}

func (h *publicClusterHandler) List(context.Context, *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	h.calls.Add(1)
	return &publicv1.ClustersListResponse{}, nil
}

type publicUserHandler struct {
	publicv1.UnimplementedUsersServer
	calls atomic.Int32
}

func (h *publicUserHandler) Create(context.Context, *publicv1.UsersCreateRequest) (*publicv1.UsersCreateResponse, error) {
	h.calls.Add(1)
	return &publicv1.UsersCreateResponse{}, nil
}
