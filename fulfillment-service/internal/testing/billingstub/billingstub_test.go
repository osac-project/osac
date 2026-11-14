/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingstub_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
)

var ctx = context.Background()

const (
	testCredential    = "tok"
	testAuthorization = "Bearer sent"
)

// fixture is the same JSON format billingfake.Load reads.
const fixture = `{
  "accounts": [
    {"id": "a1", "name": "one", "externalId": "ext", "state": "ACTIVE", "rawStatus": "Active",
     "approvalStatus": "Approved"},
    {"id": "a2", "name": "two", "externalId": "ext", "state": "INACTIVE", "rawStatus": "Inactive"},
    {"id": "a3", "name": "three", "state": "UNKNOWN", "rawStatus": "weird"}
  ],
  "services": [{"id": "s1", "name": "GPU Hours", "attributes": [{"id": "dim-1", "name": "gpu_hours"}],
    "rates": {"USD": {"rows": [{"dimensionId": "dim-1", "priceMethod": "QUANTITY", "unitRate": "0.25"}]}}}]
}`

// start serves the scenario on a local stub and returns it with a built M360 adapter.
func start(s billingfake.Scenario) (*billingstub.Stub, billing.Adapter) {
	stub := billingstub.New(s)
	ExpectWithOffset(1, stub.Start("127.0.0.1:0")).To(Succeed())
	DeferCleanup(stub.Close)
	adapter, err := m360.New(billing.Config{BaseURL: stub.URL() + "/api/v2/external", Credential: testCredential, RetryCount: 3,
		BreakerThreshold: 5, BreakerWindow: 30 * time.Second, BreakerOpen: 30 * time.Second}).Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return stub, adapter
}

// reasonOf asserts err is a *billing.Error and returns its Reason.
func reasonOf(err error) billing.Reason {
	var e *billing.Error
	ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue(), "error was %v", err)
	return e.Reason
}

// load reads a scenario from JSON, failing the spec on error.
func load(data string) billingfake.Scenario {
	s, err := billingfake.Load([]byte(data))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return s
}

// requestJSON exercises the wire contract independently of the adapter's decoder.
func requestJSON(stub *billingstub.Stub, method, path, body string) (int, map[string]any) {
	GinkgoHelper()
	request, err := http.NewRequest(method, stub.URL()+"/api/v2/external"+path, strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	var data map[string]any
	Expect(json.NewDecoder(response.Body).Decode(&data)).To(Succeed())
	return response.StatusCode, data
}

var _ = Describe("Billing stub", func() {
	It("serves the shared fixture to the real M360 adapter", func() {
		stub, adapter := start(load(fixture))

		Expect(adapter.HealthCheck(ctx)).To(Succeed())

		page, err := adapter.ListAccounts(ctx, billing.AccountFilter{ExternalID: "ext"}, billing.Page{Size: 1})
		Expect(err).ToNot(HaveOccurred())
		Expect(page.Accounts).To(HaveLen(1))
		Expect(page.Next).ToNot(BeEmpty())
		next, err := adapter.ListAccounts(ctx, billing.AccountFilter{ExternalID: "ext"},
			billing.Page{Size: 1, Token: page.Next})
		Expect(err).ToNot(HaveOccurred())
		Expect(next.Accounts).To(HaveLen(1))
		Expect(next.Next).To(BeEmpty())
		// A lone INACTIVE status (no ApprovalStatus) wins over the missing signal; the raw value is still exposed.
		Expect(next.Accounts[0].State).To(Equal(billing.AccountInactive))
		Expect(next.Accounts[0].RawStatus).To(Equal("Inactive"))

		account, err := adapter.GetAccount(ctx, billing.NewAccountRef("a1"))
		Expect(err).ToNot(HaveOccurred())
		Expect(account.RawStatus).To(Equal("Active"))
		Expect(account.ApprovalStatus).To(Equal("Approved"))
		_, err = adapter.GetAccount(ctx, billing.NewAccountRef("nope"))
		Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))

		ref, err := adapter.FindServiceByName(ctx, "GPU Hours")
		Expect(err).ToNot(HaveOccurred())
		attrs, err := adapter.ListPriceableAttributes(ctx, ref)
		Expect(err).ToNot(HaveOccurred())
		Expect(attrs).To(Equal([]billing.PriceableAttribute{{ID: "dim-1", Name: "gpu_hours"}}))
		card, err := adapter.GetServiceRateCard(ctx, ref, "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(card.Rows).To(Equal([]billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}}))
		_, err = adapter.GetServiceRateCard(ctx, ref, "EUR")
		Expect(err).To(MatchError(billing.ErrRateCardNotFound))
		_, err = adapter.FindServiceByName(ctx, "missing")
		Expect(err).To(MatchError(billing.ErrServiceNotFound))

		Expect(stub.Writes()).To(BeEmpty())
	})

	It("delivers mixed incomplete rate rows through the real M360 adapter", func() {
		stub, adapter := start(billingfake.Scenario{
			Services: []billingfake.ServiceSpec{{
				ID:   "s",
				Name: "n",
				Rates: map[string]billingfake.RateCardSpec{
					"USD": {Rows: []billing.RateRow{
						{DimensionID: "d1", PriceMethod: "QUANTITY", UnitRate: "0.1000000000000000055",
							AttributeValues: []billing.AttributeValue{
								{Name: "gpu_model", Value: "H100"},
								{Name: "region", Value: "us-east-1"},
							}},
						{DimensionID: "d2", PriceMethod: "NO_PRICE",
							AttributeValues: []billing.AttributeValue{{Name: "included", Value: "true"}}},
						{DimensionID: "d3", PriceMethod: "TIERED",
							AttributeValues: []billing.AttributeValue{{Name: "billing_mode", Value: "tiered"}}},
						{UnitRate: "9.9E-9",
							AttributeValues: []billing.AttributeValue{{Name: "unclassified", Value: "kept"}}},
					}},
				},
			}},
		})

		ref, err := adapter.FindServiceByName(ctx, "n")
		Expect(err).ToNot(HaveOccurred())
		card, err := adapter.GetServiceRateCard(ctx, ref, "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(card).To(Equal(billing.RateCard{
			Currency: "USD",
			Rows: []billing.RateRow{
				{DimensionID: "d1", PriceMethod: "QUANTITY", UnitRate: "0.1000000000000000055",
					AttributeValues: []billing.AttributeValue{
						{Name: "gpu_model", Value: "H100"},
						{Name: "region", Value: "us-east-1"},
					}},
				{DimensionID: "d2", PriceMethod: "NO_PRICE",
					AttributeValues: []billing.AttributeValue{{Name: "included", Value: "true"}}},
				{DimensionID: "d3", PriceMethod: "TIERED",
					AttributeValues: []billing.AttributeValue{{Name: "billing_mode", Value: "tiered"}}},
				{UnitRate: "9.9E-9",
					AttributeValues: []billing.AttributeValue{{Name: "unclassified", Value: "kept"}}},
			},
		}))
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("returns no card for a currency mismatch with a stable non-retryable reason", func() {
		stub, adapter := start(billingfake.Scenario{Services: []billingfake.ServiceSpec{{ID: "s", Name: "n",
			Rates: map[string]billingfake.RateCardSpec{"USD": {Currency: "EUR", Rows: []billing.RateRow{
				{DimensionID: "d", PriceMethod: "QUANTITY", UnitRate: "1"}}}}}}})
		ref, err := adapter.FindServiceByName(ctx, "n")
		Expect(err).ToNot(HaveOccurred())
		card, err := adapter.GetServiceRateCard(ctx, ref, "USD")
		Expect(reasonOf(err)).To(Equal(billing.ReasonCurrencyMismatch))
		Expect(card).To(Equal(billing.RateCard{}))
		var billingErr *billing.Error
		Expect(errors.As(err, &billingErr)).To(BeTrue())
		Expect(billingErr.Retryable).To(BeFalse())
		Expect(stub.Count("GetServiceRateCard")).To(Equal(1))
	})

	It("scripts a 5xx that the adapter retries, and records every attempt", func() {
		stub, adapter := start(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"HealthCheck": {Reason: billing.ReasonUnavailable, Status: 503, Times: 2}}})

		Expect(adapter.HealthCheck(ctx)).To(Succeed())
		Expect(stub.Count("HealthCheck")).To(Equal(3))
		Expect(stub.Succeeded("HealthCheck")).To(Equal(1))
	})

	It("scripts a persistent 5xx as UNAVAILABLE", func() {
		stub, adapter := start(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"ListAccounts": {Reason: billing.ReasonUnavailable}}})

		_, err := adapter.ListAccounts(ctx, billing.AccountFilter{}, billing.Page{})
		Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
		Expect(stub.Count("ListAccounts")).To(Equal(4))
	})

	It("scripts a 401 as UNAUTHORIZED without retrying", func() {
		stub, adapter := start(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"GetAccount": {Reason: billing.ReasonUnauthorized, Status: 401}}})

		_, err := adapter.GetAccount(ctx, billing.NewAccountRef("a"))
		Expect(reasonOf(err)).To(Equal(billing.ReasonUnauthorized))
		Expect(stub.Count("GetAccount")).To(Equal(1))
	})

	It("answers failures with the flat M360 error body", func() {
		stub, _ := start(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"HealthCheck": {Reason: billing.ReasonUnauthorized, Status: 401}}})

		response, err := http.Get(stub.URL() + "/api/v2/external/accounts?size=1")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		Expect(response.StatusCode).To(Equal(401))
		var body struct {
			Status         int
			Error, Message string
		}
		Expect(json.NewDecoder(response.Body).Decode(&body)).To(Succeed())
		Expect(body.Status).To(Equal(401))
		Expect(body.Error).ToNot(BeEmpty())
		Expect(body.Message).ToNot(BeEmpty())
	})

	It("retains a camelCase externalId PUT and returns PascalCase GET and list rows", func() {
		scenario := load(fixture)
		stub, _ := start(scenario)
		code, body := requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"new-correlation"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(body).To(Equal(map[string]any{"id": "a1", "name": "one", "externalId": "new-correlation"}))
		code, body = requestJSON(stub, http.MethodGet, "/accounts/a1", "")
		Expect(code).To(Equal(http.StatusOK))
		Expect(body["data"]).To(HaveKeyWithValue("ExternalId", "new-correlation"))
		Expect(body["data"]).To(HaveKeyWithValue("Status__r", HaveKeyWithValue("Name", "Active")))
		_, body = requestJSON(stub, http.MethodGet, "/accounts?q=new-correlation", "")
		Expect(body["data"]).To(HaveKeyWithValue("content", ConsistOf(HaveKeyWithValue("Id", "a1"))))
		// The stub owns a copy, not the caller's fixture slice.
		Expect(scenario.Accounts[0].ExternalID).To(Equal("ext"))
	})

	It("reports the adapter's correlation write as unsupported and sends no request", func() {
		stub, adapter := start(load(fixture))
		Expect(adapter.SupportsCorrelationWrite()).To(BeFalse())
		err := adapter.SetCorrelationValue(ctx, billing.NewAccountRef("a1"), "new-correlation")
		Expect(reasonOf(err)).To(Equal(billing.ReasonUnsupported))
		Expect(stub.Writes()).To(BeEmpty(), "an unsupported write must not reach the provider")
		value, present, err := adapter.CorrelationValue(ctx, billing.NewAccountRef("a1"))
		Expect(err).ToNot(HaveOccurred())
		Expect(present).To(BeTrue())
		Expect(value).To(Equal("ext"), "the account keeps the fixture value")
	})

	It("classifies an account-specific write denial as unsupported without changing its value", func() {
		stub, _ := start(load(fixture))
		stub.SetCorrelationWriteDenied("a1", true)
		// The adapter advertises no write, so the stub's PUT contract is exercised directly over raw HTTP.
		code, _ := requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"denied-value"}`)
		Expect(code).To(Equal(http.StatusMethodNotAllowed))
		Expect(stub.Count("SetCorrelationValue")).To(Equal(1), "the stub records the denied write once")
		code, body := requestJSON(stub, http.MethodGet, "/accounts/a1", "")
		Expect(code).To(Equal(http.StatusOK))
		Expect(body["data"]).To(HaveKeyWithValue("ExternalId", "ext"))
		code, _ = requestJSON(stub, http.MethodPut, "/accounts/a2", `{"externalId":"allowed-value"}`)
		Expect(code).To(Equal(http.StatusOK))
		stub.SetCorrelationWriteDenied("a1", false)
		code, _ = requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"allowed-now"}`)
		Expect(code).To(Equal(http.StatusOK))
	})

	DescribeTable("rejects malformed account writes without mutation", func(body string) {
		stub, _ := start(load(fixture))
		code, _ := requestJSON(stub, http.MethodPut, "/accounts/a1", body)
		Expect(code).To(Equal(http.StatusBadRequest))
		_, read := requestJSON(stub, http.MethodGet, "/accounts/a1", "")
		Expect(read["data"]).To(HaveKeyWithValue("ExternalId", "ext"))
	},
		Entry("GET field casing is not the write contract", `{"ExternalId":"bad"}`),
		Entry("no scalar", `{}`),
		Entry("not a string", `{"externalId":123}`),
		Entry("null", `{"externalId":null}`),
		Entry("unmodelled writes", `{"externalId":"bad","addresses":[]}`),
		Entry("trailing JSON", `{"externalId":"bad"} {}`),
		Entry("malformed JSON", `{"externalId":`),
	)

	It("returns 404 for a PUT to a missing account", func() {
		stub, _ := start(load(fixture))
		code, body := requestJSON(stub, http.MethodPut, "/accounts/missing", `{"externalId":"value"}`)
		Expect(code).To(Equal(http.StatusNotFound))
		Expect(body).To(HaveKeyWithValue("status", float64(http.StatusNotFound)))
	})

	It("leaves account state unchanged when a scripted PUT fails", func() {
		scenario := load(fixture)
		scenario.Failures = map[string]billingfake.Failure{"SetCorrelationValue": {Status: http.StatusServiceUnavailable}}
		stub, _ := start(scenario)
		code, _ := requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"not-persisted"}`)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		_, read := requestJSON(stub, http.MethodGet, "/accounts/a1", "")
		Expect(read["data"]).To(HaveKeyWithValue("ExternalId", "ext"))
		Expect(stub.Writes()).To(ConsistOf(HaveField("Failed", true)))
	})

	It("resets retained values and write denials when replacing the scenario", func() {
		stub, _ := start(load(fixture))
		code, _ := requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"new-value"}`)
		Expect(code).To(Equal(http.StatusOK))
		stub.SetCorrelationWriteDenied("a1", true)
		stub.SetScenario(load(fixture))
		_, read := requestJSON(stub, http.MethodGet, "/accounts/a1", "")
		Expect(read["data"]).To(HaveKeyWithValue("ExternalId", "ext"))
		code, _ = requestJSON(stub, http.MethodPut, "/accounts/a1", `{"externalId":"allowed-again"}`)
		Expect(code).To(Equal(http.StatusOK))
	})

	It("keeps concurrent account writes and readbacks isolated within one stub", func() {
		accounts := make([]billingfake.AccountSpec, 8)
		for i := range accounts {
			accounts[i].ID = fmt.Sprintf("account-%d", i)
		}
		stub, _ := start(billingfake.Scenario{Accounts: accounts})
		var wg sync.WaitGroup
		for _, account := range accounts {
			wg.Go(func() {
				defer GinkgoRecover()
				value := "correlation-" + account.ID
				code, _ := requestJSON(stub, http.MethodPut, "/accounts/"+account.ID,
					`{"externalId":"`+value+`"}`)
				Expect(code).To(Equal(http.StatusOK))
				_, body := requestJSON(stub, http.MethodGet, "/accounts/"+account.ID, "")
				Expect(body["data"]).To(HaveKeyWithValue("ExternalId", value))
				_ = stub.Requests() // snapshots can race with other writes, but do not expose mutable state.
			})
		}
		wg.Wait()
		Expect(stub.Succeeded("SetCorrelationValue")).To(Equal(len(accounts)))
	})

	It("keeps q as substring search and leaves exact matching and page walking to the adapter", func() {
		accounts := make([]billingfake.AccountSpec, 21)
		for i := range accounts {
			accounts[i] = billingfake.AccountSpec{ID: fmt.Sprintf("a%d", i), Name: "EXT candidate", ExternalID: "ext-other"}
		}
		accounts[20].ExternalID = "ext"
		stub, adapter := start(billingfake.Scenario{Accounts: accounts})
		_, body := requestJSON(stub, http.MethodGet, "/accounts?q=EXT&size=100", "")
		Expect(body["data"]).To(HaveKeyWithValue("size", float64(20)))
		Expect(body["data"]).To(HaveKeyWithValue("last", false))
		Expect(body["data"].(map[string]any)["content"]).To(HaveLen(20))
		first, err := adapter.ListAccounts(ctx, billing.AccountFilter{ExternalID: "ext"}, billing.Page{Size: 100})
		Expect(err).ToNot(HaveOccurred())
		Expect(first.Accounts).To(BeEmpty(), "substring hits must not become exact correlation matches")
		Expect(first.Next).To(Equal("1"))
		second, err := adapter.ListAccounts(ctx, billing.AccountFilter{ExternalID: "ext"}, billing.Page{Token: first.Next})
		Expect(err).ToNot(HaveOccurred())
		Expect(second.Accounts).To(HaveLen(1))
		Expect(second.Accounts[0].Ref.ID()).To(Equal("a20"))
		Expect(second.Next).To(BeEmpty())
		query, err := url.ParseQuery(stub.Requests()[1].Query)
		Expect(err).ToNot(HaveOccurred())
		Expect(query.Get("size")).To(Equal("20"))
	})

	It("rejects and records unimplemented writes with 405", func() {
		stub, _ := start(load(fixture))

		for _, method := range []string{http.MethodPatch, http.MethodPost, http.MethodDelete} {
			request, err := http.NewRequest(method, stub.URL()+"/api/v2/external/accounts/a1", nil)
			Expect(err).ToNot(HaveOccurred())
			response, err := http.DefaultClient.Do(request)
			Expect(err).ToNot(HaveOccurred())
			_ = response.Body.Close()
			Expect(response.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		}
		Expect(stub.Writes()).To(HaveLen(3))
	})

	It("records the Authorization header of each request", func() {
		stub, _ := start(load(fixture))

		request, err := http.NewRequest(http.MethodGet, stub.URL()+"/api/v2/external/accounts?size=1", nil)
		Expect(err).ToNot(HaveOccurred())
		request.Header.Set("Authorization", testAuthorization)
		response, err := http.DefaultClient.Do(request)
		Expect(err).ToNot(HaveOccurred())
		_ = response.Body.Close()
		Expect(stub.Requests()).To(ConsistOf(HaveField("Authorization", testAuthorization)))
	})

	DescribeTable("serves TLS for the host with a certificate signed by the returned CA",
		func(host string) {
			stub := billingstub.New(load(fixture))
			caPEM, err := stub.StartTLS("127.0.0.1:0", host)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stub.Close)
			roots := x509.NewCertPool()
			Expect(roots.AppendCertsFromPEM(caPEM)).To(BeTrue())
			// Only the returned CA is trusted, and ServerName makes the client check the SAN for host.
			client := &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: host}}}

			response, err := client.Get(stub.URL() + "/api/v2/external/accounts?size=1")
			Expect(err).ToNot(HaveOccurred())
			_ = response.Body.Close()
			Expect(response.StatusCode).To(Equal(http.StatusOK))
			Expect(stub.Succeeded("HealthCheck")).To(Equal(1))
		},
		Entry("an IP address", "127.0.0.1"),
		Entry("a DNS name", "localhost"),
	)

	It("records no request when nothing calls it", func() {
		stub, _ := start(load(fixture))
		Expect(stub.Requests()).To(BeEmpty())
	})

	It("lets a running stub switch scenario and restarts failure counting", func() {
		stub, adapter := start(billingfake.Scenario{})
		Expect(adapter.HealthCheck(ctx)).To(Succeed())

		stub.SetScenario(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"HealthCheck": {Reason: billing.ReasonUnavailable}}})
		Expect(reasonOf(adapter.HealthCheck(ctx))).To(Equal(billing.ReasonUnavailable))
	})

	It("keeps instances isolated when used in parallel", func() {
		stubs := make([]*billingstub.Stub, 4)
		var wg sync.WaitGroup
		for i := range stubs {
			stub, adapter := start(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
				{ID: string(rune('a' + i)), Name: "n", State: billing.AccountActive, RawStatus: "Active"}}})
			stubs[i] = stub
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := adapter.GetAccount(ctx, billing.NewAccountRef(string(rune('a'+i))))
				Expect(err).ToNot(HaveOccurred())
			}()
		}
		wg.Wait()
		for _, stub := range stubs {
			Expect(stub.Requests()).To(HaveLen(1))
		}
	})
})
