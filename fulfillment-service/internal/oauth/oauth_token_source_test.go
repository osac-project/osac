/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package oauth

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/ghttp"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

var _ = Describe("Token source", func() {
	var (
		ctx    context.Context
		ctrl   *gomock.Controller
		store  auth.TokenStore
		server *Server
		caPool *trust.CertPool
	)

	BeforeEach(func() {
		var err error

		// Create the context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Create an empty token store:
		store, err = auth.NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the TLS server that responds to the discovery requests any number of times. Other responses will
		// be added in specific tests.
		var caFile string
		server, caFile = testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:        server.URL(),
					TokenEndpoint: fmt.Sprintf("%s/token", server.URL()),
				},
				http.Header{
					"Content-Type": {
						"application/json",
					},
				},
			),
		)

		// Create CA pool with the server's certificate
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool = loadedCaPool

	})

	It("Can be created with all the mandatory parameters", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(source).ToNot(BeNil())
	})

	It("Can be created with optional scopes", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetScopes("read", "write").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(source).ToNot(BeNil())
	})

	It("Can be created with insecure TLS option", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetInsecure(true).
			SetStore(store).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(source).ToNot(BeNil())
	})

	It("Can be created with CA pool for TLS", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(source).ToNot(BeNil())
	})

	It("Can't be created without a logger", func() {
		source, err := NewTokenSource().
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).To(MatchError("logger is mandatory"))
		Expect(source).To(BeNil())
	})

	It("Can't be created without an issuer", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).To(MatchError("issuer is mandatory"))
		Expect(source).To(BeNil())
	})

	It("Can't be created without a client identifier for the client credentials flow", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).To(MatchError("client identifier is mandatory"))
		Expect(source).To(BeNil())
	})

	It("Can't be created without a client secret for client credentials flow", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetCaPool(caPool).
			SetStore(store).
			Build()
		Expect(err).To(MatchError("client secret is mandatory for the client credentials flow"))
		Expect(source).To(BeNil())
	})

	Describe("File-backed client credentials", func() {
		var (
			path    string
			builder *TokenSourceBuilder
			logs    bytes.Buffer
		)

		BeforeEach(func() {
			path = filepath.Join(GinkgoT().TempDir(), "client-secret")
			logs.Reset()
			builder = NewTokenSource().
				SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))).
				SetIssuer(server.URL()).
				SetFlow(CredentialsFlow).
				SetClientId("my_client").
				SetClientSecretFile(path).
				SetCaPool(caPool).
				SetStore(store)
		})

		It("rejects configuring both an inline secret and a secret file", func() {
			_, err := builder.SetClientSecret("inline").Build()
			Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
		})

		It("rejects file-backed credentials in other flows", func() {
			_, err := builder.SetFlow(DeviceFlow).Build()
			Expect(err).To(MatchError(ContainSubstring("only supported for the client credentials flow")))
		})

		It("uses fresh cached tokens and reads the replaced file on the next grant", func() {
			source, err := builder.Build()
			Expect(err).NotTo(HaveOccurred())

			var grants atomic.Int32
			server.RouteToHandler(http.MethodPost, "/token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				switch grants.Add(1) {
				case 1:
					Expect(r.FormValue("client_secret")).To(Equal("initial-value"))
				case 2:
					Expect(r.FormValue("client_secret")).To(Equal("rotated-value"))
				default:
					Fail("unexpected token grant")
				}
				Expect(r.FormValue("grant_type")).To(Equal("client_credentials"))
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `{"access_token":"access","expires_in":3600}`)
				Expect(err).NotTo(HaveOccurred())
			}))

			Expect(os.WriteFile(path, []byte("initial-value\n"), 0o600)).To(Succeed())
			token, err := source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Access).To(Equal("access"))

			Expect(os.Remove(path)).To(Succeed())
			token, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Access).To(Equal("access"))
			Expect(grants.Load()).To(Equal(int32(1)))

			replacement := path + ".next"
			Expect(os.WriteFile(replacement, []byte("rotated-value"), 0o600)).To(Succeed())
			Expect(os.Rename(replacement, path)).To(Succeed())
			Expect(source.Invalidate(ctx)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(grants.Load()).To(Equal(int32(2)))
		})

		It("does not send a grant for an unavailable or blank file and recovers after repair", func() {
			source, err := builder.Build()
			Expect(err).NotTo(HaveOccurred())
			var grants atomic.Int32
			server.RouteToHandler(http.MethodPost, "/token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				grants.Add(1)
				Expect(r.FormValue("client_secret")).To(Equal("repaired-value"))
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `{"access_token":"access","expires_in":3600}`)
				Expect(err).NotTo(HaveOccurred())
			}))

			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("failed to read client secret file")))
			Expect(os.Mkdir(path, 0o700)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("failed to read client secret file")))
			Expect(os.Remove(path)).To(Succeed())
			Expect(os.WriteFile(path, []byte(" \r\n\t"), 0o600)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("client secret file is empty")))
			Expect(grants.Load()).To(BeZero())

			Expect(os.WriteFile(path, []byte("repaired-value"), 0o600)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(grants.Load()).To(Equal(int32(1)))
		})

		It("rejects a non-regular or oversized secret file without sending a grant", func() {
			source, err := builder.Build()
			Expect(err).NotTo(HaveOccurred())
			var grants atomic.Int32
			server.RouteToHandler(http.MethodPost, "/token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				grants.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `{"access_token":"access","expires_in":3600}`)
				Expect(err).NotTo(HaveOccurred())
			}))

			Expect(os.Mkdir(path, 0o700)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("not a regular file")))
			Expect(os.Remove(path)).To(Succeed())

			Expect(syscall.Mkfifo(path, 0o600)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("not a regular file")))
			Expect(os.Remove(path)).To(Succeed())

			Expect(os.WriteFile(path, make([]byte, maxClientSecretFileSize+1), 0o600)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("exceeds the maximum client secret file size")))
			Expect(grants.Load()).To(BeZero())
		})

		It("reads a Kubernetes projected secret symlink and picks up its rotation", func() {
			dir := GinkgoT().TempDir()
			linkPath := filepath.Join(dir, "client-secret")

			writeVersion := func(name, value string) {
				versionDir := filepath.Join(dir, name)
				Expect(os.Mkdir(versionDir, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(versionDir, "client-secret"), []byte(value), 0o600)).To(Succeed())
			}
			// Kubernetes projects a Secret as "client-secret -> ..data/client-secret"
			// and "..data -> ..<timestamp>", so the reader must follow both links.
			writeVersion("..2026_11_27_15_00_00.000000000", "initial-value")
			Expect(os.Symlink("..2026_11_27_15_00_00.000000000", filepath.Join(dir, "..data"))).To(Succeed())
			Expect(os.Symlink(filepath.Join("..data", "client-secret"), linkPath)).To(Succeed())

			source, err := builder.SetClientSecretFile(linkPath).Build()
			Expect(err).NotTo(HaveOccurred())

			var grants atomic.Int32
			server.RouteToHandler(http.MethodPost, "/token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				switch grants.Add(1) {
				case 1:
					Expect(r.FormValue("client_secret")).To(Equal("initial-value"))
				case 2:
					Expect(r.FormValue("client_secret")).To(Equal("rotated-value"))
				default:
					Fail("unexpected token grant")
				}
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `{"access_token":"access","expires_in":3600}`)
				Expect(err).NotTo(HaveOccurred())
			}))

			_, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Rotation replaces the ..data symlink atomically; the visible file
			// link stays in place.
			writeVersion("..2026_11_27_15_05_00.000000000", "rotated-value")
			Expect(os.Symlink("..2026_11_27_15_05_00.000000000", filepath.Join(dir, "..data_tmp"))).To(Succeed())
			Expect(os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data"))).To(Succeed())
			Expect(source.Invalidate(ctx)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(grants.Load()).To(Equal(int32(2)))
		})

		It("sanitizes transport timeouts and unreadable success responses", func() {
			const dummy = "file-credential-test-value"
			Expect(os.WriteFile(path, []byte(dummy), 0o600)).To(Succeed())

			server.RouteToHandler(http.MethodPost, "/token", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(500 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"access","expires_in":3600}`))
			}))
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{RootCAs: caPool.Pool()}
			source, err := builder.SetHttpClient(&http.Client{
				Timeout:   100 * time.Millisecond,
				Transport: transport,
			}).Build()
			Expect(err).NotTo(HaveOccurred())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("token endpoint request timed out")))
			Expect(err.Error()).NotTo(ContainSubstring(dummy))

			server.RouteToHandler(http.MethodPost, "/token", RespondWith(http.StatusOK, `{"access_token":`,
				http.Header{"Content-Type": {"application/json"}}))
			Expect(source.Invalidate(ctx)).To(Succeed())
			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("token endpoint returned an invalid response")))
			Expect(err.Error()).NotTo(ContainSubstring(dummy))
			Expect(logs.String()).NotTo(ContainSubstring(dummy))
		})

		It("does not log or return a credential echoed by the token endpoint", func() {
			const dummy = "file-credential-test-value"
			Expect(os.WriteFile(path, []byte(dummy), 0o600)).To(Succeed())
			server.RouteToHandler(http.MethodPost, "/token", RespondWithJSONEncoded(http.StatusBadRequest, endpointError{
				ErrorCode:        "invalid_client",
				ErrorDescription: "echoed credential: " + dummy,
			}, http.Header{"Content-Type": {"application/json"}}))
			source, err := builder.Build()
			Expect(err).NotTo(HaveOccurred())

			_, err = source.Token(ctx)
			Expect(err).To(MatchError(ContainSubstring("HTTP 400")))
			Expect(err.Error()).NotTo(ContainSubstring(dummy))
			Expect(logs.String()).NotTo(ContainSubstring(dummy))
		})
	})

	It("Can't be created without a token store", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).To(MatchError("token store is mandatory"))
		Expect(source).To(BeNil())
	})

	It("Can't be created without a listener in interactive mode and with the code flow", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(CodeFlow).
			SetInteractive(true).
			SetClientId("my_client").
			SetCaPool(caPool).
			Build()
		Expect(err).To(MatchError("listener is mandatory for the authorization code flow"))
		Expect(source).To(BeNil())
	})

	It("Can't be created without a listener in interactive mode and with the device flow", func() {
		source, err := NewTokenSource().
			SetLogger(logger).
			SetStore(store).
			SetIssuer(server.URL()).
			SetFlow(DeviceFlow).
			SetInteractive(true).
			SetClientId("my_client").
			SetCaPool(caPool).
			Build()
		Expect(err).To(MatchError("listener is mandatory for the device flow"))
		Expect(source).To(BeNil())
	})

	It("Uses the token loaded from storage if it is still fresh", func() {
		// Prepare the store with a valid token:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(1 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the one loaded from the store:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_access_token"))
	})

	It("Retries endpoint discovery after a transient failure", func() {
		oauthDiscoveryRequests := 0
		openidDiscoveryRequests := 0
		var tokenRequests []string

		server.RouteToHandler(
			http.MethodGet,
			"/retry/.well-known/oauth-authorization-server",
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				oauthDiscoveryRequests++
				if oauthDiscoveryRequests == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := fmt.Fprintf(
					w,
					`{"issuer":%q,"token_endpoint":%q}`,
					server.URL()+"/retry",
					server.URL()+"/token",
				)
				Expect(err).To(Succeed())
			}),
		)
		server.RouteToHandler(
			http.MethodGet,
			"/retry/.well-known/openid-configuration",
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				openidDiscoveryRequests++
				w.WriteHeader(http.StatusServiceUnavailable)
			}),
		)
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokenRequests = append(tokenRequests, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := fmt.Fprint(w, `{"access_token":"my_access_token","token_type":"Bearer","expires_in":3600}`)
				Expect(err).To(Succeed())
			}),
		)

		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL() + "/retry").
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = source.Token(ctx)
		Expect(err).To(HaveOccurred())

		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token.Access).To(Equal("my_access_token"))
		Expect(oauthDiscoveryRequests).To(Equal(2))
		Expect(openidDiscoveryRequests).To(Equal(1))
		Expect(tokenRequests).To(Equal([]string{"/token"}))
	})

	It("Refreshes the access token it is expired", func() {
		// Prepare the server so that it responds to the token refresh request with a valid token:
		server.AppendHandlers(
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("client_id", "my_client"),
				VerifyFormKV("grant_type", "refresh_token"),
				VerifyFormKV("refresh_token", "my_refresh_token"),
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token":  "my_new_access_token",
						"refresh_token": "my_new_refresh_token",
						"token_type":    "Bearer",
						"expires_in":    3600,
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),
		)
		// Prepare the store with a token that is expired:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(-1 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the one returned by the server:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_new_access_token"))
		Expect(token.Refresh).To(Equal("my_new_refresh_token"))
		Expect(token.Expiry).To(BeTemporally("~", time.Now().Add(3600*time.Second), time.Second))
	})

	It("Refreshes the access token ins't expired yet, but about to expire", func() {
		// Prepare the server so that it responds to the token refresh request with a valid token:
		server.AppendHandlers(
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("client_id", "my_client"),
				VerifyFormKV("grant_type", "refresh_token"),
				VerifyFormKV("refresh_token", "my_refresh_token"),
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token":  "my_new_access_token",
						"refresh_token": "my_new_refresh_token",
						"token_type":    "Bearer",
						"expires_in":    3600,
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),
		)

		// Prepare the store with a token that is about to expire:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(1 * time.Second),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the one returned by the server:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_new_access_token"))
		Expect(token.Refresh).To(Equal("my_new_refresh_token"))
		Expect(token.Expiry).To(BeTemporally("~", time.Now().Add(3600*time.Second), time.Second))
	})

	It("Saves the refreshed access and refresh tokens", func() {
		// Prepare the server so that it responds to the token refresh request with a valid token:
		server.AppendHandlers(
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("grant_type", "refresh_token"),
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token":  "my_new_access_token",
						"refresh_token": "my_new_refresh_token",
						"token_type":    "Bearer",
						"expires_in":    3600,
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),
		)

		// Prepare the store with a token that is expired:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(-1 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the new access and refresh tokens were saved:
		token, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_new_access_token"))
		Expect(token.Refresh).To(Equal("my_new_refresh_token"))
		Expect(token.Expiry).To(BeTemporally("~", time.Now().Add(3600*time.Second), time.Second))
	})

	It("Preserves the refrest token if the server doesn't return a new one", func() {
		// Prepare the server so that it responds to the token refresh request with a valid access token, but
		// without a new refresh token:
		server.AppendHandlers(
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("grant_type", "refresh_token"),
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token": "my_new_access_token",
						"token_type":   "Bearer",
						"expires_in":   3600,
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),
		)

		// Prepare the store with a token that is expired:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(-1 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the old refresh token was preserved:
		token, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).ToNot(BeNil())
		Expect(token.Refresh).To(Equal("my_refresh_token"))
	})

	It("Requests a new access token if it is expired and there is no refresh token", func() {
		// Prepare the server so that it responds to the request to refresh the token with an error, and to
		// the request to generate a new token with a valid token.
		server.AppendHandlers(

			// First request should be to refresh the token, and this should fail:
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("grant_type", "refresh_token"),
				VerifyFormKV("refresh_token", "my_refresh_token"),
				VerifyFormKV("client_id", "my_client"),
				RespondWithJSONEncoded(
					http.StatusBadRequest,
					map[string]any{
						"error":             "invalid_grant",
						"error_description": "The refresh token is invalid or expired",
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),

			// Second request should be to generate a new token:
			CombineHandlers(
				VerifyRequest(http.MethodPost, "/token"),
				VerifyContentType("application/x-www-form-urlencoded"),
				VerifyFormKV("grant_type", "client_credentials"),
				VerifyFormKV("client_id", "my_client"),
				VerifyFormKV("client_secret", "my_secret"),
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token":  "my_new_access_token",
						"refresh_token": "my_new_refresh_token",
						"token_type":    "Bearer",
						"expires_in":    3600,
					},
					http.Header{
						"Content-Type": {
							"application/json",
						},
					},
				),
			),
		)

		// Prepare the store with a token that is expired:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(-1 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the one returned by the server:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_new_access_token"))
		Expect(token.Refresh).To(Equal("my_new_refresh_token"))
	})

	It("It uses the access token that isn't fresh, but not expired, if there is no alternative", func() {
		// Prepare the store with a token that isn't fresh (expires in less than 30 seconds) but not
		// expired yet, and no refresh token.
		err := store.Save(ctx, &auth.Token{
			Access: "my_access_token",
			Expiry: time.Now().Add(10 * time.Second),
		})
		Expect(err).ToNot(HaveOccurred())

		// Create the source with a flow that can't be used because it is interactive and interactive mode is
		// disabled. This should force it to use the token from the store.
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CodeFlow).
			SetInteractive(false).
			SetClientId("my_client").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token:
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the old one:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_access_token"))
	})

	It("Doesn't perform discovery when there is already a valid token", func() {
		// Prepare the store with a fresh token that won't expire soon:
		err := store.Save(ctx, &auth.Token{
			Access:  "my_fresh_access_token",
			Refresh: "my_refresh_token",
			Expiry:  time.Now().Add(2 * time.Hour),
		})
		Expect(err).ToNot(HaveOccurred())

		// Replace the server with a new one that doesn't respond to discovery requests, so if discovery is
		// attempted, it will fail with a 404.
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})

		// Create CA pool with the discovery server's certificate
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool

		// Create the server that doesn't respond to discovery requests:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request the token. This should use the fresh token from storage without performing discovery.
		token, err := source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the token is the fresh one from storage:
		Expect(token).ToNot(BeNil())
		Expect(token.Access).To(Equal("my_fresh_access_token"))
		Expect(token.Refresh).To(Equal("my_refresh_token"))
		Expect(token.Expiry).To(BeTemporally("~", time.Now().Add(2*time.Hour), time.Second))

		// Verify that no requests were made to the discovery server by checking that no handlers were called:
		Expect(server.ReceivedRequests()).To(BeEmpty())
	})

	It("Performs discovery only once across multiple token requests", func() {
		// Replace the server with one that responds once to discovery, and then as many times as needed to
		// the token request. This will fail if used more than once for discovery.
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		count := 0
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			func(w http.ResponseWriter, r *http.Request) {
				Expect(count).To(BeZero())
				RespondWithJSONEncoded(
					http.StatusOK,
					&ServerMetadata{
						Issuer:        server.URL(),
						TokenEndpoint: fmt.Sprintf("%s/token", server.URL()),
					},
				)(w, r)
				count++
			},
		)
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			RespondWithJSONEncoded(
				http.StatusOK,
				map[string]any{
					"access_token": "my_first_access_token",
					"token_type":   "Bearer",
					"expires_in":   3600,
				},
			),
		)

		// Create a mock token store that returns no token the first time and an expired token the second time.
		store := auth.NewMockTokenStore(ctrl)
		store.EXPECT().Save(gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes()
		store.EXPECT().Load(gomock.Any()).
			Return(nil, nil).
			Times(1)
		store.EXPECT().Load(gomock.Any()).
			Return(
				&auth.Token{
					Access: "my_expired_token",
					Expiry: time.Now().Add(-1 * time.Hour),
				},
				nil,
			).
			Times(1)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// First token request, should trigger discovery and request a new token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Second token request, should use the expired token and therefore request
		// another token, but should not trigger discovery:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that discovery was called exactly once:
		Expect(count).To(Equal(1))
	})

	It("Uses all default scopes when server supports them all", func() {
		// Create a server that supports all default scopes:
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:          server.URL(),
					TokenEndpoint:   fmt.Sprintf("%s/token", server.URL()),
					ScopesSupported: []string{"openid", "profile", "email", "other"},
				},
			),
		)

		// Capture the scopes sent to the token endpoint:
		var requestedScopes string
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			func(w http.ResponseWriter, r *http.Request) {
				err := r.ParseForm()
				Expect(err).ToNot(HaveOccurred())
				requestedScopes = r.FormValue("scope")
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token": "my_access_token",
						"token_type":   "Bearer",
						"expires_in":   3600,
					},
				)(w, r)
			},
		)

		// Create the source without setting scopes:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request a token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that all default scopes were requested:
		Expect(requestedScopes).To(Equal("openid"))
	})

	It("Uses only the scopes supported by the server from defaults", func() {
		// Create a server that supports only some of the default scopes:
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:          server.URL(),
					TokenEndpoint:   fmt.Sprintf("%s/token", server.URL()),
					ScopesSupported: []string{"openid", "other"},
				},
			),
		)

		// Capture the scopes sent to the token endpoint:
		var requestedScopes string
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			func(w http.ResponseWriter, r *http.Request) {
				err := r.ParseForm()
				Expect(err).ToNot(HaveOccurred())
				requestedScopes = r.FormValue("scope")
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token": "my_access_token",
						"token_type":   "Bearer",
						"expires_in":   3600,
					},
				)(w, r)
			},
		)

		// Create the source without setting scopes:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request a token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that only the supported scopes were requested:
		Expect(requestedScopes).To(Equal("openid"))
	})

	It("Uses explicitly configured scopes instead of defaults", func() {
		// Create a server that supports all default scopes:
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:          server.URL(),
					TokenEndpoint:   fmt.Sprintf("%s/token", server.URL()),
					ScopesSupported: []string{"openid", "profile", "email", "custom"},
				},
			),
		)

		// Capture the scopes sent to the token endpoint:
		var requestedScopes string
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			func(w http.ResponseWriter, r *http.Request) {
				err := r.ParseForm()
				Expect(err).ToNot(HaveOccurred())
				requestedScopes = r.FormValue("scope")
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token": "my_access_token",
						"token_type":   "Bearer",
						"expires_in":   3600,
					},
				)(w, r)
			},
		)

		// Create the source with explicit scopes:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetScopes("custom", "openid").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request a token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the explicitly configured scopes were used:
		Expect(requestedScopes).To(Equal("custom openid"))
	})

	It("Doesn't set default scopes when server doesn't advertise supported scopes", func() {
		// Create a server that doesn't return supported scopes:
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:        server.URL(),
					TokenEndpoint: fmt.Sprintf("%s/token", server.URL()),
				},
			),
		)

		// Capture the scopes sent to the token endpoint:
		var requestedScopes string
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			func(w http.ResponseWriter, r *http.Request) {
				err := r.ParseForm()
				Expect(err).ToNot(HaveOccurred())
				requestedScopes = r.FormValue("scope")
				RespondWithJSONEncoded(
					http.StatusOK,
					map[string]any{
						"access_token": "my_access_token",
						"token_type":   "Bearer",
						"expires_in":   3600,
					},
				)(w, r)
			},
		)

		// Create the source without setting scopes:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request a token:
		_, err = source.Token(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that no scopes were requested:
		Expect(requestedScopes).To(BeEmpty())
	})

	It("Fails promptly instead of hanging when the token endpoint never responds", func() {
		// Create a server whose token endpoint accepts the request but never writes a response,
		// simulating a stalled connection to the OAuth server. This isn't reachable using the
		// standard ghttp handlers used by the other tests in this file, which always respond
		// immediately.
		server, caFile := testing.MakeTCPTLSServer()
		DeferCleanup(server.Close)
		DeferCleanup(func() {
			err := os.Remove(caFile)
			Expect(err).ToNot(HaveOccurred())
		})
		loadedCaPool, err := trust.NewCertPool().
			SetLogger(logger).
			AddFile(caFile).
			Build()
		Expect(err).ToNot(HaveOccurred())
		caPool := loadedCaPool
		server.RouteToHandler(
			http.MethodGet,
			"/.well-known/oauth-authorization-server",
			RespondWithJSONEncoded(
				http.StatusOK,
				&ServerMetadata{
					Issuer:        server.URL(),
					TokenEndpoint: fmt.Sprintf("%s/token", server.URL()),
				},
			),
		)
		// release unblocks the handler. Deferred so it always runs — even if the goroutine below
		// never completes — so server.Close() during cleanup doesn't itself block waiting for this
		// handler to return.
		release := make(chan struct{})
		defer close(release)
		server.RouteToHandler(
			http.MethodPost,
			"/token",
			func(w http.ResponseWriter, r *http.Request) {
				<-release
			},
		)

		// Create the source:
		source, err := NewTokenSource().
			SetLogger(logger).
			SetIssuer(server.URL()).
			SetStore(store).
			SetFlow(CredentialsFlow).
			SetClientId("my_client").
			SetClientSecret("my_secret").
			SetCaPool(caPool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Request a token with a context that has a short deadline. If the request honors the
		// context, it fails quickly once the deadline is reached instead of hanging forever. Run it
		// in a goroutine racing a timer, rather than calling it synchronously, so that a regression
		// fails this spec cleanly instead of hanging the whole test binary:
		requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		start := time.Now()
		go func() {
			_, tokenErr := source.Token(requestCtx)
			done <- tokenErr
		}()

		var tokenErr error
		select {
		case tokenErr = <-done:
		case <-time.After(5 * time.Second):
			Fail("Token() should fail promptly once the context deadline is reached, not hang forever")
		}
		elapsed := time.Since(start)

		Expect(tokenErr).To(HaveOccurred())
		Expect(elapsed).To(BeNumerically("<", 5*time.Second),
			"Token() should fail promptly once the context deadline is reached, not hang forever")
	})
})
