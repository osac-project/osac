/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. Unlike
// BillingProviderStatuses.Get, which returns a NOT_CONFIGURED status object, the accounts List has no success
// shape for disabled mode: it fails on every path, so the CLI command fails too.
var _ = Describe("Billing accounts", Label("billing"), func() {
	It("fails with BILLING_NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewBillingAccountsClient(tool.ExternalView().AdminConn())
		response, err := client.List(ctx, publicv1.BillingAccountsListRequest_builder{}.Build())
		Expect(response).To(BeNil())
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(status.Convert(err).Message()).To(ContainSubstring("billing is not configured"))
	})

	It("fails from the CLI with the not-configured error", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingaccount", "-o", "json")
		Expect(exitCode).ToNot(Equal(0))
		Expect(stdout + stderr).To(ContainSubstring("billing is not configured"))
	})
})
