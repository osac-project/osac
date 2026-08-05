/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package whoami

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

var _ = Describe("Whoami command flags", func() {
	It("Has the expected use string", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("whoami [FLAG...]"))
	})

	It("Has a --show-token flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("show-token")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("t"))
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Has a --show-token-claims flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("show-token-claims")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("c"))
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Has a --rfc-3339 flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("rfc-3339")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("R"))
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Has a --utc flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("utc")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("U"))
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Accepts no arguments", func() {
		cmd := Cmd()
		Expect(cmd.Args).ToNot(BeNil())
	})
})

var _ = Describe("Whoami command execution", func() {
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

	createTestToken := func(username, organization string, roles []string) string {
		claims := jwt.MapClaims{
			"username": username,
			"iat":      time.Now().Unix(),
			"exp":      time.Now().Add(1 * time.Hour).Unix(),
		}
		if organization != "" {
			claims["organization"] = map[string]interface{}{
				organization: map[string]interface{}{},
			}
		}
		if len(roles) > 0 {
			claims["realm_access"] = map[string]interface{}{
				"roles": roles,
			}
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, _ := token.SignedString([]byte("test-secret"))
		return tokenString
	}

	setupContext := func(tokenString string) context.Context {
		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(output).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = terminal.ConsoleIntoContext(ctx, console)

		// Create a temporary directory for settings
		tempDir, err := os.MkdirTemp("", "whoami-test-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			os.RemoveAll(tempDir)
		})

		// Create settings with a valid token
		settings, err := config.NewSettings().
			SetLogger(slog.Default()).
			SetDir(filepath.Join(tempDir, "settings")).
			Build()
		Expect(err).ToNot(HaveOccurred())

		settings.SetAddress("localhost:8000")
		settings.SetAccessToken(tokenString)
		settings.SetRefreshToken("refresh-token")
		settings.SetTokenExpiry(time.Now().Add(1 * time.Hour))

		ctx = config.SettingsIntoContext(ctx, settings)
		return ctx
	}

	It("Shows error when not logged in", func() {
		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(output).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = terminal.ConsoleIntoContext(ctx, console)

		// Create a temporary directory for settings
		tempDir, err := os.MkdirTemp("", "whoami-test-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			os.RemoveAll(tempDir)
		})

		// Settings with no auth token
		settings, err := config.NewSettings().
			SetLogger(slog.Default()).
			SetDir(filepath.Join(tempDir, "settings")).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ctx = config.SettingsIntoContext(ctx, settings)

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err = cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(stderr.String()).To(ContainSubstring("Not logged in"))
		Expect(stderr.String()).To(ContainSubstring("osac login"))
	})

	It("Shows username and tenant for logged in user", func() {
		tokenString := createTestToken("testuser", "test-org", nil)
		ctx = setupContext(tokenString)

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Logged in as: testuser"))
		Expect(output.String()).To(ContainSubstring("Tenant: test-org"))
		Expect(output.String()).ToNot(ContainSubstring("Roles: "))
		Expect(output.String()).ToNot(ContainSubstring("Token claims:"))
		Expect(output.String()).ToNot(ContainSubstring("Access token:"))
	})

	It("Shows roles when present", func() {
		tokenString := createTestToken("testuser", "test-org", []string{"admin", "developer"})
		ctx = setupContext(tokenString)

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Logged in as: testuser"))
		Expect(output.String()).To(ContainSubstring("Roles: admin, developer"))
	})

	Context("--show-token flag behavior", func() {
		It("Does not show token by default", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).ToNot(ContainSubstring("Access token:"))
			Expect(output.String()).ToNot(ContainSubstring(tokenString))
		})

		It("Shows token with --show-token flag", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).To(ContainSubstring("Access token:"))
			Expect(output.String()).To(ContainSubstring(tokenString))
		})

		It("Shows token with -t shorthand", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-t"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).To(ContainSubstring("Access token:"))
			Expect(output.String()).To(ContainSubstring(tokenString))
		})
	})

	Context("--show-token-claims flag behavior", func() {
		It("Does not show claims by default", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).ToNot(ContainSubstring("Token claims:"))
		})

		It("Shows claims with --show-token-claims flag", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).To(ContainSubstring("Token claims:"))
			Expect(output.String()).To(ContainSubstring("username"))
			Expect(output.String()).To(ContainSubstring("testuser"))
		})

		It("Shows claims with -c shorthand", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-c"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).To(ContainSubstring("Token claims:"))
		})
	})

	Context("--rfc-3339 flag behavior", func() {
		It("Shows Unix timestamps by default", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			// Should contain numeric Unix timestamp, not RFC3339 format
			outputStr := output.String()
			Expect(outputStr).To(ContainSubstring("iat"))
			// RFC3339 has 'T' separator between date and time
			Expect(outputStr).ToNot(MatchRegexp(`"iat":\s*"\d{4}-\d{2}-\d{2}T`))
		})

		It("Converts to RFC3339 with --rfc-3339 flag", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims", "--rfc-3339"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			outputStr := output.String()
			// Should contain RFC3339 formatted time (contains 'T' separator and timezone)
			Expect(outputStr).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`))
		})

		It("Converts to RFC3339 with -R shorthand", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-c", "-R"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			outputStr := output.String()
			Expect(outputStr).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`))
		})

		It("Has no effect without --show-token-claims", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--rfc-3339"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).ToNot(ContainSubstring("Token claims:"))
		})
	})

	Context("--utc flag behavior", func() {
		It("Uses local timezone by default", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims", "--rfc-3339"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			// Just verify it produces RFC3339 output (timezone depends on system)
			Expect(output.String()).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`))
		})

		It("Uses UTC timezone with --utc flag", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims", "--rfc-3339", "--utc"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			// UTC times end with 'Z' or '+00:00'
			Expect(output.String()).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`))
		})

		It("Uses UTC timezone with -U shorthand", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"-c", "-R", "-U"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(output.String()).To(MatchRegexp(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`))
		})

		It("Has no effect without --rfc-3339", func() {
			tokenString := createTestToken("testuser", "test-org", nil)
			ctx = setupContext(tokenString)

			cmd := Cmd()
			cmd.SetOut(GinkgoWriter)
			cmd.SetErr(GinkgoWriter)
			cmd.SetContext(ctx)
			cmd.SetArgs([]string{"--show-token-claims", "--utc"})

			err := cmd.Execute()
			Expect(err).ToNot(HaveOccurred())
			// Should still show Unix timestamps, not RFC3339
			outputStr := output.String()
			Expect(outputStr).To(ContainSubstring("iat"))
			Expect(outputStr).ToNot(MatchRegexp(`"iat":\s*"\d{4}-\d{2}-\d{2}T`))
		})
	})

	It("Prefers context tenant over token organization", func() {
		tokenString := createTestToken("testuser", "token-org", nil)
		ctx = setupContext(tokenString)
		// Add context tenant which should take precedence
		ctx = config.TenantIntoContext(ctx, "context-org")

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Tenant: context-org"))
		Expect(output.String()).ToNot(ContainSubstring("token-org"))
	})

	It("Combines multiple flags correctly", func() {
		tokenString := createTestToken("testuser", "test-org", []string{"admin"})
		ctx = setupContext(tokenString)

		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--show-token", "--show-token-claims"})

		err := cmd.Execute()
		Expect(err).ToNot(HaveOccurred())
		outputStr := output.String()
		Expect(outputStr).To(ContainSubstring("Logged in as: testuser"))
		Expect(outputStr).To(ContainSubstring("Roles: admin"))
		Expect(outputStr).To(ContainSubstring("Access token:"))
		Expect(outputStr).To(ContainSubstring("Token claims:"))
	})

	It("Rejects arguments", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"extra-arg"})

		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		// cobra's NoArgs validator returns this error
		lines := strings.Split(err.Error(), "\n")
		Expect(lines[0]).To(ContainSubstring("unknown command"))
	})
})
