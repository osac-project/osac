package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

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
