/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Authenticator", func() {
	var (
		ctx        context.Context
		secretFile string
	)

	BeforeEach(func() {
		ctx = context.Background()
		secretFile = filepath.Join(GinkgoT().TempDir(), "client-secret")
		Expect(os.WriteFile(secretFile, []byte("secret"), 0o600)).To(Succeed())
	})

	newKeycloakServer := func(expectedClientID, expectedSecret, returnToken string) *httptest.Server {
		var server *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"issuer":         server.URL,
				"token_endpoint": server.URL + "/protocol/openid-connect/token",
			})
		})
		mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			body, _ := io.ReadAll(r.Body)
			Expect(string(body)).To(ContainSubstring("grant_type=client_credentials"))
			Expect(string(body)).To(ContainSubstring(fmt.Sprintf("client_id=%s", expectedClientID)))
			Expect(string(body)).To(ContainSubstring(fmt.Sprintf("client_secret=%s", expectedSecret)))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": returnToken,
				"expires_in":   300,
				"token_type":   "Bearer",
			})
		})
		server = httptest.NewServer(mux)
		return server
	}

	newVaultHandler := func(expectedJWT, expectedRole, returnToken string, leaseDuration int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/v1/auth/jwt/login"))
			Expect(r.Header.Get("X-Vault-Namespace")).To(Equal("osac"))

			var reqBody map[string]string
			json.NewDecoder(r.Body).Decode(&reqBody)
			Expect(reqBody["jwt"]).To(Equal(expectedJWT))
			Expect(reqBody["role"]).To(Equal(expectedRole))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{
					"client_token":   returnToken,
					"lease_duration": leaseDuration,
				},
			})
		}
	}

	Describe("Builder", func() {
		It("fails without logger", func() {
			_, err := NewAuthenticator().
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL("http://keycloak/realms/osac").
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("fails without vault address", func() {
			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL("http://keycloak/realms/osac").
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vault address"))
		})

		It("fails without vault namespace", func() {
			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL("http://keycloak/realms/osac").
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vault namespace"))
		})

		It("fails without vault role", func() {
			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetKeycloakIssuerURL("http://keycloak/realms/osac").
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vault role"))
		})

		It("fails without keycloak issuer URL", func() {
			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("keycloak issuer URL"))
		})

		It("fails without keycloak client ID", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("keycloak client ID"))
		})

		It("fails without a keycloak client secret file path", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			_, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("keycloak client secret file"))
		})

		It("defaults auth mount path to jwt", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			var capturedPath string
			vaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"auth": map[string]any{
						"client_token":   "vault-token",
						"lease_duration": 300,
					},
				})
			}))
			DeferCleanup(vaultServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress(vaultServer.URL).
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.VaultToken(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedPath).To(Equal("/v1/auth/jwt/login"))
		})
	})

	Describe("VaultToken", func() {
		It("uses the configured file on first login and caches the lifecycle token", func() {
			keycloakServer := newKeycloakServer("my-client", "my-secret", "kc-jwt-token")
			DeferCleanup(keycloakServer.Close)

			mux := http.NewServeMux()
			mux.HandleFunc("/v1/auth/jwt/login", newVaultHandler("kc-jwt-token", "lifecycle", "vault-result-token", 3600))
			mux.HandleFunc("DELETE /v1/sys/namespaces/tenant-a", func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Expect(r.Header.Get("X-Vault-Namespace")).To(Equal("osac"))
				Expect(r.Header.Get("X-Vault-Token")).To(Equal("vault-result-token"))
				w.WriteHeader(http.StatusNoContent)
			})
			vaultServer := httptest.NewServer(mux)
			DeferCleanup(vaultServer.Close)

			path := filepath.Join(GinkgoT().TempDir(), "projected-secret")
			client, err := NewLifecycleClientFromConfig(logger, BaseConfig{
				Endpoint:                 vaultServer.URL,
				Namespace:                "osac",
				KVMountPath:              "secret",
				KeycloakIssuerURL:        keycloakServer.URL,
				KeycloakClientID:         "my-client",
				KeycloakClientSecretFile: path,
			}, LifecycleConfig{Role: "lifecycle", MountPath: "jwt", TransitMountPath: "transit"}, nil)
			Expect(err).ToNot(HaveOccurred())

			Expect(client.DeleteTenantNamespace(ctx, "tenant-a")).To(MatchError(ContainSubstring("failed to read client secret file")))
			Expect(os.WriteFile(path, []byte("my-secret\n"), 0o600)).To(Succeed())
			Expect(client.DeleteTenantNamespace(ctx, "tenant-a")).To(Succeed())
			Expect(os.Remove(path)).To(Succeed())
			Expect(client.DeleteTenantNamespace(ctx, "tenant-a")).To(Succeed())
		})

		It("caches token on subsequent calls", func() {
			var keycloakCalls atomic.Int32

			var keycloakServer *httptest.Server
			mux := http.NewServeMux()
			mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"issuer":         keycloakServer.URL,
					"token_endpoint": keycloakServer.URL + "/protocol/openid-connect/token",
				})
			})
			mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
				keycloakCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"access_token": "kc-jwt",
					"expires_in":   300,
				})
			})
			keycloakServer = httptest.NewServer(mux)
			DeferCleanup(keycloakServer.Close)

			vaultServer := httptest.NewServer(newVaultHandler("kc-jwt", "lifecycle", "cached-token", 3600))
			DeferCleanup(vaultServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress(vaultServer.URL).
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token1, err := auth.VaultToken(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token1).To(Equal("cached-token"))

			token2, err := auth.VaultToken(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token2).To(Equal("cached-token"))

			Expect(keycloakCalls.Load()).To(Equal(int32(1)))
		})

		It("returns error when keycloak fails", func() {
			var keycloakServer *httptest.Server
			mux := http.NewServeMux()
			mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"issuer":         keycloakServer.URL,
					"token_endpoint": keycloakServer.URL + "/protocol/openid-connect/token",
				})
			})
			mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"error": "invalid_client", "error_description": "Invalid client credentials"}`))
			})
			keycloakServer = httptest.NewServer(mux)
			DeferCleanup(keycloakServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress("http://localhost:8200").
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.VaultToken(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("keycloak"))
		})

		It("returns error when vault login fails", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			vaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"errors":["permission denied"]}`))
			}))
			DeferCleanup(vaultServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress(vaultServer.URL).
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.VaultToken(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("vault"))
			Expect(err.Error()).To(ContainSubstring("403"))
		})

		It("uses custom auth mount path", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			var capturedPath string
			vaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"auth": map[string]any{
						"client_token":   "vault-token",
						"lease_duration": 300,
					},
				})
			}))
			DeferCleanup(vaultServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress(vaultServer.URL).
				SetVaultNamespace("osac").
				SetVaultAuthMountPath("custom-jwt").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.VaultToken(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedPath).To(Equal("/v1/auth/custom-jwt/login"))
		})

		It("is safe for concurrent access", func() {
			keycloakServer := newKeycloakServer("client", "secret", "kc-jwt")
			DeferCleanup(keycloakServer.Close)

			vaultServer := httptest.NewServer(newVaultHandler("kc-jwt", "lifecycle", "concurrent-token", 3600))
			DeferCleanup(vaultServer.Close)

			auth, err := NewAuthenticator().
				SetLogger(logger).
				SetVaultAddress(vaultServer.URL).
				SetVaultNamespace("osac").
				SetVaultRole("lifecycle").
				SetKeycloakIssuerURL(keycloakServer.URL).
				SetKeycloakClientID("client").
				SetKeycloakClientSecretFile(secretFile).
				Build()
			Expect(err).ToNot(HaveOccurred())

			var wg sync.WaitGroup
			errs := make([]error, 10)
			tokens := make([]string, 10)
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					tokens[idx], errs[idx] = auth.VaultToken(ctx)
				}(i)
			}
			wg.Wait()

			for i := 0; i < 10; i++ {
				Expect(errs[i]).ToNot(HaveOccurred())
				Expect(tokens[i]).To(Equal("concurrent-token"))
			}
		})
	})
})
