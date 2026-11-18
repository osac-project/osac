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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"

	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

// BillingAccount.status.external_billing_account_id is the canonical nested attribution property.
// The generated REST/CLI unit tests pin the marshaling contract; this spec checks the property,
// filter and order spellings are what the deployed service actually serves. It reuses the serial TLS stub fixture,
// which the cluster must reach, so the spec is skipped unless both variables are set. The fixture restarts the shared
// grpc-server, so the spec must not overlap other specs.
var _ = Describe("Billing account naming", Label("billing", "billing-configured", "billing-account-naming"), Serial, func() {
	var (
		stub     *billingstub.Stub
		fixtures []billingfake.AccountSpec
	)

	BeforeEach(func(ctx context.Context) {
		listen := os.Getenv("OSAC_IT_BILLING_STUB_LISTEN")
		origin := os.Getenv("OSAC_IT_BILLING_STUB_URL")
		if listen == "" || origin == "" {
			Skip("OSAC_IT_BILLING_STUB_LISTEN and OSAC_IT_BILLING_STUB_URL are not set")
		}
		// Discovery retains provider accounts across runs. Never reuse a provider reference or display name.
		suffix := uuid.New()
		fixtures = []billingfake.AccountSpec{
			{ID: uuid.New(), Name: "naming-a-" + suffix, ExternalID: "naming-a-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
			{ID: uuid.New(), Name: "naming-z-" + suffix, ExternalID: "naming-z-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
		}
		stub = billingstub.New(billingfake.Scenario{Accounts: fixtures})
		configureBillingStub(ctx, stub, listen, origin)
	})

	It("publishes external_billing_account_id over deployed public and private REST and CLI", func(ctx context.Context) {
		// Decode the items so every name assertion inspects parsed keys, not the raw response body.
		decodeREST := func(body []byte) ([]map[string]any, error) {
			var response struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return nil, fmt.Errorf("decode billing accounts response: %w", err)
			}
			return response.Items, nil
		}
		get := func(client *http.Client, path string) ([]byte, int, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			if err != nil {
				return nil, 0, err
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, 0, err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			return body, response.StatusCode, err
		}
		for _, route := range []struct {
			client *http.Client
			path   string
		}{
			{tool.ExternalView().AdminClient(), "/api/fulfillment/v1/billing/accounts"},
			{tool.InternalView().AdminClient(), "/api/private/v1/billing/accounts"},
		} {
			By("filtering each account by the new field at " + route.path)
			for _, fixture := range fixtures {
				query := url.Values{
					"filter": {fmt.Sprintf("this.status.external_billing_account_id == %q", fixture.ExternalID)},
					"order":  {"status.external_billing_account_id desc"},
				}
				Eventually(ctx, func(g Gomega) {
					body, statusCode, err := get(route.client, route.path+"?"+query.Encode())
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(statusCode).To(Equal(http.StatusOK))
					items, err := decodeREST(body)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(items).To(HaveLen(1))
					g.Expect(items[0]["status"]).To(HaveKeyWithValue("external_billing_account_id", fixture.ExternalID))
					g.Expect(items[0]).ToNot(HaveKey("correlation_id"))
				}, time.Minute, time.Second).Should(Succeed())
			}

			By("ordering by the new field in descending order at " + route.path)
			query := url.Values{
				"filter": {`this.status.provider_key == "m360"`},
				"order":  {"status.external_billing_account_id desc"},
				"limit":  {"1000"},
			}
			body, statusCode, err := get(route.client, route.path+"?"+query.Encode())
			Expect(err).ToNot(HaveOccurred())
			Expect(statusCode).To(Equal(http.StatusOK))
			items, err := decodeREST(body)
			Expect(err).ToNot(HaveOccurred())
			selected := []string{}
			for _, item := range items {
				status, ok := item["status"].(map[string]any)
				Expect(ok).To(BeTrue())
				if value, ok := status["external_billing_account_id"].(string); ok &&
					(value == fixtures[0].ExternalID || value == fixtures[1].ExternalID) {
					selected = append(selected, value)
				}
			}
			Expect(selected).To(Equal([]string{fixtures[1].ExternalID, fixtures[0].ExternalID}))

			By("rejecting the former field name in filters and order at " + route.path)
			for _, query := range []url.Values{
				{"filter": {fmt.Sprintf("this.correlation_id == %q", fixtures[0].ExternalID)}},
				{"order": {"correlation_id"}},
			} {
				body, statusCode, err := get(route.client, route.path+"?"+query.Encode())
				Expect(err).ToNot(HaveOccurred())
				Expect(statusCode).To(Equal(http.StatusBadRequest))
				var rejection struct {
					Code int32 `json:"code"`
				}
				Expect(json.Unmarshal(body, &rejection)).To(Succeed(), "body: %s", body)
				Expect(rejection.Code).To(Equal(int32(codes.InvalidArgument)), "body: %s", body)
			}
		}

		By("showing the populated value and header through the CLI in every format")
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")
		for _, format := range []string{"json", "yaml", "table"} {
			By("reading billing accounts as " + format)
			stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingaccount",
				"-o", format)
			Expect(exitCode).To(Equal(0), "stdout: %s\nstderr: %s", stdout, stderr)
			if format == "table" {
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				Expect(lines).ToNot(BeEmpty())
				header := strings.Fields(lines[0])
				Expect(header).To(ContainElement("EXTERNAL-BILLING-ACCOUNT-ID"))
				Expect(header).ToNot(ContainElement("CORRELATION-ID"))
				column := map[string]int{}
				for index, name := range header {
					column[name] = index
				}
				for _, fixture := range fixtures {
					values := []string{}
					for _, line := range lines[1:] {
						fields := strings.Fields(line)
						Expect(fields).To(HaveLen(len(header)), "table row is %q", line)
						if fields[column["DISPLAY-NAME"]] == fixture.Name {
							values = append(values, fields[column["EXTERNAL-BILLING-ACCOUNT-ID"]])
						}
					}
					Expect(values).To(Equal([]string{fixture.ExternalID}))
				}
				continue
			}
			items, err := decodeBillingCLIItems(stdout, format, "osac.public.v1.BillingAccount")
			Expect(err).ToNot(HaveOccurred())
			for _, fixture := range fixtures {
				matched := 0
				for _, item := range items {
					status, ok := item["status"].(map[string]any)
					Expect(ok).To(BeTrue())
					if status["display_name"] != fixture.Name {
						continue
					}
					Expect(status).To(HaveKeyWithValue("external_billing_account_id", fixture.ExternalID))
					Expect(status).ToNot(HaveKey("correlation_id"))
					matched++
				}
				Expect(matched).To(Equal(1), "the fixture account must appear exactly once in %s output", format)
			}
		}
		Expect(stub.Writes()).To(BeEmpty(), "naming must remain read-only towards the provider")
	})
})
