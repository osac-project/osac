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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI Error Handling", Label("cli", "errors"), func() {
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

	It("Command without login shows configuration error", func(ctx context.Context) {
		// Run a command without any prior login — homeDir has no stored credentials
		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "computeinstance")
		Expect(exitCode).ToNot(Equal(0), "command without login should fail")

		combinedOutput := stdout + stderr
		Expect(combinedOutput).To(ContainSubstring("there is no configuration"))
	})

	It("Unknown resource type shows a helpful error", func(ctx context.Context) {
		// Login first so we have valid credentials
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		// Try to get an unknown resource type
		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "nonexistentresource")
		Expect(exitCode).To(Equal(0), "unknown resource type renders help and exits 0")
		Expect(stdout).To(ContainSubstring("There is no object named"))
	})

	It("Version command does not crash", func(ctx context.Context) {
		// osac version should print CLI version info without crashing,
		// even without a server connection
		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "version")
		combinedOutput := stdout + stderr

		// The command should not panic — any exit code is acceptable as long as
		// there is meaningful output and no stack trace
		Expect(combinedOutput).ToNot(BeEmpty(), "version should produce output")
		Expect(combinedOutput).ToNot(ContainSubstring("runtime error"), "version should not panic")
		Expect(combinedOutput).ToNot(ContainSubstring("goroutine"), "version should not produce a stack trace")
		_ = exitCode
	})
})
