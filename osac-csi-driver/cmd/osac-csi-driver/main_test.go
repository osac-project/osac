package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const testClientSecretRequestBody = "client_secret=placeholder"

func TestValidateFulfillmentFlags(t *testing.T) {
	t.Run("all empty is valid", func(t *testing.T) {
		if err := validateFulfillmentFlags("", "", false, "", "", "", true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("all empty without stub permission returns error", func(t *testing.T) {
		if err := validateFulfillmentFlags("", "", false, "", "", "", false); err == nil {
			t.Fatal("expected error when stub mode is not allowed")
		}
	})

	t.Run("all set is valid", func(t *testing.T) {
		err := validateFulfillmentFlags("ep", "", false, "id", "/path", "https://issuer", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("only client-id set returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("", "", false, "id", "", "", true)
		if err == nil {
			t.Fatal("expected error when only client-id is set")
		}
	})

	t.Run("only secret-file set returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("", "", false, "", "/path", "", true)
		if err == nil {
			t.Fatal("expected error when only secret-file is set")
		}
	})

	t.Run("only issuer-url set returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("", "", false, "", "", "https://issuer", true)
		if err == nil {
			t.Fatal("expected error when only issuer-url is set")
		}
	})

	t.Run("missing issuer-url returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("", "", false, "id", "/path", "", true)
		if err == nil {
			t.Fatal("expected error when issuer-url is missing")
		}
	})

	t.Run("endpoint without credentials returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("fulfillment.svc:8000", "", false, "", "", "", true)
		if err == nil {
			t.Fatal("expected error when endpoint is set without credentials")
		}
	})

	t.Run("endpoint with credentials is valid", func(t *testing.T) {
		err := validateFulfillmentFlags(
			"fulfillment.svc:8000", "", false, "id", "/path", "https://issuer",
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("credentials without endpoint returns error", func(t *testing.T) {
		err := validateFulfillmentFlags("", "", false, "id", "/path", "https://issuer", false)
		if err == nil {
			t.Fatal("expected error when credentials are set without endpoint")
		}
	})

	t.Run("CA file requires an endpoint", func(t *testing.T) {
		err := validateFulfillmentFlags("", "/etc/osac-csi/fulfillment-ca/bundle.pem", false, "", "", "", true)
		if err == nil {
			t.Fatal("expected error when CA file is set without endpoint")
		}
	})

	t.Run("CA file rejects insecure gRPC", func(t *testing.T) {
		err := validateFulfillmentFlags(
			"fulfillment.svc:8000", "/etc/osac-csi/fulfillment-ca/bundle.pem", true,
			"id", "/path", "https://issuer", false,
		)
		if err == nil {
			t.Fatal("expected error when CA file is combined with grpc-insecure")
		}
	})
}

func TestBuildTokenURL(t *testing.T) {
	t.Run("without trailing slash", func(t *testing.T) {
		got, err := buildTokenURL("https://keycloak.example.com/realms/myrealm")
		if err != nil {
			t.Fatalf("buildTokenURL() returned error: %v", err)
		}
		want := "https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token"
		if got != want {
			t.Fatalf("buildTokenURL() = %q, want %q", got, want)
		}
	})

	t.Run("with trailing slash", func(t *testing.T) {
		got, err := buildTokenURL("https://keycloak.example.com/realms/myrealm/")
		if err != nil {
			t.Fatalf("buildTokenURL() returned error: %v", err)
		}
		want := "https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token"
		if got != want {
			t.Fatalf("buildTokenURL() = %q, want %q", got, want)
		}
	})

	for _, issuerURL := range []string{
		"http://keycloak.example.com/realms/myrealm",
		"https://",
		"https://keycloak.example.com/realms/myrealm?query=value",
	} {
		t.Run("rejects "+issuerURL, func(t *testing.T) {
			if _, err := buildTokenURL(issuerURL); err == nil {
				t.Fatalf("buildTokenURL(%q) returned no error", issuerURL)
			}
		})
	}
}

func TestNewTokenHTTPClient(t *testing.T) {
	if got := newTokenHTTPClient(&tls.Config{}).Timeout; got != tokenHTTPTimeout {
		t.Fatalf("token HTTP timeout = %s, want %s", got, tokenHTTPTimeout)
	}
}

func TestTokenHTTPClientRejectsCredentialRedirects(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetRequests int
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetRequests++
			}))
			defer target.Close()

			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			defer source.Close()

			req, err := http.NewRequest(
				http.MethodPost,
				source.URL,
				strings.NewReader("client_id=client&client_secret=secret"),
			)
			if err != nil {
				t.Fatalf("creating request: %v", err)
			}
			req.Header.Set("Authorization", "Basic credentials")

			client := newTokenHTTPClient(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test server certificate
			resp, err := client.Do(req)
			if err == nil {
				if resp != nil {
					resp.Body.Close()
				}
				t.Fatalf("expected redirect to be rejected")
			}
			if resp != nil {
				resp.Body.Close()
			}
			if targetRequests != 0 {
				t.Fatalf("redirect target received %d requests", targetRequests)
			}
		})
	}
}

func TestTokenHTTPClientRejectsSameHostRedirect(t *testing.T) {
	var targetRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Location", "/redirect-target")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		targetRequests++
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/token", strings.NewReader(testClientSecretRequestBody))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	client := newTokenHTTPClient(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test server certificate
	response, err := client.Do(req)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("expected same-host redirect to be rejected")
	}
	if targetRequests != 0 {
		t.Fatalf("redirect target received %d requests", targetRequests)
	}
}

func TestFulfillmentTLSConfigFromCAFile(t *testing.T) {
	t.Run("loads a PEM CA without insecure verification", func(t *testing.T) {
		_, caPEM := newTestServerCertificate(t, []net.IP{net.ParseIP("127.0.0.1")}, nil)
		caFile := writeTestCAFile(t, caPEM)

		cfg, err := fulfillmentTLSConfig(caFile, false)
		if err != nil {
			t.Fatalf("fulfillmentTLSConfig() returned error: %v", err)
		}
		if cfg.RootCAs == nil {
			t.Fatal("expected the supplied CA root pool")
		}
		if cfg.MinVersion != tls.VersionTLS12 || cfg.InsecureSkipVerify {
			t.Fatalf("unexpected strict TLS config: %+v", cfg)
		}
	})

	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "malformed", data: []byte("not PEM")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fulfillmentTLSConfig(writeTestCAFile(t, test.data), false); err == nil {
				t.Fatal("expected invalid CA file to be rejected")
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		if _, err := fulfillmentTLSConfig(filepath.Join(t.TempDir(), "missing.pem"), false); err == nil {
			t.Fatal("expected missing CA file to be rejected")
		}
	})

	t.Run("rejects insecure verification", func(t *testing.T) {
		if _, err := fulfillmentTLSConfig("/any/path.pem", true); err == nil {
			t.Fatal("expected CA mode with insecure verification to be rejected")
		}
	})

	t.Run("rejects a non-CA certificate", func(t *testing.T) {
		certificate, _ := newTestServerCertificate(t, []net.IP{net.ParseIP("127.0.0.1")}, nil)
		leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
		if _, err := fulfillmentTLSConfig(writeTestCAFile(t, leafPEM), false); err == nil {
			t.Fatal("expected non-CA certificate to be rejected")
		}
	})

	t.Run("accepts a CA certificate with unspecified key usage", func(t *testing.T) {
		caWithoutKeyUsage := newTestCertificateAuthorityWithKeyUsage(t, 0)
		if _, err := fulfillmentTLSConfig(writeTestCAFile(t, caWithoutKeyUsage.pem), false); err != nil {
			t.Fatalf("expected CA certificate with unspecified key usage to be accepted: %v", err)
		}
	})

	t.Run("rejects a CA certificate without certificate-signing usage", func(t *testing.T) {
		caWithoutCertSign := newTestCertificateAuthorityWithKeyUsage(t, x509.KeyUsageDigitalSignature)
		if _, err := fulfillmentTLSConfig(writeTestCAFile(t, caWithoutCertSign.pem), false); err == nil {
			t.Fatal("expected CA certificate without keyCertSign to be rejected")
		}
	})
}

func TestFulfillmentTLSConfigVerifiesServerCertificate(t *testing.T) {
	validAuthority := newTestCertificateAuthority(t)
	validCert := newTestServerCertificateForCA(t, validAuthority, []net.IP{net.ParseIP("127.0.0.1")}, nil)
	validCA := validAuthority.pem
	wrongRootCert, _ := newTestServerCertificate(t, []net.IP{net.ParseIP("127.0.0.1")}, nil)
	wrongSANCert := newTestServerCertificateForCA(t, validAuthority, nil, []string{"localhost"})

	for _, test := range []struct {
		name    string
		cert    tls.Certificate
		caPEM   []byte
		wantErr bool
	}{
		{name: "valid CA and IP SAN", cert: validCert, caPEM: validCA},
		{name: "wrong root", cert: wrongRootCert, caPEM: validCA, wantErr: true},
		{name: "wrong SAN", cert: wrongSANCert, caPEM: validCA, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{test.cert}, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()

			cfg, err := fulfillmentTLSConfig(writeTestCAFile(t, test.caPEM), false)
			if err != nil {
				t.Fatalf("fulfillmentTLSConfig() returned error: %v", err)
			}
			response, err := newTokenHTTPClient(cfg).Get(server.URL)
			if response != nil {
				response.Body.Close()
			}
			if test.wantErr && err == nil {
				t.Fatal("expected TLS verification to fail")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("expected TLS verification to succeed: %v", err)
			}
		})
	}
}

func TestDialFulfillmentVerifiesServerCertificate(t *testing.T) {
	validAuthority := newTestCertificateAuthority(t)
	validCert := newTestServerCertificateForCA(t, validAuthority, []net.IP{net.ParseIP("127.0.0.1")}, nil)
	validCA := validAuthority.pem
	wrongRootCert, _ := newTestServerCertificate(t, []net.IP{net.ParseIP("127.0.0.1")}, nil)
	wrongSANCert := newTestServerCertificateForCA(t, validAuthority, nil, []string{"localhost"})

	for _, test := range []struct {
		name    string
		cert    tls.Certificate
		caPEM   []byte
		wantErr bool
	}{
		{name: "valid CA and IP SAN", cert: validCert, caPEM: validCA},
		{name: "wrong root", cert: wrongRootCert, caPEM: validCA, wantErr: true},
		{name: "wrong SAN", cert: wrongSANCert, caPEM: validCA, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, stopServer := startTestGRPCServer(t, test.cert)
			defer stopServer()

			connection, err := dialFulfillment(address, writeTestCAFile(t, test.caPEM), false, "", "", "")
			if err != nil {
				t.Fatalf("dialFulfillment() returned error: %v", err)
			}
			defer connection.Close()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{})
			if test.wantErr && err == nil {
				t.Fatal("expected gRPC TLS verification to fail")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("expected gRPC TLS verification to succeed: %v", err)
			}
		})
	}
}

func TestNewClientCredentialsTokenSource(t *testing.T) {
	t.Run("valid inputs produce a token source", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(secretFile, []byte("test-secret\n"), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		ts, err := newClientCredentialsTokenSource(
			context.Background(),
			"osac-csi-driver",
			secretFile,
			"https://keycloak.example.com/realms/myrealm",
			&tls.Config{},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ts == nil {
			t.Fatal("expected non-nil token source")
		}
	})

	t.Run("missing secret file returns error", func(t *testing.T) {
		_, err := newClientCredentialsTokenSource(
			context.Background(),
			"osac-csi-driver",
			"/nonexistent/path/secret",
			"https://keycloak.example.com/realms/myrealm",
			&tls.Config{},
		)
		if err == nil {
			t.Fatal("expected error for missing secret file")
		}
	})

	t.Run("empty secret file returns error", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(secretFile, []byte(""), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		_, err := newClientCredentialsTokenSource(
			context.Background(),
			"osac-csi-driver",
			secretFile,
			"https://keycloak.example.com/realms/myrealm",
			&tls.Config{},
		)
		if err == nil {
			t.Fatal("expected error for empty secret file")
		}
	})

	t.Run("whitespace-only secret file returns error", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(secretFile, []byte("  \n\t  \n"), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		_, err := newClientCredentialsTokenSource(
			context.Background(),
			"osac-csi-driver",
			secretFile,
			"https://keycloak.example.com/realms/myrealm",
			&tls.Config{},
		)
		if err == nil {
			t.Fatal("expected error for whitespace-only secret file")
		}
	})

	t.Run("insecureSkipVerify true produces a token source", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(secretFile, []byte("test-secret\n"), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		ts, err := newClientCredentialsTokenSource(
			context.Background(),
			"osac-csi-driver",
			secretFile,
			"https://keycloak.example.com/realms/myrealm",
			&tls.Config{InsecureSkipVerify: true}, //nolint:gosec // legacy compatibility test
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ts == nil {
			t.Fatal("expected non-nil token source")
		}
	})
}

func TestDialFulfillment(t *testing.T) {
	t.Run("without credentials succeeds", func(t *testing.T) {
		conn, err := dialFulfillment("dns:///localhost:8000", "", false, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil connection")
		}
		conn.Close()
	})

	t.Run("with valid credentials succeeds", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "client-secret")
		if err := os.WriteFile(secretFile, []byte("test-secret"), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		conn, err := dialFulfillment(
			"dns:///localhost:8000", "", false,
			"osac-csi-driver", secretFile,
			"https://keycloak.example.com/realms/myrealm",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil connection")
		}
		conn.Close()
	})

	t.Run("with missing secret file returns error", func(t *testing.T) {
		_, err := dialFulfillment(
			"dns:///localhost:8000", "", false,
			"osac-csi-driver", "/nonexistent/secret",
			"https://keycloak.example.com/realms/myrealm",
		)
		if err == nil {
			t.Fatal("expected error for missing secret file")
		}
	})
}

func writeTestCAFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

type testCertificateAuthority struct {
	certificate *x509.Certificate
	privateKey  *rsa.PrivateKey
	pem         []byte
}

func newTestCertificateAuthority(t *testing.T) *testCertificateAuthority {
	return newTestCertificateAuthorityWithKeyUsage(t, x509.KeyUsageCertSign|x509.KeyUsageDigitalSignature)
}

func newTestCertificateAuthorityWithKeyUsage(t *testing.T, keyUsage x509.KeyUsage) *testCertificateAuthority {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              keyUsage,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &testCertificateAuthority{
		certificate: certificate,
		privateKey:  caKey,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}
}

func newTestServerCertificate(t *testing.T, ipAddresses []net.IP, dnsNames []string) (tls.Certificate, []byte) {
	t.Helper()
	authority := newTestCertificateAuthority(t)
	return newTestServerCertificateForCA(t, authority, ipAddresses, dnsNames), authority.pem
}

func newTestServerCertificateForCA(
	t *testing.T,
	authority *testCertificateAuthority,
	ipAddresses []net.IP,
	dnsNames []string,
) tls.Certificate {
	t.Helper()

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("generate server serial: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
	serverDER, err := x509.CreateCertificate(
		rand.Reader, serverTemplate, authority.certificate, &serverKey.PublicKey, authority.privateKey,
	)
	if err != nil {
		t.Fatalf("create server certificate: %v", err)
	}

	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}),
	)
	if err != nil {
		t.Fatalf("load server certificate: %v", err)
	}
	return certificate
}

func startTestGRPCServer(t *testing.T, certificate tls.Certificate) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for gRPC server: %v", err)
	}
	server := grpc.NewServer(grpc.Creds(grpccredentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("gRPC test server stopped: %v", err)
		}
	}()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}

func TestParseBackendMap(t *testing.T) {
	t.Run("empty string returns empty map", func(t *testing.T) {
		m, err := parseBackendMap("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 0 {
			t.Fatalf("expected empty map, got %v", m)
		}
	})

	t.Run("single pair", func(t *testing.T) {
		m, err := parseBackendMap("ontap=/csi/trident/csi.sock")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m["ontap"] != "/csi/trident/csi.sock" {
			t.Fatalf("expected ontap=/csi/trident/csi.sock, got %v", m)
		}
	})

	t.Run("multiple pairs", func(t *testing.T) {
		m, err := parseBackendMap("ontap=/csi/trident/csi.sock,lvms=none")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 2 {
			t.Fatalf("expected 2 pairs, got %d", len(m))
		}
		if m["lvms"] != "none" {
			t.Fatalf("expected lvms=none, got %v", m)
		}
	})

	t.Run("invalid pair returns error", func(t *testing.T) {
		_, err := parseBackendMap("invalid-no-equals")
		if err == nil {
			t.Fatal("expected error for invalid pair")
		}
	})
}
