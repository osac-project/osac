package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestFulfillmentOAuthTokenSourceAuthenticatesVerifiedGrpcRequest(t *testing.T) {
	const clientID = "operator-test-client"
	const clientSecret = "test-secret-value"
	const accessToken = "test-access-token"

	var issuerURL string
	var tokenRequests atomic.Int32
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/osac/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":         issuerURL,
				"token_endpoint": issuerServerURL(r) + "/token",
			})
		case "/token":
			tokenRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			username, password, ok := r.BasicAuth()
			if !ok || username != clientID || password != clientSecret {
				t.Errorf("token endpoint received unexpected client authentication")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" {
				t.Errorf("token endpoint request was not client_credentials")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": accessToken,
				"token_type":   "Bearer",
				"expires_in":   60,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuerServer.Close()
	issuerURL = issuerServer.URL + "/realms/osac"
	caFile := writeOAuthTestCA(t, issuerServer)

	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		IssuerURL: issuerURL, ClientID: clientID, ClientSecret: clientSecret, CAFile: caFile,
	})
	if err != nil {
		t.Fatalf("construct token source: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCert := issuerServer.TLS.Certificates[0]
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{serverCert}})),
		grpc.UnaryInterceptor(
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				md, ok := metadata.FromIncomingContext(ctx)
				if !ok || strings.Join(md.Get("authorization"), ",") != "Bearer "+accessToken {
					return nil, status.Error(codes.Unauthenticated, "missing expected bearer token")
				}
				return handler(ctx, req)
			},
		),
	)
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	client := &verifiedFulfillmentConn{
		address: listener.Addr().String(), caFile: caFile, tokenSource: tokenSource,
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.reload(probeCtx); err != nil {
		t.Fatalf("create verified fulfillment client: %v", err)
	}
	t.Cleanup(client.close)
	if _, err := healthpb.NewHealthClient(client).Check(probeCtx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("authenticated fulfillment request failed: %v", err)
	}
	legacy, err := createGrpcConn(false, false, tokenSource, listener.Addr().String(), caFile)
	if err != nil {
		t.Fatalf("create authenticated legacy fulfillment client: %v", err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := healthpb.NewHealthClient(legacy).Check(probeCtx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("authenticated legacy fulfillment request failed: %v", err)
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("token endpoint received %d requests, want 1 cached token", got)
	}
}

func TestFulfillmentOAuthTokenSourceRetriesIssuerDiscovery(t *testing.T) {
	var issuerURL string
	var discoveryRequests atomic.Int32
	var tokenRequests atomic.Int32
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/osac/.well-known/openid-configuration":
			if discoveryRequests.Add(1) == 1 {
				http.Error(w, "issuer unavailable", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": issuerURL, "token_endpoint": issuerServerURL(r) + "/token",
			})
		case "/token":
			tokenRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-access-token", "token_type": "Bearer", "expires_in": 60,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuerServer.Close()
	issuerURL = issuerServer.URL + "/realms/osac"
	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		IssuerURL: issuerURL, ClientID: "operator-test-client", ClientSecret: "test-secret-value",
		CAFile: writeOAuthTestCA(t, issuerServer),
	})
	if err != nil {
		t.Fatalf("construct token source: %v", err)
	}
	if _, err := tokenSource.Token(); err == nil {
		t.Fatal("first token request succeeded while issuer discovery was unavailable")
	}
	token, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("token source did not retry issuer discovery: %v", err)
	}
	if token.AccessToken != "test-access-token" || discoveryRequests.Load() != 2 || tokenRequests.Load() != 1 {
		t.Fatalf("unexpected retry result: token=%q discovery=%d token_requests=%d",
			token.AccessToken, discoveryRequests.Load(), tokenRequests.Load())
	}
}

func TestFulfillmentOAuthTokenSourceDoesNotFollowTokenRedirect(t *testing.T) {
	const clientSecret = "redirect-secret"
	var issuerURL string
	var redirectTargetRequests atomic.Int32
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/osac/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": issuerURL, "token_endpoint": issuerServerURL(r) + "/redirect",
			})
		case "/redirect":
			http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
		case "/redirect-target":
			redirectTargetRequests.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuerServer.Close()
	issuerURL = issuerServer.URL + "/realms/osac"
	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		IssuerURL: issuerURL, ClientID: "operator-test-client", ClientSecret: clientSecret,
		CAFile: writeOAuthTestCA(t, issuerServer),
	})
	if err != nil {
		t.Fatalf("construct token source: %v", err)
	}
	if _, err := tokenSource.Token(); err == nil {
		t.Fatal("redirected token request unexpectedly succeeded")
	}
	if got := redirectTargetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
}

func TestFulfillmentOAuthTokenSourceDoesNotExposeTokenEndpointErrorBody(t *testing.T) {
	const clientSecret = "credential-that-must-not-be-logged"
	var issuerURL string
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/osac/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": issuerURL, "token_endpoint": issuerServerURL(r) + "/token",
			})
		case "/token":
			http.Error(w, clientSecret, http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuerServer.Close()
	issuerURL = issuerServer.URL + "/realms/osac"
	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		IssuerURL: issuerURL, ClientID: "operator-test-client", ClientSecret: clientSecret,
		CAFile: writeOAuthTestCA(t, issuerServer),
	})
	if err != nil {
		t.Fatalf("construct token source: %v", err)
	}
	if _, err := tokenSource.Token(); err == nil || strings.Contains(err.Error(), clientSecret) {
		t.Fatalf("token endpoint error leaked credentials or unexpectedly succeeded: %v", err)
	}
}

func TestNewFulfillmentTokenSourceRejectsIncompleteOrInsecureConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  fulfillmentOAuthConfig
	}{
		{name: "missing issuer", cfg: fulfillmentOAuthConfig{ClientID: "client", ClientSecret: "secret"}},
		{
			name: "insecure issuer",
			cfg:  fulfillmentOAuthConfig{IssuerURL: "http://issuer.example/realm", ClientID: "client", ClientSecret: "secret"},
		},
		{
			name: "missing client ID",
			cfg:  fulfillmentOAuthConfig{IssuerURL: "https://issuer.example/realm", ClientSecret: "secret"},
		},
		{
			name: "missing client secret",
			cfg:  fulfillmentOAuthConfig{IssuerURL: "https://issuer.example/realm", ClientID: "client"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newFulfillmentTokenSource(tc.cfg); err == nil {
				t.Fatal("invalid OAuth configuration was accepted")
			}
		})
	}
}

func writeOAuthTestCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	caFile := filepath.Join(t.TempDir(), "oauth-ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, ca, 0600); err != nil {
		t.Fatal(err)
	}
	return caFile
}

func issuerServerURL(r *http.Request) string {
	return "https://" + r.Host
}

func TestVerifiedFulfillmentBundleRotation(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	serverCert := fixture.TLS.Certificates[0]
	root := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate().Raw})
	fixture.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{serverCert}})))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	caFile := filepath.Join(t.TempDir(), "bundle.pem")
	writeBundle := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(caFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeBundle(root)
	client := &verifiedFulfillmentConn{address: listener.Addr().String(), caFile: caFile}
	initialCtx, cancelInitial := context.WithTimeout(context.Background(), 2*time.Second)
	if err := client.reload(initialCtx); err != nil {
		cancelInitial()
		t.Fatalf("valid CA and hostname rejected: %v", err)
	}
	t.Cleanup(client.close)
	first := client.current.Load()
	firstHash := client.observedHash()
	if firstHash == "" {
		cancelInitial()
		t.Fatal("successful probe did not record bundle hash")
	}
	if _, err := healthpb.NewHealthClient(client).Check(initialCtx, &healthpb.HealthCheckRequest{}); err != nil {
		cancelInitial()
		t.Fatalf("verified client cannot call server: %v", err)
	}
	cancelInitial()
	checkLastVerified := func() error {
		checkCtx, cancelCheck := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelCheck()
		_, err := healthpb.NewHealthClient(client).Check(checkCtx, &healthpb.HealthCheckRequest{})
		return err
	}

	wrongRoot := makeTestCA(t)
	wrongAddress := "localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	for _, tc := range []struct {
		name    string
		bundle  []byte
		address string
	}{
		{"malformed", []byte("not PEM"), listener.Addr().String()},
		{"malformed suffix", append(append([]byte{}, root...), []byte("garbage")...), listener.Addr().String()},
		{"wrong root", wrongRoot, listener.Addr().String()},
		{"wrong SAN", append(root, '\n'), wrongAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeBundle(tc.bundle)
			client.address = tc.address
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer probeCancel()
			if err := client.reload(probeCtx); err == nil {
				t.Fatal("unverified replacement accepted")
			}
			if client.current.Load() != first || client.observedHash() != firstHash {
				t.Fatal("failed revision changed the last verified client or observed hash")
			}
			if err := checkLastVerified(); err != nil {
				t.Fatalf("last verified client was lost: %v", err)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if err := os.Remove(caFile); err != nil {
			t.Fatal(err)
		}
		if err := client.reload(context.Background()); err == nil {
			t.Fatal("missing CA file accepted")
		}
		if client.current.Load() != first || client.observedHash() != firstHash {
			t.Fatal("read failure changed the last verified client or observed hash")
		}
	})

	client.address = listener.Addr().String()
	writeBundle(append(append([]byte{}, root...), wrongRoot...))
	replacementCtx, cancelReplacement := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReplacement()
	if err := client.reload(replacementCtx); err != nil {
		t.Fatalf("valid replacement rejected: %v", err)
	}
	if client.current.Load() == first || client.observedHash() == firstHash {
		t.Fatal("verified revision did not atomically replace the client and hash")
	}
}

func TestVerifiedFulfillmentConnUnavailableBeforeFirstHandshake(t *testing.T) {
	client := &verifiedFulfillmentConn{}
	if err := client.Invoke(context.Background(), "/test.Service/Call", nil, nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("Invoke before a verified connection returned %v, want Unavailable", err)
	}
	_, err := client.NewStream(context.Background(), &grpc.StreamDesc{}, "/test.Service/Stream")
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("NewStream before a verified connection returned %v, want Unavailable", err)
	}
}

func TestVerifiedFulfillmentConnValidatesConfigurationWithoutRequiringService(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(caFile, makeTestCA(t), 0600); err != nil {
		t.Fatal(err)
	}
	client := &verifiedFulfillmentConn{address: "127.0.0.1:1", caFile: caFile}
	if err := client.validateCAFile(); err != nil {
		t.Fatalf("valid local CA configuration was rejected before dialing: %v", err)
	}
}

func TestLoadFulfillmentCAPool(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(caFile, makeTestCA(t), 0600); err != nil {
		t.Fatal(err)
	}
	pool, err := loadFulfillmentCAPool(caFile)
	if err != nil || pool == nil {
		t.Fatalf("valid fulfillment CA bundle was rejected: %v", err)
	}

	if err := os.WriteFile(caFile, []byte("not PEM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFulfillmentCAPool(caFile); err == nil {
		t.Fatal("invalid fulfillment CA bundle was accepted")
	}
}

func TestVerifiedCAPoolAllowsUnspecifiedKeyUsage(t *testing.T) {
	if _, err := verifiedCAPool(makeTestCAWithKeyUsage(t, 0)); err != nil {
		t.Fatalf("CA without an explicit key usage was rejected: %v", err)
	}
}

func TestVerifiedCAPoolRejectsExplicitKeyUsageWithoutCertSign(t *testing.T) {
	if _, err := verifiedCAPool(makeTestCAWithKeyUsage(t, x509.KeyUsageDigitalSignature)); err == nil {
		t.Fatal("CA with an explicit key usage lacking CertSign was accepted")
	}
}

func makeTestCA(t *testing.T) []byte {
	return makeTestCAWithKeyUsage(t, x509.KeyUsageCertSign)
}

func makeTestCAWithKeyUsage(t *testing.T, keyUsage x509.KeyUsage) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "wrong root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: keyUsage, BasicConstraintsValid: true, IsCA: true,
	}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}
