/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("M360 billing account client", func() {
	It("reports the provider as available when the account API responds successfully", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/v1/external/accounts"))
			Expect(request.URL.Query().Get("size")).To(Equal("1"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[],"totalElements":0,"totalPages":0,"size":1,"number":0,"last":true}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		status, err := client.GetStatus(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(billing.ProviderStateAvailable))
		Expect(status.UsageDeliveryState).To(Equal(billing.UsageDeliveryStateUnknown))
		Expect(status.Message).To(ContainSubstring("pricing, costs, and invoices are configured but not probed"))
		Expect(status.Message).To(ContainSubstring("usage delivery is not monitored by fulfillment"))
		Expect(status.Capabilities).To(Equal([]billing.ProviderCapability{
			billing.ProviderCapabilityPricing,
			billing.ProviderCapabilityCosts,
			billing.ProviderCapabilityInvoices,
		}))
	})

	It("retrieves one account by its provider identifier", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/v1/external/accounts/account-7"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"Id":"account-7","Name":"Acme","ExternalId":"tenant-a","Status__r":{"Name":"Active"}}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		account, err := client.GetAccount(context.Background(), "account-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(account).To(Equal(billing.Account{
			Reference:   "account-7",
			DisplayName: "Acme",
			ExternalID:  "tenant-a",
			State:       billing.AccountStateActive,
		}))
	})

	DescribeTable("classifies account read failures for billing coverage",
		func(statusCode int, body string, want error) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(statusCode)
				_, _ = fmt.Fprint(writer, body)
			}))
			DeferCleanup(server.Close)

			client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
			Expect(err).NotTo(HaveOccurred())
			_, err = client.GetAccount(context.Background(), "account-7")
			Expect(errors.Is(err, want)).To(BeTrue())
			Expect(errors.Is(err, billing.ErrProviderUnavailable)).To(Equal(errors.Is(want, billing.ErrProviderUnavailable)))
		},
		Entry("missing account", http.StatusNotFound, "", billing.ErrAccountNotFound),
		Entry("rate limit", http.StatusTooManyRequests, "", billing.ErrProviderUnavailable),
		Entry("provider failure", http.StatusServiceUnavailable, "", billing.ErrProviderUnavailable),
	)

	DescribeTable("retries transient provider read failures",
		func(statusCode int) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests++
				if requests == 1 {
					writer.WriteHeader(statusCode)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[],"totalElements":0,"totalPages":0,"size":1,"number":0,"last":true}}`)
			}))
			DeferCleanup(server.Close)

			client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
			Expect(err).NotTo(HaveOccurred())
			_, err = billing.RetryProviderRead(context.Background(), client.GetStatus)
			Expect(err).NotTo(HaveOccurred())
			Expect(requests).To(Equal(2))
		},
		Entry("HTTP request timeout", http.StatusRequestTimeout),
		Entry("rate limit", http.StatusTooManyRequests),
		Entry("server failure", http.StatusInternalServerError),
	)

	DescribeTable("does not retry permanent provider read failures",
		func(statusCode int, body string) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests++
				if statusCode != http.StatusOK {
					writer.WriteHeader(statusCode)
				}
				_, _ = fmt.Fprint(writer, body)
			}))
			DeferCleanup(server.Close)

			client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
			Expect(err).NotTo(HaveOccurred())
			_, err = billing.RetryProviderRead(context.Background(), client.GetStatus)
			Expect(err).To(HaveOccurred())
			Expect(requests).To(Equal(1))
		},
		Entry("bad request", http.StatusBadRequest, ``),
		Entry("invalid JSON", http.StatusOK, `{"success":true,"data":`),
	)

	It("retries a provider transport timeout while the operation context remains live", func() {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				time.Sleep(50 * time.Millisecond)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[],"totalElements":0,"totalPages":0,"size":1,"number":0,"last":true}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1",
			HTTPClient: &http.Client{Timeout: 10 * time.Millisecond},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = billing.RetryProviderRead(context.Background(), client.GetStatus)
		Expect(err).NotTo(HaveOccurred())
		Expect(requests.Load()).To(Equal(int32(2)))
	})

	It("keeps a malformed account response separate from provider outage", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"data":{}}`)
		}))
		DeferCleanup(server.Close)
		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.GetAccount(context.Background(), "account-7")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, billing.ErrProviderUnavailable)).To(BeFalse())
	})

	It("keeps a rejected account request separate from provider outage", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusBadRequest)
		}))
		DeferCleanup(server.Close)
		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.GetAccount(context.Background(), "account-7")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, billing.ErrProviderUnavailable)).To(BeFalse())
	})

	It("updates only the external ID scalar field on an account", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.URL.Path).To(Equal("/api/v1/external/accounts/account-7"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer test-token"))
			if request.Method == http.MethodGet {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"Id":"account-7","Name":"Acme","ExternalId":"tenant-old"}}`)
				return
			}
			Expect(request.Method).To(Equal(http.MethodPut))
			body := make(map[string]any)
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body).To(Equal(map[string]any{"ExternalId": "tenant-a"}))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"Id":"account-7","Name":"Acme","ExternalId":"tenant-a"}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(client.UpdateAccountExternalID(context.Background(), "account-7", "tenant-old", "tenant-a")).To(Succeed())
	})

	It("retries the account read before updating its external ID", func() {
		getAttempts := 0
		putAttempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.URL.Path).To(Equal("/api/v1/external/accounts/account-7"))
			if request.Method == http.MethodGet {
				getAttempts++
				if getAttempts == 1 {
					http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"Id":"account-7","Name":"Acme","ExternalId":"tenant-old"}}`)
				return
			}
			Expect(request.Method).To(Equal(http.MethodPut))
			putAttempts++
			writer.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(client.UpdateAccountExternalID(context.Background(), "account-7", "tenant-old", "tenant-a")).To(Succeed())
		Expect(getAttempts).To(Equal(2))
		Expect(putAttempts).To(Equal(1))
	})

	It("does not update an account whose external ID changed since it was read", func() {
		putRequested := false
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodPut {
				putRequested = true
				return
			}
			Expect(request.Method).To(Equal(http.MethodGet))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"Id":"account-7","Name":"Acme","ExternalId":"tenant-b"}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		err = client.UpdateAccountExternalID(context.Background(), "account-7", "tenant-a", "tenant-c")
		Expect(errors.Is(err, billing.ErrAccountExternalIDChanged)).To(BeTrue())
		Expect(putRequested).To(BeFalse())
	})

	It("lists all accounts across provider pages and normalizes their status", func() {
		var requestedPages []string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/v1/external/accounts"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer test-token"))
			Expect(request.URL.Query().Get("size")).To(Equal("20"))
			requestedPages = append(requestedPages, request.URL.Query().Get("page"))

			page := request.URL.Query().Get("page")
			writer.Header().Set("Content-Type", "application/json")
			if page == "0" {
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[{"Id":"account-1","Name":"Acme","ExternalId":"tenant-a","Status__r":{"Name":"Active"}}],"totalElements":2,"totalPages":2,"size":20,"number":0,"last":false}}`)
				return
			}
			_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[{"Id":"account-2","Name":"Northwind","ExternalId":"tenant-b","Status__r":{"Name":"Inactive"}}],"totalElements":2,"totalPages":2,"size":20,"number":1,"last":true}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		accounts, err := client.ListAccounts(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(requestedPages).To(Equal([]string{"0", "1"}))
		Expect(accounts).To(Equal([]billing.Account{
			{Reference: "account-1", DisplayName: "Acme", ExternalID: "tenant-a", State: billing.AccountStateActive},
			{Reference: "account-2", DisplayName: "Northwind", ExternalID: "tenant-b", State: billing.AccountStateInactive},
		}))
	})

	It("lists provider-neutral rows from an OSAC-matched M360 service rate card", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				Expect(request.Method).To(Equal(http.MethodGet))
				Expect(request.URL.Query().Get("page")).To(Equal("0"))
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[{"Id":"service-1","Name":"Compute","Code":"compute_instance"}],"totalElements":1,"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				Expect(request.Method).To(Equal(http.MethodGet))
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"id":"service-1","name":"Compute","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceDimId":"dimension-1","priceMethod":"FLAT_RATE","flatRate":4.50,"attributeValues":[{"attributeName":"vm_image_ref","attributeValue":"fedora-16"}]}]}]}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			Service:      "compute_instance",
			CurrencyCode: "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(Equal([]billing.RateCardItem{{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Source:       billing.RateCardSourceDefault,
			Conditions: []billing.RateCondition{{
				Dimension: "image_ref",
				Operator:  billing.RateMatchEquals,
				Value:     "fedora-16",
			}},
			Amount: "4.50",
			Unit:   billing.RateUnitMonth,
		}}))
	})

	It("uses the active account contract's service rate card when an exception exists", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"FLAT_RATE","flatRate":99.0,"attributeValues":[] }]}]}}`)
			case "/api/v1/external/contracts":
				Expect(request.URL.Query().Get("page")).To(Equal("0"))
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-1","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"id":"contract-service-1","serviceId":"service-1","startDate":"2000-01-01","exceptionPricing":true}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services/contract-service-1/rate-cards/USD":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"QUANTITY","unitRate":2.75,"attributeValues":[{"attributeName":"vm_image_ref","attributeValue":"fedora-16"}]}]}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(Equal([]billing.RateCardItem{{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Source:       billing.RateCardSourceContract,
			Conditions: []billing.RateCondition{{
				Dimension: "image_ref",
				Operator:  billing.RateMatchEquals,
				Value:     "fedora-16",
			}},
			Amount: "2.75",
			Unit:   billing.RateUnitHour,
		}}))
	})

	It("uses the default card when a complete contract list has no contract for the account", func() {
		defaultCardRequested := false
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts":
				Expect(request.URL.Query().Get("accountId")).To(BeEmpty())
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-other","MasterAccountId":"account-other","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				defaultCardRequested = true
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"FLAT_RATE","flatRate":3.00,"attributeValues":[]}]}]}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(defaultCardRequested).To(BeTrue())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Source).To(Equal(billing.RateCardSourceDefault))
		Expect(items[0].Amount).To(Equal("3.00"))
	})

	It("does not use the default card when listing account contracts fails", func() {
		defaultCardRequested := false
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts":
				writer.WriteHeader(http.StatusServiceUnavailable)
			case "/api/v1/external/services/service-1":
				defaultCardRequested = true
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[]}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		_, err = client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(errors.Is(err, billing.ErrProviderUnavailable)).To(BeTrue())
		Expect(defaultCardRequested).To(BeFalse())
	})

	DescribeTable("does not use the default card when contract pagination is incomplete",
		func(contractPage string) {
			defaultCardRequested := false
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/api/v1/external/services":
					_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
				case "/api/v1/external/contracts":
					_, _ = fmt.Fprint(writer, contractPage)
				case "/api/v1/external/services/service-1":
					defaultCardRequested = true
					_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[]}}`)
				default:
					Fail("unexpected M360 request path: " + request.URL.Path)
				}
			}))
			DeferCleanup(server.Close)

			client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
			Expect(err).NotTo(HaveOccurred())

			_, err = client.ListRateCardItems(context.Background(), billing.RateCardQuery{
				AccountReference: "account-1",
				Service:          "compute_instance",
				CurrencyCode:     "USD",
			})
			Expect(err).To(HaveOccurred())
			Expect(defaultCardRequested).To(BeFalse())
		},
		Entry("empty nonterminal page", `{"success":true,"data":{"content":[],"totalPages":2,"last":false}}`),
		Entry("missing total-pages metadata", `{"success":true,"data":{"content":[{"Id":"contract-other","MasterAccountId":"account-other","ApprovalStatus__r":{"Name":"Approved"}}],"totalPages":0,"last":false}}`),
	)

	It("skips a non-current exception card without falling back to the catalog card", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"FLAT_RATE","flatRate":99.0,"attributeValues":[]}]}]}}`)
			case "/api/v1/external/contracts":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-1","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"id":"contract-service-1","serviceId":"service-1","startDate":"2000-01-01","exceptionPricing":true}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services/contract-service-1/rate-cards/USD":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"currency":"USD","isCurrent":false,"rows":[{"priceMethod":"QUANTITY","unitRate":2.75,"attributeValues":[{"attributeName":"vm_image_ref","attributeValue":"fedora-16"}]}]}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(BeEmpty())
	})

	It("returns an error when the account has multiple active contracts", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-1","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"},{"Id":"contract-2","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		_, err = client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).To(MatchError(ContainSubstring("multiple applicable account contracts")))
	})

	It("uses the service catalog card when the applicable contract has no service exception", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-1","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"QUANTITY","unitRate":4.5,"attributeValues":[]}]}]}}`)
			case "/api/v1/external/contracts/contract-1/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"id":"contract-service-1","serviceId":"service-1","startDate":"2000-01-01","exceptionPricing":false}],"totalPages":1,"last":true}}`)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Source).To(Equal(billing.RateCardSourceContract))
		Expect(items[0].Amount).To(Equal("4.5"))
	})

	It("does not use the catalog card when an exception-priced contract rate card is missing", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"service-1","Code":"compute_instance"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/services/service-1":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"id":"service-1","code":"compute_instance","rateCards":[{"currency":"USD","isCurrent":true,"rows":[{"priceMethod":"FLAT_RATE","flatRate":99.0,"attributeValues":[]}] }]}}`)
			case "/api/v1/external/contracts":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"Id":"contract-1","MasterAccountId":"account-1","ApprovalStatus__r":{"Name":"Approved"},"StartDate":"2000-01-01"}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services":
				_, _ = fmt.Fprint(writer, `{"success":true,"data":{"content":[{"id":"contract-service-1","serviceId":"service-1","startDate":"2000-01-01","exceptionPricing":true}],"totalPages":1,"last":true}}`)
			case "/api/v1/external/contracts/contract-1/services/contract-service-1/rate-cards/USD":
				writer.WriteHeader(http.StatusNotFound)
			default:
				Fail("unexpected M360 request path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListRateCardItems(context.Background(), billing.RateCardQuery{
			AccountReference: "account-1",
			Service:          "compute_instance",
			CurrencyCode:     "USD",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(BeEmpty())
	})

	It("maps M360 VM attribute names to OSAC billing dimensions", func() {
		item, err := normalizeRateCardItem("compute_instance", "USD", billing.RateCardSourceDefault, m360RateCardRow{
			PriceMethod: "FLAT_RATE",
			FlatRate:    json.Number("4.50"),
			AttributeValues: []m360RateAttribute{
				{AttributeName: "vm_image_ref", AttributeValue: "fedora-16"},
				{AttributeName: "boot_disk_size", AttributeValue: "*"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(item.Conditions).To(Equal([]billing.RateCondition{
			{Dimension: "image_ref", Operator: billing.RateMatchEquals, Value: "fedora-16"},
			{Dimension: "boot_disk_size_gib", Operator: billing.RateMatchAny},
		}))
	})

	It("binds quantity rates to the node count without scaling flat rates", func() {
		row := m360RateCardRow{
			PriceMethod: "QUANTITY", UnitRate: json.Number("2.75"),
			AttributeValues: []m360RateAttribute{{AttributeName: "node_count", AttributeValue: "*"}},
		}
		item, err := normalizeRateCardItem(billing.ServiceCluster, "USD", billing.RateCardSourceDefault, row)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.QuantityDimension).To(Equal("node_count"))
		Expect(item.Unit).To(Equal(billing.RateUnitHour))

		row.PriceMethod = "FLAT_RATE"
		row.FlatRate = json.Number("6")
		item, err = normalizeRateCardItem(billing.ServiceCluster, "USD", billing.RateCardSourceDefault, row)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.QuantityDimension).To(BeEmpty())
	})

	It("does not include the provider response body in errors", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(writer, `{"message":"sensitive account data"}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{
			BaseURL:    server.URL,
			APIKey:     "test-token",
			APIVersion: "v1",
			HTTPClient: server.Client(),
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = client.ListAccounts(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("sensitive account data"))
	})
})
