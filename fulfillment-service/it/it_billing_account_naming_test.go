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
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"

	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The generated REST/CLI unit tests pin the marshaling contract; this spec checks private field and filter names on
// the deployed service. It reuses the serial TLS stub fixture, so it is skipped unless the cluster can reach it and
// runs serially because the fixture restarts the shared grpc-server.
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
			{ID: uuid.New(), Name: "naming-account-a-" + suffix, ExternalID: "naming-a-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
			{ID: uuid.New(), Name: "naming-account-z-" + suffix, ExternalID: "naming-z-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
		}
		stub = billingstub.New(billingfake.Scenario{Accounts: fixtures})
		configureBillingStub(ctx, stub, listen, origin)
	})

	It("serves provider_external_id over deployed private REST and CLI", func(ctx context.Context) {
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
		expectNames := func(g Gomega, item map[string]any, fixture billingfake.AccountSpec, assigned bool) {
			GinkgoHelper()
			for _, key := range []string{"correlation_id", "external_billing_account_id", "provider_external_id", "provider_account_reference"} {
				g.Expect(item).ToNot(HaveKey(key))
			}
			status := item["status"]
			g.Expect(status).To(HaveKeyWithValue("display_name", fixture.Name))
			g.Expect(status).To(HaveKeyWithValue("provider_external_id", fixture.ExternalID))
			g.Expect(status).ToNot(HaveKey("correlation_id"))
			if assigned {
				g.Expect(status).To(HaveKeyWithValue("external_billing_account_id", fixture.ExternalID))
			} else {
				g.Expect(status).ToNot(HaveKey("external_billing_account_id"), "discovery must not establish attribution")
			}
			g.Expect(status).To(HaveKeyWithValue("provider_account_reference", fixture.ID))
		}
		routes := []struct {
			client *http.Client
			path   string
		}{
			{tool.InternalView().AdminClient(), "/api/private/v1/billing/accounts"},
		}
		accountIDs := make([]string, len(fixtures))
		for _, route := range routes {
			By("filtering discovered accounts by provider_external_id at " + route.path)
			for i, fixture := range fixtures {
				query := url.Values{
					"filter": {fmt.Sprintf("this.status.provider_external_id == %q", fixture.ExternalID)},
				}
				Eventually(ctx, func(g Gomega) {
					body, statusCode, err := get(route.client, route.path+"?"+query.Encode())
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(statusCode).To(Equal(http.StatusOK))
					items, err := decodeREST(body)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(items).To(HaveLen(1))
					expectNames(g, items[0], fixture, false)
					id, ok := items[0]["id"].(string)
					g.Expect(ok).To(BeTrue())
					g.Expect(id).ToNot(BeEmpty())
					g.Expect(id).ToNot(Equal(fixture.ID))
					accountIDs[i] = id
				}, time.Minute, time.Second).Should(Succeed())
			}
		}

		By("establishing attribution by assigning each account to a fresh tenant")
		assignments := privatev1.NewTenantBillingAssignmentsClient(tool.InternalView().AdminConn())
		var tenant string
		for _, id := range accountIDs {
			tenant = newBillingTenant(ctx, nil, nil).GetMetadata().GetName()
			created, err := assignments.Create(ctx, privatev1.TenantBillingAssignmentsCreateRequest_builder{
				Object: privatev1.TenantBillingAssignment_builder{
					Metadata: privatev1.Metadata_builder{Name: "billing-" + uuid.New(), Tenant: tenant}.Build(),
					Spec: privatev1.TenantBillingAssignmentSpec_builder{
						BillingAccount: privatev1.BillingAccountReference_builder{Id: id}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		}

		for _, route := range routes {
			By("filtering by the established external_billing_account_id at " + route.path)
			query := url.Values{
				"filter": {fmt.Sprintf("this.status.external_billing_account_id == %q || this.status.external_billing_account_id == %q",
					fixtures[0].ExternalID, fixtures[1].ExternalID)},
			}
			body, statusCode, err := get(route.client, route.path+"?"+query.Encode())
			Expect(err).ToNot(HaveOccurred())
			Expect(statusCode).To(Equal(http.StatusOK))
			items, err := decodeREST(body)
			Expect(err).ToNot(HaveOccurred())
			Expect(items).To(HaveLen(2))
			// List ordering is not implemented: the service orders by internal id and ignores `order`, so
			// identify each returned account by id instead of relying on its position.
			byID := make(map[string]map[string]any, len(items))
			for _, item := range items {
				id, ok := item["id"].(string)
				Expect(ok).To(BeTrue())
				byID[id] = item
			}
			for i, fixture := range fixtures {
				item, ok := byID[accountIDs[i]]
				Expect(ok).To(BeTrue(), "missing account %s", accountIDs[i])
				expectNames(Default, item, fixture, true)
			}

			By("rejecting the former field name in filters at " + route.path)
			body, statusCode, err = get(route.client, route.path+"?"+url.Values{
				"filter": {fmt.Sprintf("this.correlation_id == %q", fixtures[0].ExternalID)},
			}.Encode())
			Expect(err).ToNot(HaveOccurred())
			Expect(statusCode).To(Equal(http.StatusBadRequest))
			var rejection struct {
				Code int32 `json:"code"`
			}
			Expect(json.Unmarshal(body, &rejection)).To(Succeed(), "body: %s", body)
			Expect(rejection.Code).To(Equal(int32(codes.InvalidArgument)), "body: %s", body)
		}

		By("showing the populated value and header through the private CLI")
		headers := []string{"ID", "DELETING", "NAME", "PROVIDER", "DISPLAY-NAME", "CURRENCY", "STATE",
			"EXTERNAL-BILLING-ACCOUNT-ID", "STATUS-OBSERVED"}
		home := newBillingCLI(ctx, tenant, true)
		typeName := "osac.private.v1.BillingAccount"
		for i, fixture := range fixtures {
			for _, format := range []string{"json", "yaml"} {
				items := billingCLIItems(ctx, home, "billingaccount", format, typeName, accountIDs[i])
				Expect(items).To(HaveLen(1))
				Expect(items[0]).To(HaveKeyWithValue("id", accountIDs[i]))
				expectNames(Default, items[0], fixture, true)
			}
			table := expectBillingCLITable(ctx, home, "billingaccount", headers, 1, accountIDs[i])
			row := strings.Fields(strings.SplitN(table, "\n", 2)[1])
			Expect(row).To(HaveLen(len(headers)))
			Expect(row[slices.Index(headers, "EXTERNAL-BILLING-ACCOUNT-ID")]).To(Equal(fixture.ExternalID))
			Expect(table).ToNot(ContainSubstring(fixture.ID), "private native identity is not a default column")
		}
		Expect(stub.Writes()).To(BeEmpty(), "naming must remain read-only towards the provider")
	})
})
