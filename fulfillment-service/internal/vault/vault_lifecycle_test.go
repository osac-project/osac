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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	vaultapi "github.com/hashicorp/vault/api"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("VaultLifecycleClient", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	newMockTokenSource := func(token string) *MockTokenSource {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mock := NewMockTokenSource(ctrl)
		mock.EXPECT().VaultToken(gomock.Any()).Return(token, nil).AnyTimes()
		return mock
	}

	type requestRecord struct {
		method    string
		path      string
		namespace string
		body      map[string]any
	}

	newTestLifecycleClient := func(handler http.HandlerFunc) (*VaultLifecycleClient, *httptest.Server) {
		server := httptest.NewServer(handler)
		DeferCleanup(server.Close)
		client, err := NewVaultLifecycleClient().
			SetLogger(logger).
			SetAddress(server.URL).
			SetTokenSource(newMockTokenSource("test-token")).
			SetParentNamespace("osac").
			SetKVMountPath("secret").
			SetKeycloakIssuerURL("https://keycloak.example.com/realms/osac").
			SetKeycloakAudience("osac-api").
			SetServiceClientID("test-client").
			Build()
		Expect(err).ToNot(HaveOccurred())
		return client, server
	}

	Describe("Builder", func() {
		DescribeTable("rejects unsafe Transit mount paths", func(mount string) {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetTransitMountPath(mount).
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(MatchError(ContainSubstring("mount path")))
		},
			Entry("empty", ""),
			Entry("nested", "transit/keys"),
			Entry("traversal", "../transit"),
			Entry("KV collision", "secret"),
		)

		It("fails without logger", func() {
			_, err := NewVaultLifecycleClient().
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("fails without address", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("address"))
		})

		It("fails without token source", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetParentNamespace("osac").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("token source"))
		})

		It("fails without parent namespace", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("parent namespace"))
		})

		It("fails without keycloak issuer URL", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("keycloak issuer URL"))
		})

		It("fails without service client ID", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("service client ID"))
		})

		It("fails with invalid KV mount path", func() {
			_, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress("http://localhost:8200").
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetKVMountPath("../escape").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("KV mount path"))
		})

		It("uses default KV mount path and audience", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			}))
			DeferCleanup(server.Close)

			client, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress(server.URL).
				SetTokenSource(newMockTokenSource("token")).
				SetParentNamespace("osac").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(client.kvMountPath).To(Equal("secret"))
			Expect(client.transitMountPath).To(Equal("transit"))
			Expect(client.keycloakAudience).To(Equal("osac-api"))
		})
	})

	Describe("EnsureTenantNamespace", func() {
		DescribeTable("provisions Transit according to ownership", func(tenantName string, expectedMounts int) {
			mounts := 0
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/mounts/custom-transit" {
					mounts++
					Expect(r.Header.Get("X-Vault-Namespace")).To(Equal("osac/" + tenantName))
					var body map[string]any
					Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
					Expect(body["type"]).To(Equal("transit"))
				}
				w.Write([]byte(`{}`))
			})
			client.transitMountPath = "custom-transit"
			Expect(client.EnsureTenantNamespace(ctx, tenantName)).To(Succeed())
			Expect(mounts).To(Equal(expectedMounts))
		},
			Entry("ordinary tenant", "tenant-a", 1),
			Entry("system", "system", 1),
			Entry("shared", "shared", 0),
		)

		DescribeTable("checks an occupied mount", func(mountPath, engine, mountsJSON string, succeeds bool) {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/sys/mounts/" + mountPath:
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"errors":["path is already in use"]}`))
				case "/v1/sys/mounts":
					Expect(r.Method).To(Equal(http.MethodGet))
					Expect(r.Header.Get("X-Vault-Namespace")).To(Equal("osac/tenant-a"))
					w.Write([]byte(mountsJSON))
				default:
					w.Write([]byte(`{}`))
				}
			})
			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			if succeeds {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("configure a different " + engine + " mount path")))
			}
		},
			Entry("existing Transit", "transit", "transit", `{"data":{"transit/":{"type":"transit"}}}`, true),
			Entry("conflicting KV", "transit", "transit", `{"data":{"transit/":{"type":"kv"}}}`, false),
			Entry("missing Transit mount", "transit", "transit", `{"data":{}}`, false),
			Entry("existing KV v2", "secret", "kv", `{"data":{"secret/":{"type":"kv","options":{"version":"2"}}}}`, true),
			Entry("conflicting Transit", "secret", "kv", `{"data":{"secret/":{"type":"transit"}}}`, false),
			Entry("existing KV v1", "secret", "kv", `{"data":{"secret/":{"type":"kv","options":{"version":"1"}}}}`, false),
			Entry("KV without version", "secret", "kv", `{"data":{"secret/":{"type":"kv"}}}`, false),
			Entry("missing KV mount", "secret", "kv", `{"data":{}}`, false),
		)

		DescribeTable("redacts mount request failures", func(mountPath, engine string, status int, inspect bool) {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/mounts/"+mountPath && inspect {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"errors":["existing mount"]}`))
					return
				}
				if r.URL.Path == "/v1/sys/mounts/"+mountPath || r.URL.Path == "/v1/sys/mounts" {
					w.WriteHeader(status)
					w.Write([]byte(`{"errors":["sensitive-provider-response"]}`))
					return
				}
				w.Write([]byte(`{}`))
			})
			// Avoid SDK retry delays in failure tests.
			client.client.SetMaxRetries(0)
			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(MatchError(ContainSubstring("verify lifecycle mount permissions and backend availability")))
			Expect(err.Error()).To(ContainSubstring(engine + " for tenant \"tenant-a\""))
			Expect(err.Error()).To(ContainSubstring("HTTP %d", status))
			if inspect {
				Expect(err.Error()).To(ContainSubstring("failed to inspect mount"))
			} else {
				Expect(err.Error()).To(ContainSubstring("failed to mount"))
			}
			Expect(err.Error()).ToNot(ContainSubstring("sensitive-provider-response"))
			Expect(err.Error()).ToNot(ContainSubstring("test-token"))
		},
			Entry("Transit mount denied", "transit", "transit", http.StatusForbidden, false),
			Entry("Transit mount unavailable", "transit", "transit", http.StatusServiceUnavailable, false),
			Entry("Transit inspection denied", "transit", "transit", http.StatusForbidden, true),
			Entry("KV mount denied", "secret", "kv", http.StatusForbidden, false),
			Entry("KV mount unavailable", "secret", "kv", http.StatusServiceUnavailable, false),
			Entry("KV inspection denied", "secret", "kv", http.StatusForbidden, true),
		)

		It("retries partial provisioning without replacing the existing engine", func() {
			mounted := map[string]bool{}
			roleAttempts := 0
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/sys/mounts/secret", "/v1/sys/mounts/transit":
					if mounted[r.URL.Path] {
						w.WriteHeader(http.StatusBadRequest)
						w.Write([]byte(`{"errors":["existing mount"]}`))
						return
					}
					mounted[r.URL.Path] = true
				case "/v1/sys/mounts":
					w.Write([]byte(`{"data":{"secret/":{"type":"kv","options":{"version":"2"}},"transit/":{"type":"transit"}}}`))
					return
				case "/v1/auth/jwt/role/service-access":
					roleAttempts++
					if roleAttempts == 1 {
						w.WriteHeader(http.StatusForbidden)
						w.Write([]byte(`{"errors":["permission denied"]}`))
						return
					}
				}
				Expect(r.Method).ToNot(Equal(http.MethodDelete))
				w.Write([]byte(`{}`))
			})
			Expect(client.EnsureTenantNamespace(ctx, "tenant-a")).To(HaveOccurred())
			Expect(mounted).To(HaveLen(2))
			Expect(client.EnsureTenantNamespace(ctx, "tenant-a")).To(Succeed())
			Expect(roleAttempts).To(Equal(2))
		})

		DescribeTable("redacts connectivity errors", func(engine string) {
			client, server := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {})
			server.Close()
			client.client.SetMaxRetries(0)
			var err error
			if engine == "transit" {
				err = client.mountTransit(ctx, client.client, "tenant-a")
			} else {
				err = client.mountKV(ctx, client.client, "tenant-a")
			}
			Expect(err).To(MatchError(ContainSubstring("verify backend connectivity and TLS configuration")))
			Expect(err.Error()).To(ContainSubstring(engine))
			Expect(err.Error()).ToNot(ContainSubstring(server.URL))
		},
			Entry("Transit", "transit"),
			Entry("KV", "kv"),
		)

		It("sends all seven requests with correct paths and namespaces", func() {
			var mu sync.Mutex
			var requests []requestRecord

			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				rec := requestRecord{
					method:    r.Method,
					path:      r.URL.Path,
					namespace: r.Header.Get("X-Vault-Namespace"),
				}
				if r.Body != nil {
					bodyBytes, _ := io.ReadAll(r.Body)
					if len(bodyBytes) > 0 {
						var parsed map[string]any
						json.Unmarshal(bodyBytes, &parsed)
						rec.body = parsed
					}
				}
				mu.Lock()
				requests = append(requests, rec)
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(requests).To(HaveLen(7))

			// 1. Create namespace
			Expect(requests[0].method).To(Equal("PUT"))
			Expect(requests[0].path).To(Equal("/v1/sys/namespaces/tenant-a"))
			Expect(requests[0].namespace).To(Equal("osac"))

			// 2. Mount KV v2
			Expect(requests[1].path).To(Equal("/v1/sys/mounts/secret"))
			Expect(requests[1].namespace).To(Equal("osac/tenant-a"))
			Expect(requests[1].body["type"]).To(Equal("kv"))
			Expect(requests[1].body["options"]).To(Equal(map[string]any{"version": "2"}))

			// 3. Mount Transit
			Expect(requests[2].path).To(Equal("/v1/sys/mounts/transit"))
			Expect(requests[2].namespace).To(Equal("osac/tenant-a"))
			Expect(requests[2].body["type"]).To(Equal("transit"))

			// 4. Enable JWT auth
			Expect(requests[3].path).To(Equal("/v1/sys/auth/jwt"))
			Expect(requests[3].namespace).To(Equal("osac/tenant-a"))
			Expect(requests[3].body["type"]).To(Equal("jwt"))

			// 5. Configure JWT auth
			Expect(requests[4].path).To(Equal("/v1/auth/jwt/config"))
			Expect(requests[4].namespace).To(Equal("osac/tenant-a"))
			Expect(requests[4].body["oidc_discovery_url"]).To(Equal(
				"https://keycloak.example.com/realms/osac"))
			Expect(requests[4].body["default_role"]).To(Equal("service-access"))

			// 6. Create policy
			Expect(requests[5].path).To(Equal("/v1/sys/policies/acl/tenant-kv-access"))
			Expect(requests[5].namespace).To(Equal("osac/tenant-a"))

			// 7. Create role
			Expect(requests[6].path).To(Equal("/v1/auth/jwt/role/service-access"))
			Expect(requests[6].namespace).To(Equal("osac/tenant-a"))
			Expect(requests[6].body["role_type"]).To(Equal("jwt"))
			Expect(requests[6].body["user_claim"]).To(Equal("azp"))
		})

		It("tolerates already-exists errors for namespace creation", func() {
			callCount := 0
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if r.URL.Path == "/v1/sys/namespaces/tenant-a" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"errors":["already exists"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(callCount).To(Equal(7))
		})

		It("tolerates existing-mount errors for KV mount", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/mounts/secret" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"errors":["existing mount at secret/"]}`))
					return
				}
				if r.URL.Path == "/v1/sys/mounts" {
					w.Write([]byte(`{"data":{"secret/":{"type":"kv","options":{"version":"2"}}}}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
		})

		It("tolerates already-exists errors for JWT auth enable", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/auth/jwt" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"errors":["path already exists at jwt/"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns error when namespace creation fails with non-exists error", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/namespaces/tenant-a" {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"errors":["internal error"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create namespace"))
		})

		It("returns error when KV mount fails", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/mounts/secret" {
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"errors":["permission denied"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to mount kv"))
		})

		It("returns error when JWT auth configuration fails", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/auth/jwt/config" {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"errors":["internal error"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to configure JWT auth"))
		})

		It("returns error when policy creation fails", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/policies/acl/tenant-kv-access" {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"errors":["internal error"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create policy"))
		})

		It("returns error when role creation fails", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/auth/jwt/role/service-access" {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"errors":["internal error"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create role"))
		})

		It("rejects invalid tenant names", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "../escape")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid characters"))
		})

		It("stops after first failing step", func() {
			callCount := 0
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if r.URL.Path == "/v1/sys/mounts/secret" {
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"errors":["permission denied"]}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(callCount).To(Equal(2))
		})

		It("creates policy with correct KV mount path", func() {
			var policyBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/sys/policies/acl/tenant-kv-access" {
					bodyBytes, _ := io.ReadAll(r.Body)
					var parsed map[string]any
					json.Unmarshal(bodyBytes, &parsed)
					policyBody, _ = parsed["policy"].(string)
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			}))
			DeferCleanup(server.Close)

			client, err := NewVaultLifecycleClient().
				SetLogger(logger).
				SetAddress(server.URL).
				SetTokenSource(newMockTokenSource("test-token")).
				SetParentNamespace("osac").
				SetKVMountPath("custom-kv").
				SetKeycloakIssuerURL("https://keycloak/realms/osac").
				SetServiceClientID("test-client").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = client.EnsureTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(policyBody).To(ContainSubstring(`"custom-kv/data/*"`))
			Expect(policyBody).To(ContainSubstring(`"custom-kv/metadata/*"`))
			Expect(policyBody).ToNot(ContainSubstring("transit"))
		})

		It("creates role with correct bound claims for service client", func() {
			var roleBody map[string]any
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/auth/jwt/role/service-access" {
					bodyBytes, _ := io.ReadAll(r.Body)
					json.Unmarshal(bodyBytes, &roleBody)
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.EnsureTenantNamespace(ctx, "my-tenant")
			Expect(err).ToNot(HaveOccurred())

			boundClaims, ok := roleBody["bound_claims"].(map[string]any)
			Expect(ok).To(BeTrue())
			clients, ok := boundClaims["azp"].([]any)
			Expect(ok).To(BeTrue())
			Expect(clients).To(ConsistOf("test-client"))

			audiences, ok := roleBody["bound_audiences"].([]any)
			Expect(ok).To(BeTrue())
			Expect(audiences).To(ConsistOf("osac-api"))
			Expect(roleBody["policies"]).To(ConsistOf("tenant-kv-access"))
		})
	})

	Describe("DeleteTenantNamespace", func() {
		It("sends DELETE request with correct path and namespace", func() {
			var mu sync.Mutex
			var requests []requestRecord

			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				rec := requestRecord{
					method:    r.Method,
					path:      r.URL.Path,
					namespace: r.Header.Get("X-Vault-Namespace"),
				}
				mu.Lock()
				requests = append(requests, rec)
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.DeleteTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(requests).To(HaveLen(1))

			Expect(requests[0].method).To(Equal("DELETE"))
			Expect(requests[0].path).To(Equal("/v1/sys/namespaces/tenant-a"))
			Expect(requests[0].namespace).To(Equal("osac"))
		})

		It("tolerates not-found errors", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"errors":["no namespace"]}`))
			})

			err := client.DeleteTenantNamespace(ctx, "tenant-a")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns error on server failure", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"errors":["internal error"]}`))
			})

			err := client.DeleteTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete namespace"))
		})

		It("returns error on permission denied", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"errors":["permission denied"]}`))
			})

			err := client.DeleteTenantNamespace(ctx, "tenant-a")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to delete namespace"))
		})

		It("rejects invalid tenant names", func() {
			client, _ := newTestLifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			})

			err := client.DeleteTenantNamespace(ctx, "../escape")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid characters"))
		})
	})

	Describe("isAlreadyExistsError", func() {
		It("returns true for 'already exists' in error body", func() {
			err := generateResponseError(http.StatusBadRequest, "namespace already exists")
			Expect(isAlreadyExistsError(err)).To(BeTrue())
		})

		It("returns true for 'existing mount' in error body", func() {
			err := generateResponseError(http.StatusBadRequest, "existing mount at secret/")
			Expect(isAlreadyExistsError(err)).To(BeTrue())
		})

		It("returns true for 'path is already in use' in error body", func() {
			err := generateResponseError(http.StatusBadRequest, "path is already in use at secret/")
			Expect(isAlreadyExistsError(err)).To(BeTrue())
		})

		It("returns false for non-400 status", func() {
			err := generateResponseError(http.StatusInternalServerError, "already exists")
			Expect(isAlreadyExistsError(err)).To(BeFalse())
		})

		It("returns false for 400 without exists message", func() {
			err := generateResponseError(http.StatusBadRequest, "invalid request")
			Expect(isAlreadyExistsError(err)).To(BeFalse())
		})

		It("returns false for non-vault errors", func() {
			Expect(isAlreadyExistsError(io.EOF)).To(BeFalse())
		})
	})

	Describe("isNotFoundError", func() {
		It("returns true for 404 status", func() {
			err := generateResponseError(http.StatusNotFound, "no namespace")
			Expect(isNotFoundError(err)).To(BeTrue())
		})

		It("returns false for non-404 status", func() {
			err := generateResponseError(http.StatusInternalServerError, "not found")
			Expect(isNotFoundError(err)).To(BeFalse())
		})

		It("returns false for non-vault errors", func() {
			Expect(isNotFoundError(io.EOF)).To(BeFalse())
		})
	})
})

func generateResponseError(statusCode int, message string) error {
	return &vaultapi.ResponseError{
		StatusCode: statusCode,
		Errors:     []string{message},
	}
}
