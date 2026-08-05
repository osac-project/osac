/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/auth/jwe"
)

var _ = Describe("Ticket", func() {
	var (
		ctx                context.Context
		tmpDir             string
		signingCertFile    string
		signingKeyFile     string
		encryptionCertFile string
		encryptionKeyFile  string
		tokenSealer        *jwe.Sealer
		sealer             *TicketSealer
		issuer             string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "ticket-test-*")
		Expect(err).ToNot(HaveOccurred())

		signingCertFile = filepath.Join(tmpDir, "signing-tls.crt")
		signingKeyFile = filepath.Join(tmpDir, "signing-tls.key")
		encryptionCertFile = filepath.Join(tmpDir, "encryption-tls.crt")
		encryptionKeyFile = filepath.Join(tmpDir, "encryption-tls.key")

		ca := generateTicketTestCA()
		generateTicketLeafCert(signingCertFile, signingKeyFile, "fulfillment-ticket-signer", ca)
		generateTicketLeafCert(encryptionCertFile, encryptionKeyFile, "fulfillment-ticket-encryption", ca)

		issuer = "https://fulfillment.test.example.com"
		tokenSealer, err = jwe.NewSealer().
			SetLogger(logger).
			SetSigningCertFile(signingCertFile).
			SetSigningKeyFile(signingKeyFile).
			SetEncryptionCertFile(encryptionCertFile).
			SetIssuer(issuer).
			Build()
		Expect(err).ToNot(HaveOccurred())
		sealer = NewTicketSealer(tokenSealer)
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Describe("Seal and Open roundtrip", func() {
		It("Roundtrips all ticket fields through seal and open", func() {
			ticket := &Ticket{
				User:        "jane",
				ClientID:    "cli-456",
				ConsoleType: ConsoleTypeVNC,
				TargetURI:   "wss://my-hub:6443/apis/console.osac.openshift.io/v1alpha1/namespaces/ns/computeinstances/vm1/vnc",
				TargetToken: "test-bearer-token",
			}

			tokenString, expiresAt, err := sealer.Seal(ctx, ticket, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(tokenString).ToNot(BeEmpty())
			Expect(expiresAt).To(BeTemporally("~", time.Now().Add(30*time.Second), 2*time.Second))

			// Serve JWKS from the token sealer for verification.
			jwksServer, jwksCAPool := serveTicketJWKS(tokenSealer)
			defer jwksServer.Close()

			tokenOpener, err := jwe.NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(TicketAudience).
				SetCache(newTicketTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			opener := NewTicketOpener(tokenOpener)

			parsed, err := opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.User).To(Equal("jane"))
			Expect(parsed.ClientID).To(Equal("cli-456"))
			Expect(parsed.ConsoleType).To(Equal(ConsoleTypeVNC))
			Expect(parsed.JTI).ToNot(BeEmpty())
			Expect(parsed.TargetURI).To(Equal("wss://my-hub:6443/apis/console.osac.openshift.io/v1alpha1/namespaces/ns/computeinstances/vm1/vnc"))
			Expect(parsed.TargetToken).To(Equal("test-bearer-token"))
		})

		It("Roundtrips a ticket with empty client_id and empty target token", func() {
			ticket := &Ticket{
				User:        "admin",
				ConsoleType: ConsoleTypeSerial,
				TargetURI:   "wss://hub:6443/apis/console.osac.openshift.io/v1alpha1/namespaces/ns/computeinstances/vm2/console",
			}

			tokenString, _, err := sealer.Seal(ctx, ticket, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveTicketJWKS(tokenSealer)
			defer jwksServer.Close()

			tokenOpener, err := jwe.NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(TicketAudience).
				SetCache(newTicketTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			opener := NewTicketOpener(tokenOpener)

			parsed, err := opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.User).To(Equal("admin"))
			Expect(parsed.ClientID).To(BeEmpty())
			Expect(parsed.ConsoleType).To(Equal(ConsoleTypeSerial))
			Expect(parsed.TargetToken).To(BeEmpty())
			Expect(parsed.TargetURI).To(ContainSubstring("/console"))
		})
	})

})

// serveTicketJWKS starts a TLS test server serving the token sealer's JWKS
// and returns the server along with a CA pool that trusts its certificate.
func serveTicketJWKS(sealer *jwe.Sealer) (*httptest.Server, *x509.CertPool) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set, err := sealer.JWKSet(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(set); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return server, pool
}

// testTicketCAInfo holds a CA's key and certificate for test leaf cert generation.
type testTicketCAInfo struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
	pool *x509.CertPool
}

// generateTicketTestCA creates a self-signed test CA.
func generateTicketTestCA() *testTicketCAInfo {
	caKey, err := rsa.GenerateKey(rand.Reader, 3072)
	Expect(err).ToNot(HaveOccurred())

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	Expect(err).ToNot(HaveOccurred())
	caCert, err := x509.ParseCertificate(caCertDER)
	Expect(err).ToNot(HaveOccurred())

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &testTicketCAInfo{key: caKey, cert: caCert, pool: pool}
}

// newTicketTestCache creates a jwk.Cache registered to the given JWKS server URL,
// optionally configured with a custom CA pool for TLS.
func newTicketTestCache(serverURL string, caPool *x509.CertPool) *jwk.Cache {
	cache, err := jwk.NewCache(context.Background(), httprc.NewClient())
	Expect(err).ToNot(HaveOccurred())
	opts := []jwk.RegisterOption{}
	if caPool != nil {
		opts = append(opts, jwk.WithHTTPClient(
			jwk.WrapHTTPClientDefaults(&http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: caPool},
				},
			}),
		))
	}
	err = cache.Register(context.Background(), serverURL, opts...)
	Expect(err).ToNot(HaveOccurred())
	return cache
}

// generateTicketLeafCert generates a leaf certificate signed by the given CA and writes
// the cert and key files to disk.
func generateTicketLeafCert(certFile, keyFile, dnsName string, ca *testTicketCAInfo) {
	leafKey, err := rsa.GenerateKey(rand.Reader, 3072)
	Expect(err).ToNot(HaveOccurred())

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca.cert, &leafKey.PublicKey, ca.key)
	Expect(err).ToNot(HaveOccurred())

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafCertDER})
	err = os.WriteFile(certFile, certPEM, 0o600)
	Expect(err).ToNot(HaveOccurred())

	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	Expect(err).ToNot(HaveOccurred())
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	err = os.WriteFile(keyFile, keyPEM, 0o600)
	Expect(err).ToNot(HaveOccurred())
}
