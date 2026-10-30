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

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. They need no
// provider, hence no provider call can happen.
var _ = Describe("Billing provider status", Label("billing"), func() {
	It("is NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetState()).To(Equal(
			publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		Expect(response.GetObject().GetProviderKey()).To(BeEmpty())
	})

	It("is shown by the CLI as NOT_CONFIGURED", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingproviderstatus", "-o", "json")
		Expect(exitCode).To(Equal(0))
		Expect(stdout).To(MatchJSON(`{
			"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
		}`))
	})
})
