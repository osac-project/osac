/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logout

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

var _ = Describe("Logout command flags", func() {
	It("Has the expected use string", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("logout [FLAG...]"))
	})

	It("Has short help text", func() {
		cmd := Cmd()
		Expect(cmd.Short).ToNot(BeEmpty())
	})

	It("Has long help text", func() {
		cmd := Cmd()
		Expect(cmd.Long).ToNot(BeEmpty())
	})
})

var _ = Describe("Logout command execution", func() {
	var (
		ctx    context.Context
		output *bytes.Buffer
		stderr *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctx = logging.LoggerIntoContext(ctx, slog.Default())
		output = &bytes.Buffer{}
		stderr = &bytes.Buffer{}
	})

	setupContext := func() (context.Context, *config.Settings) {
		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(output).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = terminal.ConsoleIntoContext(ctx, console)

		tempDir, err := os.MkdirTemp("", "logout-test-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			os.RemoveAll(tempDir)
		})

		settings, err := config.NewSettings().
			SetLogger(slog.Default()).
			SetDir(filepath.Join(tempDir, "settings")).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ctx = config.SettingsIntoContext(ctx, settings)
		return ctx, settings
	}

	It("Clears credentials when no issuer is configured", func() {
		ctx, settings := setupContext()

		settings.SetAddress("localhost:8000")
		settings.SetAccessToken("my-access-token")
		settings.SetRefreshToken("my-refresh-token")

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Successfully logged out"))
		Expect(settings.Address()).To(BeEmpty())
	})

	It("Clears credentials when no token source is available", func() {
		ctx, settings := setupContext()

		settings.SetAddress("localhost:8000")
		settings.SetIssuer("https://example.com/auth/realms/test")

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Successfully logged out"))
		Expect(settings.Address()).To(BeEmpty())
		Expect(settings.Issuer()).To(BeEmpty())
	})

	It("Clears credentials when access token exists but no refresh token", func() {
		ctx, settings := setupContext()

		settings.SetAddress("localhost:8000")
		settings.SetIssuer("https://example.com/auth/realms/test")
		settings.SetAccessToken("my-access-token")
		settings.SetTokenExpiry(time.Now().Add(1 * time.Hour))

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Successfully logged out"))
		Expect(settings.Address()).To(BeEmpty())
	})

	It("Shows warning and still clears credentials when session termination fails", func() {
		discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		DeferCleanup(discoveryServer.Close)

		ctx, settings := setupContext()

		settings.SetAddress("localhost:8000")
		settings.SetIssuer(discoveryServer.URL)
		settings.SetAccessToken("my-access-token")
		settings.SetRefreshToken("my-refresh-token")
		settings.SetTokenExpiry(time.Now().Add(1 * time.Hour))
		settings.SetFlow("code")
		settings.SetClientId("test-client")
		settings.SetClientSecret("test-secret")
		settings.SetScopes([]string{"openid"})

		err := settings.Save(ctx)
		Expect(err).ToNot(HaveOccurred())
		err = settings.Load(ctx)
		Expect(err).ToNot(HaveOccurred())

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err = cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(stderr.String()).To(ContainSubstring("Warning: Failed to terminate server session"))
		Expect(stderr.String()).To(ContainSubstring("Local credentials will be cleared"))
		Expect(output.String()).To(ContainSubstring("Successfully logged out"))
		Expect(settings.Address()).To(BeEmpty())
	})
})

var _ = Describe("refreshForIdToken", func() {
	var runner *runnerContext

	BeforeEach(func() {
		runner = &runnerContext{}
	})

	It("Returns the ID token on success", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.FormValue("grant_type")).To(Equal("refresh_token"))
			Expect(r.FormValue("refresh_token")).To(Equal("my-refresh-token"))
			Expect(r.FormValue("client_id")).To(Equal("my-client"))
			Expect(r.FormValue("client_secret")).To(Equal("my-secret"))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"id_token": "the-id-token",
			})
		}))
		DeferCleanup(server.Close)

		idToken, err := runner.refreshForIdToken(
			context.Background(), server.Client(), server.URL,
			"my-refresh-token", "my-client", "my-secret",
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(idToken).To(Equal("the-id-token"))
	})

	It("Omits client_id when empty", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.FormValue("client_id")).To(BeEmpty())
			Expect(r.FormValue("client_secret")).To(BeEmpty())

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"id_token": "token-no-client",
			})
		}))
		DeferCleanup(server.Close)

		idToken, err := runner.refreshForIdToken(
			context.Background(), server.Client(), server.URL,
			"my-refresh-token", "", "",
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(idToken).To(Equal("token-no-client"))
	})

	It("Returns error when token endpoint returns non-200 status", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		DeferCleanup(server.Close)

		_, err := runner.refreshForIdToken(
			context.Background(), server.Client(), server.URL,
			"bad-token", "my-client", "my-secret",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("error status"))
	})

	It("Returns error when response does not contain id_token", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"access_token": "some-access-token",
			})
		}))
		DeferCleanup(server.Close)

		_, err := runner.refreshForIdToken(
			context.Background(), server.Client(), server.URL,
			"my-refresh-token", "my-client", "my-secret",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not return an ID token"))
	})

	It("Returns error when response body is invalid JSON", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("not-json"))
		}))
		DeferCleanup(server.Close)

		_, err := runner.refreshForIdToken(
			context.Background(), server.Client(), server.URL,
			"my-refresh-token", "my-client", "my-secret",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to parse token response"))
	})

	It("Returns error when token endpoint is unreachable", func() {
		_, err := runner.refreshForIdToken(
			context.Background(), http.DefaultClient, "http://127.0.0.1:1",
			"my-refresh-token", "my-client", "my-secret",
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to call token endpoint"))
	})
})

var _ = Describe("httpClient", func() {
	var runner *runnerContext

	BeforeEach(func() {
		runner = &runnerContext{}
	})

	It("Creates a client with default TLS config", func() {
		client := runner.httpClient(nil, false)
		Expect(client).ToNot(BeNil())
		transport := client.Transport.(*http.Transport)
		Expect(transport.TLSClientConfig).ToNot(BeNil())
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeFalse())
	})

	It("Creates a client with insecure TLS config", func() {
		client := runner.httpClient(nil, true)
		Expect(client).ToNot(BeNil())
		transport := client.Transport.(*http.Transport)
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
	})
})
