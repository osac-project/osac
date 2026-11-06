/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingstub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

var ctx = context.Background()

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
func start(s billingfake.Scenario) (*Stub, billing.Adapter) {
	stub := New(s)
	ExpectWithOffset(1, stub.Start("127.0.0.1:0")).To(Succeed())
	DeferCleanup(stub.Close)
	adapter, err := m360.New(billing.Config{BaseURL: stub.URL() + "/api/v2/external", Credential: "tok", RetryCount: 3,
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

// captureDisplaced replaces the httptest constructor with one that records each listener httptest allocates, and
// restores it when the spec ends. That listener is the disposable one start must close.
func captureDisplaced() *[]net.Listener {
	var displaced []net.Listener
	original := newUnstartedServer
	newUnstartedServer = func(handler http.Handler) *httptest.Server {
		server := original(handler)
		displaced = append(displaced, server.Listener)
		return server
	}
	DeferCleanup(func() { newUnstartedServer = original })
	return &displaced
}

// expectClosed fails unless listener was closed, which File reports as net.ErrClosed.
func expectClosed(listener net.Listener) {
	tcp, ok := listener.(*net.TCPListener)
	ExpectWithOffset(1, ok).To(BeTrue(), "listener was %T", listener)
	file, err := tcp.File()
	if err == nil {
		_ = file.Close()
	}
	ExpectWithOffset(1, errors.Is(err, net.ErrClosed)).To(BeTrue(), "listener was not closed: %v", err)
}

// failingWriter fails every Write, as when the client goes away before the response is delivered.
type failingWriter struct{ *httptest.ResponseRecorder }

func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

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
		// The normalized state stays UNKNOWN until the status mapping rule is decided; the raw value is exposed.
		Expect(next.Accounts[0].State).To(Equal(billing.AccountUnknown))
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

	It("rejects and records every write with 405", func() {
		stub, _ := start(load(fixture))

		for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete} {
			request, err := http.NewRequest(method, stub.URL()+"/api/v2/external/accounts/a1", nil)
			Expect(err).ToNot(HaveOccurred())
			response, err := http.DefaultClient.Do(request)
			Expect(err).ToNot(HaveOccurred())
			_ = response.Body.Close()
			Expect(response.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		}
		Expect(stub.Writes()).To(HaveLen(4))
	})

	It("records the Authorization header of each request", func() {
		stub, _ := start(load(fixture))

		request, err := http.NewRequest(http.MethodGet, stub.URL()+"/api/v2/external/accounts?size=1", nil)
		Expect(err).ToNot(HaveOccurred())
		request.Header.Set("Authorization", "Bearer sent")
		response, err := http.DefaultClient.Do(request)
		Expect(err).ToNot(HaveOccurred())
		_ = response.Body.Close()
		Expect(stub.Requests()).To(ConsistOf(HaveField("Authorization", "Bearer sent")))
	})

	DescribeTable("serves TLS for the host with a certificate signed by the returned CA",
		func(host string) {
			stub := New(load(fixture))
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
		stubs := make([]*Stub, 4)
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

var _ = Describe("Billing stub lifecycle", func() {
	DescribeTable("closes the httptest listener start displaces",
		func(startTLS bool) {
			displaced := captureDisplaced()
			stub := New(billingfake.Scenario{})
			if startTLS {
				_, err := stub.StartTLS("127.0.0.1:0", "127.0.0.1")
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(stub.Start("127.0.0.1:0")).To(Succeed())
			}
			DeferCleanup(stub.Close)
			Expect(*displaced).To(HaveLen(1))
			expectClosed((*displaced)[0])
		},
		Entry("plain HTTP", false),
		Entry("TLS", true),
	)

	It("closes the displaced listener and keeps Close a no-op when the bind fails", func() {
		displaced := captureDisplaced()
		blocker, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = blocker.Close() })

		stub := New(billingfake.Scenario{})
		Expect(stub.Start(blocker.Addr().String())).To(HaveOccurred())
		Expect(*displaced).To(HaveLen(1))
		expectClosed((*displaced)[0])
		Expect(stub.server).To(BeNil())
		Expect(stub.Close).ToNot(Panic())

		// The stub is still usable: the failed bind left nothing behind.
		Expect(stub.Start("127.0.0.1:0")).To(Succeed())
		DeferCleanup(stub.Close)
		response, err := http.Get(stub.URL() + "/api/v2/external/accounts?size=1")
		Expect(err).ToNot(HaveOccurred())
		_ = response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
	})

	It("closes every displaced listener across repeated start and close cycles", func() {
		displaced := captureDisplaced()
		stub := New(billingfake.Scenario{})
		for range 10 {
			Expect(stub.Start("127.0.0.1:0")).To(Succeed())
			stub.Close()
		}
		Expect(*displaced).To(HaveLen(10))
		for _, listener := range *displaced {
			expectClosed(listener)
		}
	})

	It("retires the previous server when started again", func() {
		stub := New(billingfake.Scenario{})
		Expect(stub.Start("127.0.0.1:0")).To(Succeed())
		DeferCleanup(stub.Close)
		first := stub.URL()
		response, err := http.Get(first + "/api/v2/external/accounts?size=1")
		Expect(err).ToNot(HaveOccurred())
		_ = response.Body.Close()

		Expect(stub.Start("127.0.0.1:0")).To(Succeed())
		second := stub.URL()
		response, err = http.Get(second + "/api/v2/external/accounts?size=1")
		Expect(err).ToNot(HaveOccurred())
		_ = response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))

		// A reused port would make the old and new origins alike; otherwise the retired listener must refuse.
		if second != first {
			client := &http.Client{
				Transport: &http.Transport{DisableKeepAlives: true},
				Timeout:   time.Second,
			}
			Eventually(func() error {
				response, err := client.Get(first + "/api/v2/external/accounts?size=1")
				if err == nil {
					_ = response.Body.Close()
				}
				return err
			}).WithTimeout(5 * time.Second).WithPolling(50 * time.Millisecond).Should(HaveOccurred())
		}
	})
})

var _ = Describe("Billing stub responses", func() {
	DescribeTable("answers an unencodable rate card with a flat 500 and records a failure",
		func(unitRate string) {
			stub := New(billingfake.Scenario{Services: []billingfake.ServiceSpec{{
				ID:   "s",
				Name: "n",
				Rates: map[string]billingfake.RateCardSpec{
					"USD": {Rows: []billing.RateRow{{DimensionID: "d", PriceMethod: "QUANTITY", UnitRate: unitRate}}},
				},
			}}})
			Expect(stub.Start("127.0.0.1:0")).To(Succeed())
			DeferCleanup(stub.Close)

			path := "/api/v2/external/services/s/rate-cards/USD"
			response, err := http.Get(stub.URL() + path)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = response.Body.Close() }()
			Expect(response.StatusCode).To(Equal(http.StatusInternalServerError))
			raw, err := io.ReadAll(response.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(raw)).ToNot(ContainSubstring(unitRate), "the scenario value must not be echoed")

			var body struct {
				Status  int
				Error   string
				Message string
				Path    string
			}
			Expect(json.Unmarshal(raw, &body)).To(Succeed())
			Expect(body.Status).To(Equal(http.StatusInternalServerError))
			Expect(body.Error).ToNot(BeEmpty())
			Expect(body.Message).ToNot(BeEmpty())
			Expect(body.Path).To(Equal(path))

			Expect(stub.Count("GetServiceRateCard")).To(Equal(1))
			Expect(stub.Succeeded("GetServiceRateCard")).To(Equal(0))
			Expect(stub.Requests()).To(ConsistOf(HaveField("Failed", true)))
		},
		Entry("letters", "not-a-number"),
		Entry("an empty exponent", "1e"),
		Entry("an explicit plus sign", "+1"),
		Entry("a decimal comma", "1,5"),
	)

	It("marks a request failed when the response cannot be delivered", func() {
		stub := New(billingfake.Scenario{})
		writer := &failingWriter{ResponseRecorder: httptest.NewRecorder()}
		request := httptest.NewRequest(http.MethodGet, "/api/v2/external/accounts?size=1", nil)

		Expect(func() { stub.ServeHTTP(writer, request) }).ToNot(Panic())
		Expect(writer.Code).To(Equal(http.StatusOK))
		Expect(stub.Count("HealthCheck")).To(Equal(1))
		Expect(stub.Succeeded("HealthCheck")).To(Equal(0))
		Expect(stub.Requests()).To(ConsistOf(SatisfyAll(
			HaveField("Op", "HealthCheck"),
			HaveField("Failed", true),
		)))
	})
})
