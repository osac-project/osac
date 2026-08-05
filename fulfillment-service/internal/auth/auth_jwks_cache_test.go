/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	kubefiles "github.com/osac-project/fulfillment-service/internal/kubernetes/files"
	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("JSON web key set cache creation", func() {
	It("Can't be built without a logger", func() {
		_, err := NewJwksCache().
			AddIssuer("https://my-issuer.example.com").
			Build()
		Expect(err).To(MatchError("logger is mandatory"))
	})

	It("Can't be built without at least one issuer", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			Build()
		Expect(err).To(MatchError("at least one issuer must be configured"))
	})

	It("Can't be built with an empty issuer URL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("").
			Build()
		Expect(err).To(MatchError("invalid empty issuer URL"))
	})

	It("Can't be built with an invalid issuer URL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("://bad").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid issuer URL"))
	})

	It("Can't be built with an HTTP issuer URL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("http://my-issuer.example.com").
			Build()
		Expect(err).To(MatchError(
			"issuer URL 'http://my-issuer.example.com' must use the HTTPS scheme",
		))
	})

	It("Can't be built with a negative minimum TTL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("https://my-issuer.example.com").
			SetTTL(-time.Second, time.Hour).
			Build()
		Expect(err).To(MatchError("minimum TTL must be zero or positive"))
	})

	It("Can't be built with a negative maximum TTL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("https://my-issuer.example.com").
			SetTTL(time.Second, -time.Hour).
			Build()
		Expect(err).To(MatchError("maximum TTL must be zero or positive"))
	})

	It("Can't be built with minimum TTL greater than maximum TTL", func() {
		_, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("https://my-issuer.example.com").
			SetTTL(time.Hour, time.Second).
			Build()
		Expect(err).To(MatchError("minimum TTL must be less than or equal to maximum TTL"))
	})

	It("Can be built with valid parameters", func() {
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("https://my-issuer.example.com").
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(cache).ToNot(BeNil())
	})

	It("Can be built with multiple issuers", func() {
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuers(
				"https://issuer-one.example.com",
				"https://issuer-two.example.com",
			).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(cache).ToNot(BeNil())
	})

	It("Can be built with custom CA pool", func() {
		caPool := x509.NewCertPool()
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer("https://my-issuer.example.com").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(cache).ToNot(BeNil())
	})

	It("Automatically adds Kubernetes issuer when token file exists", func() {
		// Create a temporary directory containing a valid Kubernetes service account token:
		tmpDir, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})
		issuerUrl := "https://kubernetes.default.svc.cluster.local"
		token := MakeTokenString(issuerUrl, "Bearer", time.Minute)
		tokenFile := filepath.Join(tmpDir, kubefiles.ServiceAccountToken)
		tokenDir := filepath.Dir(tokenFile)
		err = os.MkdirAll(tokenDir, 0700)
		Expect(err).ToNot(HaveOccurred())
		err = os.WriteFile(tokenFile, []byte(token), 0600)
		Expect(err).ToNot(HaveOccurred())

		// Build the cache with no issuers other than the Kubernetes issuer. This would normally fail because
		// there are no issuers, but the Kubernetes issuer is automatically added because the token file exists.
		cache, err := NewJwksCache().
			SetLogger(logger).
			SetRoot(tmpDir).
			AddKubernetesIssuer(true).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(cache).ToNot(BeNil())
	})

	It("Falls back to no Kubernetes issuer when token file does not exist", func() {
		// Create the an empty temporary directory:
		tmpDir, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})

		// No token file exists, and no other issuer is configured, so the build should fail because there are
		// no issuers.
		_, err = NewJwksCache().
			SetLogger(logger).
			SetRoot(tmpDir).
			AddKubernetesIssuer(true).
			Build()
		Expect(err).To(MatchError("at least one issuer must be configured"))
	})
})

var _ = Describe("JWKS cache behaviour with a working issuer server", func() {
	var (
		issuerServer *ghttp.Server
		issuerUrl    string
		caPool       *x509.CertPool
	)

	BeforeEach(func() {
		// Create a server that responds to the OpenID discovery and JWKS endpoints:
		issuerServer = ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl = issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": issuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWithJSONEncoded(http.StatusOK, MakeJwksObject()),
		)

		// Create a CA pool that trusts the server's certificate:
		caPool = x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())
	})

	It("Returns a key for a trusted issuer and valid key identifier", func(ctx context.Context) {
		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(0, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the key is returned:
		key, err := cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())

		// Verify that the key is an RSA public key:
		rsaKey, ok := key.(*rsa.PublicKey)
		Expect(ok).To(BeTrue())
		Expect(rsaKey.N).ToNot(BeNil())
	})

	It("Returns error for an untrusted issuer", func(ctx context.Context) {
		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(0, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, "https://untrusted.example.com", "123")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ErrBadIssuer))
	})

	It("Returns error when key identifier is not found", func(ctx context.Context) {
		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "no-such-key")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ErrBadKey))
	})

	It("Caches keys after first load", func(ctx context.Context) {
		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(0, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// First call loads from server:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())

		// Second call should use the cache and not hit the server again. We verify by counting the
		// number of requests received by the server: only the discovery and JWKS requests from the
		// first call should be present.
		count := len(issuerServer.ReceivedRequests())
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(issuerServer.ReceivedRequests()).To(HaveLen(count))
	})

	It("Respects minimum TTL throttling", func(ctx context.Context) {
		// Create a cache with a high minimum TTL, so that the key is not refreshed:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(time.Minute, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// First request loads the key set and succeeds:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())

		// Second request for the same issuer URL but a different key identifier will usually trigger a
		// refresh, but because the minimum TTL is high, the key will not be refreshed.
		count := len(issuerServer.ReceivedRequests())
		_, err = cache.Get(ctx, issuerUrl, "unknown-key")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ErrBadKey))
		Expect(issuerServer.ReceivedRequests()).To(HaveLen(count))
	})

	It("Triggers background refresh when maximum TTL has elapsed", func(ctx context.Context) {
		// Create a cache with a very short maximum TTL so that the key is immediately considered stale after
		// the first load:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(0, time.Nanosecond).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// First call loads the key set:
		key, err := cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())

		// Second call should return the cached key immediately, and trigger a background refresh. We verify it
		// still returns a key even though the maximum TTL has elapsed.
		key, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())

		// Give the background goroutine time to complete, then verify the server received more requests:
		Eventually(func(g Gomega) {
			count := len(issuerServer.ReceivedRequests())
			g.Expect(count).To(BeNumerically(">", 2))
		}).Should(Succeed())
	})

	It("Refreshes issuers independently of each other", func(ctx context.Context) {
		// Create a second issuer server:
		issuerServer2 := ghttp.NewUnstartedServer()
		issuerServer2.Writer = GinkgoWriter
		issuerServer2.HTTPTestServer.EnableHTTP2 = true
		issuerServer2.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer2.Close)
		issuerUrl2 := issuerServer2.URL()
		issuerServer2.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl2,
					"jwks_uri": issuerUrl2 + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer2.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWithJSONEncoded(http.StatusOK, MakeJwksObject()),
		)

		// Create a CA pool that trusts both servers:
		multiCaPool := x509.NewCertPool()
		multiCaPool.AddCert(issuerServer.HTTPTestServer.Certificate())
		multiCaPool.AddCert(issuerServer2.HTTPTestServer.Certificate())

		// Create a cache with a high minimum TTL so that the throttle would prevent a second refresh if it were
		// global:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuers(issuerUrl, issuerUrl2).
			SetCaPool(multiCaPool).
			SetTTL(time.Minute, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Load keys from the first issuer:
		key, err := cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())

		// Loading keys from the second issuer should succeed even though the first issuer was just refreshed
		// and the minimum TTL has not elapsed:
		key, err = cache.Get(ctx, issuerUrl2, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())
	})

	It("Returns the correct key when two issuers use the same key identifier", func(ctx context.Context) {
		// Generate a second RSA key pair distinct from the default test key:
		secondKey, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).ToNot(HaveOccurred())
		secondE := big.NewInt(int64(secondKey.E))
		secondN := secondKey.N
		secondJwks := map[string]any{
			"keys": []any{
				map[string]any{
					"kid": "123",
					"kty": "RSA",
					"alg": "RS256",
					"e":   base64.RawURLEncoding.EncodeToString(secondE.Bytes()),
					"n":   base64.RawURLEncoding.EncodeToString(secondN.Bytes()),
				},
			},
		}

		// Create a second issuer server that serves the second key under the same kid '123':
		secondIssuerServer := ghttp.NewUnstartedServer()
		secondIssuerServer.Writer = GinkgoWriter
		secondIssuerServer.HTTPTestServer.EnableHTTP2 = true
		secondIssuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(secondIssuerServer.Close)
		secondIssuerUrl := secondIssuerServer.URL()
		secondIssuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   secondIssuerUrl,
					"jwks_uri": secondIssuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		secondIssuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWithJSONEncoded(http.StatusOK, secondJwks),
		)

		// Create a CA pool that trusts both servers:
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())
		caPool.AddCert(secondIssuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuers(issuerUrl, secondIssuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Load key '123' from the first issuer:
		key1, err := cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		rsaKey1, ok := key1.(*rsa.PublicKey)
		Expect(ok).To(BeTrue())

		// Load key '123' from the second issuer:
		key2, err := cache.Get(ctx, secondIssuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		rsaKey2, ok := key2.(*rsa.PublicKey)
		Expect(ok).To(BeTrue())

		// The two keys must be different even though they share the same kid:
		Expect(rsaKey1.N.Cmp(rsaKey2.N)).ToNot(Equal(0))
	})

	It("Handles concurrent get calls safely", func(ctx context.Context) {
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			SetTTL(0, time.Hour).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Run multiple concurrent Get calls to exercise the locking paths:
		const n = 20
		results := make(chan error, n)
		for range n {
			go func() {
				_, err := cache.Get(ctx, issuerUrl, "123")
				results <- err
			}()
		}
		for range n {
			err := <-results
			Expect(err).ToNot(HaveOccurred())
		}
	})
})

var _ = Describe("JWKS cache behaviour with bad issuer servers", func() {
	It("Returns error when OIDC discovery fails", func(ctx context.Context) {
		// Create a server that responds with an internal server error to the OpenID discovery endpoint:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWith(http.StatusInternalServerError, ""),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
	})

	It("Returns error when JWKS endpoint fails", func(ctx context.Context) {
		// Create a server that responds correction to the OpenID discovery endpoint, but with an internal
		// server error to the JWKS endpoint:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": issuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWith(http.StatusInternalServerError, ""),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
	})

	It("Returns error when discovery document has no 'jwks_uri' value", func(ctx context.Context) {
		// Create a server that responds with a discovery document that has no 'jwks_uri' value:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer": issuerUrl,
				},
			),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerServer.URL()).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does not contain a 'jwks_uri' value"))
	})

	It("Returns error when discovered 'jwks_uri' value uses HTTP scheme", func(ctx context.Context) {
		// Create a server that responds with a discovery document that has a 'jwks_uri' value that uses
		// the HTTP scheme:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": "http://insecure.example.com/.well-known/jwks.json",
				},
			),
		)
		failCaPool := x509.NewCertPool()
		failCaPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerServer.URL()).
			SetCaPool(failCaPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must use the HTTPS scheme"))
	})

	It("Skips keys with missing fields", func(ctx context.Context) {
		// Create a server that responds with a JWKS object that has a key with a missing 'kid' field:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": issuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]any{
				"keys": []any{
					map[string]any{
						"kid": "bad-key",
						"kty": "RSA",
					},
				},
			}),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "bad-key")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ErrBadKey))
	})

	It("Skips keys with unsupported key type", func(ctx context.Context) {
		// Create a server that responds with a JWKS containing only a key with an unsupported type:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": issuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]any{
				"keys": []any{
					map[string]any{
						"kid": "unknown-key",
						"kty": "QUANTUM",
					},
				},
			}),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// The unsupported key should be skipped, so requesting it returns "no key found":
		_, err = cache.Get(ctx, issuerUrl, "unknown-key")
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ErrBadKey))
	})

	It("Returns error when discovery document contains invalid JSON", func(ctx context.Context) {
		// Create a server that responds with a discovery document that contains invalid JSON:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWith(http.StatusOK, "not valid json{{{"),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to parse discovery document"))
	})

	It("Returns error when JWKS endpoint returns invalid JSON", func(ctx context.Context) {
		// Create a server that responds with a JWKS endpoint that returns invalid JSON:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"issuer":   issuerUrl,
					"jwks_uri": issuerUrl + "/.well-known/jwks.json",
				},
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.RespondWith(http.StatusOK, "not valid json{{{"),
		)
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			AddIssuer(issuerUrl).
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the error is returned:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
	})

	It("Returns error when token file is unreadable at refresh time", func(ctx context.Context) {
		// Create the server:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		caPool := x509.NewCertPool()
		caPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create a temporary directory with a valid Kubernetes service account token:
		tmpDir, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})
		token := MakeTokenString(issuerUrl, "Bearer", time.Minute)
		tokenFile := filepath.Join(tmpDir, kubefiles.ServiceAccountToken)
		tokenDir := filepath.Dir(tokenFile)
		err = os.MkdirAll(tokenDir, 0700)
		Expect(err).ToNot(HaveOccurred())
		err = os.WriteFile(tokenFile, []byte(token), 0600)
		Expect(err).ToNot(HaveOccurred())

		// Build the cache while the token file is readable:
		cache, err := NewJwksCache().
			SetLogger(logger).
			SetRoot(tmpDir).
			SetCaPool(caPool).
			AddKubernetesIssuer(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Remove the token file so that the refresh attempt fails when it tries to read it:
		err = os.Remove(tokenFile)
		Expect(err).ToNot(HaveOccurred())

		// Verify that Get returns an error about the unreadable token file:
		_, err = cache.Get(ctx, issuerUrl, "123")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to read token from file"))
	})

	It("Sends bearer token when fetching from Kubernetes issuer", func(ctx context.Context) {
		// Create the server:
		issuerServer := ghttp.NewUnstartedServer()
		issuerServer.Writer = GinkgoWriter
		issuerServer.HTTPTestServer.EnableHTTP2 = true
		issuerServer.HTTPTestServer.StartTLS()
		DeferCleanup(issuerServer.Close)
		issuerUrl := issuerServer.URL()
		issuerCaPool := x509.NewCertPool()
		issuerCaPool.AddCert(issuerServer.HTTPTestServer.Certificate())

		// Create a temporary directory containing a valid Kubernetes service account token:
		tmpDir, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})
		token := MakeTokenString(issuerUrl, "Bearer", time.Minute)
		tokenFile := filepath.Join(tmpDir, kubefiles.ServiceAccountToken)
		tokenDir := filepath.Dir(tokenFile)
		err = os.MkdirAll(tokenDir, 0700)
		Expect(err).ToNot(HaveOccurred())
		err = os.WriteFile(tokenFile, []byte(token), 0600)
		Expect(err).ToNot(HaveOccurred())

		// Configure the server so that it verifies that the bearer token is sent in the authorization header to
		// both the OpenID discovery and JWKS endpoints:
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/openid-configuration",
			ghttp.CombineHandlers(
				ghttp.VerifyHeaderKV(Authorization, "Bearer "+token),
				ghttp.RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"issuer":   issuerUrl,
						"jwks_uri": issuerUrl + "/.well-known/jwks.json",
					},
				),
			),
		)
		issuerServer.RouteToHandler(
			http.MethodGet,
			"/.well-known/jwks.json",
			ghttp.CombineHandlers(
				ghttp.VerifyHeaderKV(Authorization, "Bearer "+token),
				ghttp.RespondWithJSONEncoded(http.StatusOK, MakeJwksObject()),
			),
		)

		// Create the cache:
		cache, err := NewJwksCache().
			SetLogger(logger).
			SetRoot(tmpDir).
			SetCaPool(issuerCaPool).
			AddKubernetesIssuer(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that the key is returned:
		key, err := cache.Get(ctx, issuerUrl, "123")
		Expect(err).ToNot(HaveOccurred())
		Expect(key).ToNot(BeNil())
	})
})
