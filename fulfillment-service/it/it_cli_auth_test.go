/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI Authentication", Label("cli", "auth"), func() {
	var homeDir string

	BeforeEach(func() {
		var err error
		homeDir, err = tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(homeDir)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	It("Login with password flow succeeds", func(ctx context.Context) {
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		// Verify that subsequent commands work with the stored credentials
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).To(Equal(0), "get should succeed after login")
	})

	It("Login with client credentials flow succeeds", func(ctx context.Context) {
		_, _, exitCode := tool.RunCLI(ctx, homeDir,
			"login", "https://"+externalServiceAddr,
			"--flow=credentials",
			"--client-id="+controllerClientId,
			"--client-secret="+tool.Secret(),
			"--insecure",
		)
		Expect(exitCode).To(Equal(0), "client credentials login should succeed")

		// Verify that subsequent commands work with the stored credentials
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).To(Equal(0), "get should succeed after client credentials login")
	})

	It("Login with invalid credentials fails", func(ctx context.Context) {
		_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, "wrong-password")
		Expect(exitCode).ToNot(Equal(0), "login with wrong password should fail")
		Expect(stderr).To(ContainSubstring("invalid_grant"), "should report invalid credentials")

		// Verify that no valid configuration was saved
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).ToNot(Equal(0), "commands should fail after failed login")
	})

	It("Logout clears stored credentials", func(ctx context.Context) {
		// First login successfully
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		// Verify commands work
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).To(Equal(0), "get should succeed after login")

		// Logout
		_, _, exitCode = tool.RunCLI(ctx, homeDir, "logout")
		Expect(exitCode).To(Equal(0), "logout should succeed")

		// Verify commands fail after logout
		_, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).ToNot(Equal(0), "commands should fail after logout")
		Expect(stderr).To(ContainSubstring("there is no configuration"))
	})

	It("Corrupted configuration produces a clear error", func(ctx context.Context) {
		// Write a malformed config.json to verify the CLI handles it gracefully
		configDir := filepath.Join(homeDir, ".config", "osac")
		err := os.MkdirAll(configDir, 0700)
		Expect(err).ToNot(HaveOccurred())
		configFile := filepath.Join(configDir, "config.json")
		err = os.WriteFile(configFile, []byte(`{not valid json`), 0600)
		Expect(err).ToNot(HaveOccurred())

		// The CLI should fail with a clear error, not a panic
		_, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).ToNot(Equal(0), "commands with corrupted config should fail")
		Expect(stderr).ToNot(BeEmpty(), "should produce an error message")
		Expect(stderr).ToNot(ContainSubstring("runtime error"), "should not panic")
		Expect(stderr).ToNot(ContainSubstring("goroutine"), "should not dump stack trace")
	})
})
