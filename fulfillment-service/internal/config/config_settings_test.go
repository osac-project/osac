/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/zalando/go-keyring"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/oauth"
)

var _ = Describe("Settings", func() {
	var tmp string

	BeforeEach(func() {
		var err error

		// Create a temporary directory:
		tmp, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmp)
	})

	Describe("Builder", func() {
		It("Fails if logger is nil", func() {
			settings, err := NewSettings().
				SetDir(tmp).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(settings).To(BeNil())
		})

		It("Fails if directory is empty", func() {
			settings, err := NewSettings().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(settings).To(BeNil())
		})
	})

	Describe("Common behavior", func() {
		BeforeEach(func() {
			keyring.MockInit()
		})

		It("Loads general settings from the config file", func(ctx context.Context) {
			// Create the config file:
			file := filepath.Join(tmp, "config.json")
			content := []byte(`{
				"address": "api.example.com:443",
				"insecure": true,
				"flow": "code",
				"issuer": "https://example.com",
				"redirect_uri": "https://example.com/callback",
				"scopes": ["openid"],
				"private": true
			}`)
			err := os.WriteFile(file, content, 0600)
			Expect(err).ToNot(HaveOccurred())

			// Load the settings:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = settings.Load(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify the settings:
			Expect(settings.Address()).To(Equal("api.example.com:443"))
			Expect(settings.Insecure()).To(BeTrue())
			Expect(settings.Flow()).To(Equal(oauth.CodeFlow))
			Expect(settings.Issuer()).To(Equal("https://example.com"))
			Expect(settings.RedirectUri()).To(Equal("https://example.com/callback"))
			Expect(settings.Scopes()).To(Equal([]string{"openid"}))
			Expect(settings.Private()).To(BeTrue())
		})

		It("Saves general settings in the config file", func(ctx context.Context) {
			// Create the settings and save them:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetAddress("api.example.com:443")
			settings.SetInsecure(true)
			settings.SetPrivate(true)
			settings.SetFlow(oauth.CodeFlow)
			settings.SetRedirectUri("https://example.com/callback")
			settings.SetScopes([]string{"openid"})
			settings.SetIssuer("https://example.com")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the config file contains the expected data:
			file := filepath.Join(tmp, "config.json")
			content, err := os.ReadFile(file)
			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(MatchJSON(`{
				"address": "api.example.com:443",
				"insecure": true,
				"flow": "code",
				"issuer": "https://example.com",
				"redirect_uri": "https://example.com/callback",
				"scopes": ["openid"],
				"private": true
			}`))
		})

		It("Returns empty settings when no file exists", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = settings.Load(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(settings.Address()).To(BeEmpty())
			Expect(settings.Insecure()).To(BeFalse())
			Expect(settings.Flow()).To(Equal(oauth.Flow("")))
			Expect(settings.Issuer()).To(BeEmpty())
			Expect(settings.RedirectUri()).To(BeEmpty())
			Expect(settings.Scopes()).To(BeEmpty())
			Expect(settings.Private()).To(BeFalse())
		})

		It("Returns nil token when no access token is present", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			store := settings.TokenStore()
			token, err := store.Load(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(BeNil())
		})

		It("Skips save when tokens have not changed", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetAccessToken("access-abc")
			settings.SetRefreshToken("refresh-xyz")
			settings.SetTokenExpiry(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

			store := settings.TokenStore()
			err = store.Save(ctx, &auth.Token{
				Access:  "access-abc",
				Refresh: "refresh-xyz",
				Expiry:  time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
			})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("Keyring available", func() {
		BeforeEach(func() {
			keyring.MockInit()
		})

		It("Saves secrets in the keyring, not in the config file", func(ctx context.Context) {
			// Create the settings and save them:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetTokenExpiry(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
			settings.SetClientId("my-client")
			settings.SetClientSecret("my-secret")
			settings.SetUser("my-user")
			settings.SetPassword("my-password")
			settings.SetAccessToken("my-access-token")
			settings.SetRefreshToken("my-refresh-token")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the keyring contains the expected data under the scoped key:
			data, err := keyring.Get("osac", "secrets:"+tmp)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(MatchJSON(`{
				"access_token": "my-access-token",
				"refresh_token": "my-refresh-token",
				"token_expiry": "2026-06-01T12:00:00Z",
				"client_id": "my-client",
				"client_secret": "my-secret",
				"user": "my-user",
				"password": "my-password"
			}`))

			// Verify that the settings file is empty:
			file := filepath.Join(tmp, "config.json")
			content, err := os.ReadFile(file)
			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(MatchJSON(`{}`))
		})

		It("Persists tokens when saving through the token store", func(ctx context.Context) {
			// Create the settings and save them:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetAddress("api.example.com:443")
			Expect(settings.Save(ctx)).To(Succeed())

			// Save a token:
			store := settings.TokenStore()
			err = store.Save(ctx, &auth.Token{
				Access:  "new-access",
				Refresh: "new-refresh",
				Expiry:  time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify that the keyring contains the expected data under the scoped key:
			data, err := keyring.Get("osac", "secrets:"+tmp)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(MatchJSON(`{
				"access_token": "new-access",
				"refresh_token": "new-refresh",
				"token_expiry": "2026-07-01T12:00:00Z"
			}`))
		})
	})

	When("Keyring is not available", func() {
		BeforeEach(func() {
			keyring.MockInitWithError(fmt.Errorf("keyring backend not available"))
		})

		It("Saves secrets in the secrets file, not in the keyring", func(ctx context.Context) {
			// Create the settings and save them:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetTokenExpiry(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
			settings.SetClientId("my-client")
			settings.SetClientSecret("my-secret")
			settings.SetUser("my-user")
			settings.SetPassword("my-password")
			settings.SetAccessToken("my-access-token")
			settings.SetRefreshToken("my-refresh-token")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the secrets file contains the expected data:
			file := filepath.Join(tmp, "secrets.json")
			content, err := os.ReadFile(file)
			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(MatchJSON(`{
				"access_token": "my-access-token",
				"refresh_token": "my-refresh-token",
				"token_expiry": "2026-06-01T12:00:00Z",
				"client_id": "my-client",
				"client_secret": "my-secret",
				"user": "my-user",
				"password": "my-password"
			}`))
		})

		It("Persists tokens when saving through the token store", func(ctx context.Context) {
			// Create the settings and save them:
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetAddress("api.example.com:443")
			Expect(settings.Save(ctx)).To(Succeed())

			// Save a token:
			store := settings.TokenStore()
			err = store.Save(ctx, &auth.Token{
				Access:  "new-access",
				Refresh: "new-refresh",
				Expiry:  time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify that the secrets file contains the expected data:
			file := filepath.Join(tmp, "secrets.json")
			content, err := os.ReadFile(file)
			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(MatchJSON(`{
				"access_token": "new-access",
				"refresh_token": "new-refresh",
				"token_expiry": "2026-07-01T12:00:00Z"
			}`))
		})
	})

	Describe("CA pool creation", func() {
		var pemBytes []byte

		BeforeEach(func() {
			keyring.MockInit()

			// Generate a test CA certificate:
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			Expect(err).ToNot(HaveOccurred())
			now := time.Now()
			template := &x509.Certificate{
				SerialNumber:          big.NewInt(1),
				NotBefore:             now.Add(-time.Hour),
				NotAfter:              now.Add(time.Hour),
				IsCA:                  true,
				BasicConstraintsValid: true,
			}
			derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			Expect(err).ToNot(HaveOccurred())
			pemBytes = pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: derBytes,
			})
		})

		It("Ignores entries with relative paths", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name:    "relative/path/ca.pem",
				Content: string(pemBytes),
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Reads file from disk when absolute path and content are both set", func(ctx context.Context) {
			pemFile := filepath.Join(tmp, "ca.pem")
			err := os.WriteFile(pemFile, pemBytes, 0600)
			Expect(err).ToNot(HaveOccurred())
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name:    pemFile,
				Content: string(pemBytes),
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Falls back to stored content when file is not readable", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name:    "/nonexistent/ca.pem",
				Content: string(pemBytes),
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Adds CA files when absolute path without content is a directory", func(ctx context.Context) {
			caDir := filepath.Join(tmp, "ca")
			err := os.MkdirAll(caDir, 0755)
			Expect(err).ToNot(HaveOccurred())
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name: caDir,
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Ignores CA files when absolute path without content does not exist", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name: "/nonexistent/directory",
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})

		It("Ignores absolute path without content that is a regular file", func(ctx context.Context) {
			pemFile := filepath.Join(tmp, "not-a-dir.pem")
			err := os.WriteFile(pemFile, pemBytes, 0600)
			Expect(err).ToNot(HaveOccurred())
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.AddCaFile(CaFile{
				Name: pemFile,
			})
			pool, err := settings.CaPool(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(pool).ToNot(BeNil())
		})
	})

	Describe("Tenant", func() {
		BeforeEach(func() {
			keyring.MockInit()
		})

		It("Returns empty string when no tenant is saved", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = settings.Load(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(settings.Tenant()).To(BeEmpty())
		})

		It("Round-trips the tenant through the config file", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetTenant("my-tenant")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			settings2, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = settings2.Load(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(settings2.Tenant()).To(Equal("my-tenant"))
		})

		It("Clears the tenant when set to empty string", func(ctx context.Context) {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetTenant("my-tenant")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			settings.SetTenant("")
			err = settings.Save(ctx)
			Expect(err).ToNot(HaveOccurred())

			settings2, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = settings2.Load(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(settings2.Tenant()).To(BeEmpty())
		})
	})

	Describe("Armed check", func() {
		It("Returns false when settings are nil", func() {
			var settings *Settings
			Expect(settings.Armed()).To(BeFalse())
		})

		It("Returns true when the settings are armed", func() {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetAddress("api.example.com:443")
			Expect(settings.Armed()).To(BeTrue())
		})

		It("Returns false when the settings are not armed", func() {
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(settings.Armed()).To(BeFalse())
		})
	})
})
