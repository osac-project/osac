/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingaccount

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	run := func(format string, response *publicv1.BillingAccountsListResponse) string {
		buffer := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, response)).To(Succeed())
		return buffer.String()
	}

	listWith := func(items ...*publicv1.BillingAccount) *publicv1.BillingAccountsListResponse {
		response := &publicv1.BillingAccountsListResponse{}
		response.SetSize(int32(len(items)))
		response.SetTotal(int32(len(items)))
		response.SetItems(items)
		return response
	}

	active := publicv1.BillingAccount_builder{
		Id: "ba-1", ProviderKey: "m360", DisplayName: "Acme production", BaseCurrency: "USD",
		State:            publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
		StatusObservedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		CorrelationId:    "acct-corr-1001",
	}.Build()

	unknownNoCurrency := publicv1.BillingAccount_builder{
		Id: "ba-2", ProviderKey: "m360", DisplayName: "Acme staging",
		State: publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN,
	}.Build()

	It("shows an empty currency and an unknown state explicitly, never blank", func() {
		output := run("table", listWith(unknownNoCurrency))
		Expect(output).To(MatchRegexp(`ba-2\s+m360\s+Acme staging\s+-\s+UNKNOWN\s+-\s+-`))
	})

	It("renders an active account's fields without an enum prefix", func() {
		output := run("table", listWith(active))
		Expect(output).To(ContainSubstring("USD"))
		Expect(output).To(MatchRegexp(`\bACTIVE\b`))
		Expect(output).ToNot(ContainSubstring("BILLING_ACCOUNT_STATE_"))
		Expect(output).To(ContainSubstring("acct-corr-1001"))
	})

	It("says so when there are no accounts, in table mode", func() {
		output := run("table", listWith())
		Expect(output).To(ContainSubstring("No billing accounts found"))
	})

	It("renders JSON with API field names, omitting empty base_currency and correlation_id", func() {
		output := run("json", listWith(unknownNoCurrency))
		Expect(output).To(MatchJSON(`{
			"size": 1,
			"total": 1,
			"items": [{
				"id": "ba-2",
				"provider_key": "m360",
				"display_name": "Acme staging",
				"state": "BILLING_ACCOUNT_STATE_UNKNOWN"
			}]
		}`))
	})

	It("renders YAML with API field names", func() {
		output := run("yaml", listWith(active))
		Expect(output).To(ContainSubstring("base_currency: USD"))
		Expect(output).To(ContainSubstring("correlation_id: acct-corr-1001"))
	})
})
