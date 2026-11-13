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

// The dev deployment configures no billing provider, so this spec exercises the disabled mode end to end: the CLI
// reaches the deployed server and surfaces its FAILED_PRECONDITION/BILLING_NOT_CONFIGURED rejection. The currency
// filter is mandatory on this command, so a valid value must be supplied for the request to reach the server at all.
var _ = Describe("Provider rate card items CLI", Label("billing"), func() {
	It("fails with billing not configured over the CLI when no provider is configured", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "providerratecarditem", "--currency-code", "USD")
		Expect(exitCode).ToNot(Equal(0))
		Expect(stdout + stderr).To(ContainSubstring("billing is not configured"))
	})
})
