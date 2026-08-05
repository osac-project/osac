/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jwe

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// testCAInfo holds a CA's key and certificate for test leaf cert generation.
type testCAInfo struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
	pool *x509.CertPool
}

var testCA *testCAInfo

var _ = Describe("Token", func() {
	var (
		ctx                context.Context
		tmpDir             string
		signingCertFile    string
		signingKeyFile     string
		encryptionCertFile string
		encryptionKeyFile  string
		sealer             *Sealer
		issuer             string
		audience           string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "token-test-*")
		Expect(err).ToNot(HaveOccurred())

		signingCertFile = filepath.Join(tmpDir, "signing-tls.crt")
		signingKeyFile = filepath.Join(tmpDir, "signing-tls.key")
		encryptionCertFile = filepath.Join(tmpDir, "encryption-tls.crt")
		encryptionKeyFile = filepath.Join(tmpDir, "encryption-tls.key")

		testCA = generateTestCA()
		generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)
		generateLeafCert(encryptionCertFile, encryptionKeyFile, "fulfillment-token-encryption", testCA)

		issuer = "https://fulfillment.test.example.com"
		audience = "test-audience"
		sealer, err = NewSealer().
			SetLogger(logger).
			SetSigningCertFile(signingCertFile).
			SetSigningKeyFile(signingKeyFile).
			SetEncryptionCertFile(encryptionCertFile).
			SetIssuer(issuer).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Describe("Seal and Open roundtrip", func() {
		It("Produces a nested JWT and opens it via JWKS", func() {
			claims := map[string]any{
				"custom_key": "custom_value",
				"nested": map[string]any{
					"uri":   "wss://example.com/test",
					"token": "bearer-token-123",
				},
			}

			tokenString, expiresAt, err := sealer.Seal(ctx, "jane", audience, claims, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(tokenString).ToNot(BeEmpty())
			Expect(expiresAt).To(BeTemporally("~", time.Now().Add(30*time.Second), 2*time.Second))

			// The token should be a JWE (5 dot-separated parts).
			Expect(countDots(tokenString)).To(Equal(4), "expected JWE compact serialization (5 parts)")

			// The JWE header should indicate DEFLATE compression.
			headerJSON, err := base64.RawURLEncoding.DecodeString(strings.SplitN(tokenString, ".", 2)[0])
			Expect(err).ToNot(HaveOccurred())
			var jweHeader map[string]any
			Expect(json.Unmarshal(headerJSON, &jweHeader)).To(Succeed())
			Expect(jweHeader["zip"]).To(Equal("DEF"))

			// Serve JWKS from the sealer for verification.
			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			parsed, err := opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.Subject).To(Equal("jane"))
			Expect(parsed.JTI).ToNot(BeEmpty())
			Expect(parsed.Custom["custom_key"]).To(Equal("custom_value"))

			nested, ok := parsed.Custom["nested"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(nested["uri"]).To(Equal("wss://example.com/test"))
			Expect(nested["token"]).To(Equal("bearer-token-123"))
		})

		It("Roundtrips a token with empty custom claims", func() {
			tokenString, _, err := sealer.Seal(ctx, "admin", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			parsed, err := opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.Subject).To(Equal("admin"))
			Expect(parsed.Custom).To(BeEmpty())
		})
	})

	Describe("Expiry", func() {
		It("Rejects an expired token", func() {
			tokenString, _, err := sealer.Seal(ctx, "jane", audience, nil, -10*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exp"))
		})
	})

	Describe("JTI single-use", func() {
		It("Rejects a replayed token", func() {
			tokenString, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already used"))
		})
	})

	Describe("Wrong keys", func() {
		It("Rejects a token encrypted with the wrong key", func() {
			tokenString, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Create an opener with a different decryption key.
			wrongKeyFile := filepath.Join(tmpDir, "wrong-key.key")
			wrongCertFile := filepath.Join(tmpDir, "wrong-key.crt")
			generateLeafCert(wrongCertFile, wrongKeyFile, "wrong", testCA)

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(wrongKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("decrypt"))
		})

		It("Rejects a token signed with a key not in JWKS", func() {
			// Create a second sealer with a different signing key.
			otherSigningCrt := filepath.Join(tmpDir, "other-signing.crt")
			otherSigningKey := filepath.Join(tmpDir, "other-signing.key")
			generateLeafCert(otherSigningCrt, otherSigningKey, "other-signer", testCA)

			otherSealer, err := NewSealer().
				SetLogger(logger).
				SetSigningCertFile(otherSigningCrt).
				SetSigningKeyFile(otherSigningKey).
				SetEncryptionCertFile(encryptionCertFile).
				SetIssuer(issuer).
				Build()
			Expect(err).ToNot(HaveOccurred())

			tokenString, _, err := otherSealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Serve JWKS from the *original* sealer (different signing key).
			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("JWKS endpoint", func() {
		It("Returns a valid JWKS with the signing key", func() {
			set, err := sealer.JWKSet(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(set.Len()).To(Equal(1))

			data, err := json.Marshal(set)
			Expect(err).ToNot(HaveOccurred())

			var jwksResp map[string]any
			Expect(json.Unmarshal(data, &jwksResp)).To(Succeed())

			keys := jwksResp["keys"].([]any)
			Expect(keys).To(HaveLen(1))
			key := keys[0].(map[string]any)
			Expect(key["kty"]).To(Equal("RSA"))
			Expect(key["use"]).To(Equal("sig"))
			Expect(key["alg"]).To(Equal("RS256"))
			Expect(key["kid"]).ToNot(BeEmpty())
		})
	})

	Describe("JWKS URL validation", func() {
		It("Rejects a non-HTTPS JWKS URL", func() {
			_, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL("http://example.com/.well-known/jwks.json").
				SetIssuer(issuer).
				SetAudience(audience).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("HTTPS"))
		})
	})

	Describe("Audience validation", func() {
		It("Rejects a token with wrong audience", func() {
			tokenString, _, err := sealer.Seal(ctx, "jane", "aud-a", nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience("aud-b").
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = opener.Open(context.Background(), tokenString)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("aud"))
		})

		It("Accepts a token when audience matches", func() {
			tokenString, _, err := sealer.Seal(ctx, "jane", "my-service", nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience("my-service").
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			parsed, err := opener.Open(context.Background(), tokenString)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.Subject).To(Equal("jane"))
		})

		It("Accepts a token when opener audience is in multi-value aud claim", func() {
			// Build a token with multiple audiences by using the JWT builder directly,
			// since Seal now always sets a single-element aud.
			now := time.Now()
			tok, err := jwt.NewBuilder().
				Issuer(issuer).
				Audience([]string{"aud-a", "aud-b"}).
				Subject("jane").
				JwtID("multi-aud-test-jti").
				IssuedAt(now).
				Expiration(now.Add(30 * time.Second)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Import the signing private key as JWK with kid matching the
			// sealer's JWKS, so the keyset-based verification succeeds.
			sigKey := loadTestPrivateKey(signingKeyFile)
			sigJWK, err := jwk.Import(sigKey)
			Expect(err).ToNot(HaveOccurred())
			jwkSet, err := sealer.JWKSet(ctx)
			Expect(err).ToNot(HaveOccurred())
			pubKey, ok := jwkSet.Key(0)
			Expect(ok).To(BeTrue())
			kid, ok := pubKey.KeyID()
			Expect(ok).To(BeTrue())
			Expect(sigJWK.Set(jwk.KeyIDKey, kid)).To(Succeed())

			encCert := loadTestCertificate(encryptionCertFile)

			serialized, err := jwt.NewSerializer().
				Sign(jwt.WithKey(jwa.RS256(), sigJWK)).
				Encrypt(
					jwt.WithKey(jwa.RSA_OAEP_256(), encCert.PublicKey),
					jwt.WithEncryptOption(jwe.WithContentEncryption(jwa.A256GCM())),
					jwt.WithEncryptOption(jwe.WithCompress(jwa.Deflate())),
				).
				Serialize(tok)
			Expect(err).ToNot(HaveOccurred())

			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			// Opener configured for "aud-b" should accept this token because
			// "aud-b" is present in the token's aud array.
			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience("aud-b").
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			parsed, err := opener.Open(context.Background(), string(serialized))
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed.Subject).To(Equal("jane"))
		})
	})

	Describe("certKeyReloader", func() {
		It("Rejects mismatched cert and key", func() {
			mismatchedCertFile := filepath.Join(tmpDir, "mismatch-tls.crt")
			mismatchedKeyFile := filepath.Join(tmpDir, "mismatch-tls.key")
			otherCertFile := filepath.Join(tmpDir, "other-tls.crt")
			otherKeyFile := filepath.Join(tmpDir, "other-tls.key")

			generateLeafCert(mismatchedCertFile, mismatchedKeyFile, "signer", testCA)
			generateLeafCert(otherCertFile, otherKeyFile, "signer", testCA)

			// Use the cert from one pair and the key from another.
			_, err := NewSealer().
				SetLogger(logger).
				SetSigningCertFile(mismatchedCertFile).
				SetSigningKeyFile(otherKeyFile).
				SetEncryptionCertFile(encryptionCertFile).
				SetIssuer(issuer).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not match"))
		})

		It("Reloads when the cert file changes", func() {
			claims := map[string]any{"test": "value"}

			tokenString1, _, err := sealer.Seal(ctx, "jane", audience, claims, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Regenerate the cert/key files (simulating cert-manager rotation).
			time.Sleep(10 * time.Millisecond) // ensure mtime differs
			generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)

			// Seal again -- should use the new key.
			tokenString2, _, err := sealer.Seal(ctx, "jane", audience, claims, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// The two tokens should be different (different keys, different JTI).
			Expect(tokenString1).ToNot(Equal(tokenString2))
		})

		It("Preserves consistent state when private key reload fails", func() {
			// Seal with original keys -- should work.
			_, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Overwrite key file with a key from a different cert pair.
			otherCertFile := filepath.Join(tmpDir, "other-split.crt")
			otherKeyFile := filepath.Join(tmpDir, "other-split.key")
			generateLeafCert(otherCertFile, otherKeyFile, "other-split", testCA)
			otherKeyPEM, err := os.ReadFile(otherKeyFile)
			Expect(err).ToNot(HaveOccurred())
			time.Sleep(10 * time.Millisecond)
			Expect(os.WriteFile(signingKeyFile, otherKeyPEM, 0o600)).To(Succeed())

			// Touch cert file so mtime triggers reload.
			certPEM, err := os.ReadFile(signingCertFile)
			Expect(err).ToNot(HaveOccurred())
			time.Sleep(10 * time.Millisecond)
			Expect(os.WriteFile(signingCertFile, certPEM, 0o600)).To(Succeed())

			// Seal should fail with mismatch error.
			_, _, err = sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("does not match"))

			// Restore the correct key pair.
			time.Sleep(10 * time.Millisecond)
			generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)

			// Seal should succeed -- struct was not corrupted by the failed reload.
			_, _, err = sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Reloads when only the key file changes", func() {
			_, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Record original cert mtime.
			certInfo, err := os.Stat(signingCertFile)
			Expect(err).ToNot(HaveOccurred())
			origCertMtime := certInfo.ModTime()

			// Generate a new key pair (writes both files).
			time.Sleep(10 * time.Millisecond)
			generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)

			// Reset cert file mtime to original so only key mtime differs.
			Expect(os.Chtimes(signingCertFile, origCertMtime, origCertMtime)).To(Succeed())

			// Seal should use the new key (reload triggered by key mtime).
			_, _, err = sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Does not reload when neither file has changed", func() {
			_, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			set1, err := sealer.JWKSet(ctx)
			Expect(err).ToNot(HaveOccurred())
			key1, ok := set1.Key(0)
			Expect(ok).To(BeTrue())
			kid1, ok := key1.KeyID()
			Expect(ok).To(BeTrue())

			// Seal again without touching any files.
			_, _, err = sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			set2, err := sealer.JWKSet(ctx)
			Expect(err).ToNot(HaveOccurred())
			key2, ok := set2.Key(0)
			Expect(ok).To(BeTrue())
			kid2, ok := key2.KeyID()
			Expect(ok).To(BeTrue())
			Expect(kid2).To(Equal(kid1))
		})
	})

	Describe("Opener JWKS refresh", func() {
		It("Refreshes JWKS and succeeds when the signing key rotates", func() {
			jwksServer, jwksCAPool := serveJWKS(sealer)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Seal and open with the original key (cache hit path).
			token1, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
			parsed1, err := opener.Open(ctx, token1)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed1.Subject).To(Equal("jane"))

			// Rotate signing cert/key (simulates cert-manager rotation).
			// Sealer hot-reloads via mtime; JWKS endpoint now serves the new kid.
			time.Sleep(10 * time.Millisecond)
			generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)

			// Seal a token signed with the rotated key (new kid).
			token2, _, err := sealer.Seal(ctx, "jane", audience, nil, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())

			// Open must succeed: opener detects unknown kid, refreshes JWKS
			// from the endpoint (now serving the new key), and verifies.
			parsed2, err := opener.Open(ctx, token2)
			Expect(err).ToNot(HaveOccurred())
			Expect(parsed2.Subject).To(Equal("jane"))
		})

		It("Concurrent Open calls succeed when refresh is in-flight", func() {
			jwksServer, jwksCAPool, reqCount := serveSlowJWKS(sealer, 200*time.Millisecond)
			defer jwksServer.Close()

			opener, err := NewOpener().
				SetContext(context.Background()).
				SetLogger(logger).
				SetDecryptionKeyFile(encryptionKeyFile).
				SetJWKSURL(jwksServer.URL).
				SetIssuer(issuer).
				SetAudience(audience).
				SetCache(newTestCache(jwksServer.URL, jwksCAPool)).
				Build()
			Expect(err).ToNot(HaveOccurred())

			time.Sleep(10 * time.Millisecond)
			generateLeafCert(signingCertFile, signingKeyFile, "fulfillment-token-signer", testCA)

			const n = 5
			tokens := make([]string, n)
			for i := range tokens {
				tok, _, err := sealer.Seal(ctx, fmt.Sprintf("user-%d", i), audience, nil, 30*time.Second)
				Expect(err).ToNot(HaveOccurred())
				tokens[i] = tok
			}

			var wg sync.WaitGroup
			errs := make([]error, n)
			subs := make([]string, n)
			wg.Add(n)
			for i := range tokens {
				go func(i int) {
					defer wg.Done()
					defer GinkgoRecover()
					claims, err := opener.Open(ctx, tokens[i])
					errs[i] = err
					if claims != nil {
						subs[i] = claims.Subject
					}
				}(i)
			}
			wg.Wait()

			for i := range errs {
				Expect(errs[i]).ToNot(HaveOccurred(), "request %d failed", i)
				Expect(subs[i]).To(Equal(fmt.Sprintf("user-%d", i)))
			}

			// Build() fetches once. After rotation, singleflight coalesces
			// all concurrent refreshes into one additional request.
			Expect(reqCount.Load()).To(Equal(int32(2)))
		})
	})
})

// countDots counts the number of '.' characters in a string.
func countDots(s string) int {
	count := 0
	for _, c := range s {
		if c == '.' {
			count++
		}
	}
	return count
}

// newTestCache creates a jwk.Cache with the given test server URL registered and ready.
func newTestCache(serverURL string, caPool *x509.CertPool) *jwk.Cache {
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

// serveJWKS starts a TLS test server serving the sealer's JWKS and returns
// the server along with a CA pool that trusts its certificate.
func serveJWKS(sealer *Sealer) (*httptest.Server, *x509.CertPool) {
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

// serveSlowJWKS is like serveJWKS but delays the first response by the
// given duration. Returns an atomic request counter.
func serveSlowJWKS(sealer *Sealer, firstDelay time.Duration) (*httptest.Server, *x509.CertPool, *atomic.Int32) {
	var reqCount atomic.Int32
	var once sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		once.Do(func() { time.Sleep(firstDelay) })
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
	return server, pool, &reqCount
}

// generateTestCA creates a self-signed test CA.
func generateTestCA() *testCAInfo {
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
	return &testCAInfo{key: caKey, cert: caCert, pool: pool}
}

// generateLeafCert generates a leaf certificate signed by the given CA and writes
// the cert and key files to disk.
func generateLeafCert(certFile, keyFile, dnsName string, ca *testCAInfo) {
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

// loadTestPrivateKey reads and parses an RSA private key from a PEM file.
func loadTestPrivateKey(keyFile string) *rsa.PrivateKey {
	data, err := os.ReadFile(keyFile)
	Expect(err).ToNot(HaveOccurred())
	block, _ := pem.Decode(data)
	Expect(block).ToNot(BeNil())
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	Expect(err).ToNot(HaveOccurred())
	rsaKey, ok := key.(*rsa.PrivateKey)
	Expect(ok).To(BeTrue())
	return rsaKey
}

// loadTestCertificate reads and parses an X.509 certificate from a PEM file.
func loadTestCertificate(certFile string) *x509.Certificate {
	data, err := os.ReadFile(certFile)
	Expect(err).ToNot(HaveOccurred())
	block, _ := pem.Decode(data)
	Expect(block).ToNot(BeNil())
	cert, err := x509.ParseCertificate(block.Bytes)
	Expect(err).ToNot(HaveOccurred())
	return cert
}
