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
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("M360 invoice reader", func() {
	It("lists normalized invoice headers for one account and date range", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/api/v1/external/invoices"))
			Expect(request.URL.Query().Get("accountId")).To(Equal("provider-account-1"))
			Expect(request.URL.Query().Get("fromDate")).To(Equal("2026-08-01"))
			Expect(request.URL.Query().Get("toDate")).To(Equal("2026-08-31"))
			Expect(request.URL.Query().Get("page")).To(Equal("0"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[{"Id":"native-invoice-id","InvoiceNo":"INV-1001","ApprovalStatus__r":{"Name":"Approved"}}],"totalElements":1,"totalPages":1,"size":20,"number":0,"last":true}}`)
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		page, err := client.ListInvoices(context.Background(), billing.InvoiceQuery{
			AccountReference: "provider-account-1",
			FromDate:         "2026-08-01",
			ToDate:           "2026-08-31",
			Offset:           0,
			Limit:            20,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(int32(1)))
		Expect(page.Items).To(Equal([]billing.Invoice{{
			ID:             "INV-1001",
			Number:         "INV-1001",
			ApprovalStatus: "Approved",
		}}))
	})

	It("gets invoice details by the public invoice number without returning the native identifier", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v1/external/invoices":
				Expect(request.URL.Query().Get("q")).To(Equal("INV-1001"))
				Expect(request.URL.Query().Get("accountId")).To(Equal("provider-account-1"))
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"content":[{"Id":"native-invoice-id","InvoiceNo":"INV-1001"}],"totalElements":1,"totalPages":1,"size":20,"number":0,"last":true}}`)
			case "/api/v1/external/invoices/native-invoice-id":
				_, _ = fmt.Fprint(writer, `{"success":true,"status":200,"data":{"id":"native-invoice-id","invoiceNo":"INV-1001","invoiceDate":"2026-08-01","invoiceStatus":"Posted","amount":40.25,"taxAmount":4.025,"balance":44.275,"currency":"USD","dueDate":"2026-08-31","periodStart":"2026-08-01","periodEnd":"2026-08-31","paymentStatus":"Unpaid","approvalStatus":"Approved","items":[{"description":"Usage","volume":1000,"ratePerUnit":0.04025,"amount":40.25,"tax":4.025,"balance":44.275,"chargeType":"Usage","priceMethodType":"QUANTITY","billStartDate":"2026-08-01","billEndDate":"2026-08-31","currency":"USD"}]}}`)
			default:
				Fail("unexpected provider path: " + request.URL.Path)
			}
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		invoice, err := client.GetInvoice(context.Background(), "provider-account-1", "INV-1001")
		Expect(err).NotTo(HaveOccurred())
		Expect(invoice).To(Equal(billing.Invoice{
			ID:             "INV-1001",
			Number:         "INV-1001",
			Date:           "2026-08-01",
			Status:         "Posted",
			Amount:         "40.25",
			TaxAmount:      "4.025",
			Balance:        "44.275",
			CurrencyCode:   "USD",
			DueDate:        "2026-08-31",
			PeriodStart:    "2026-08-01",
			PeriodEnd:      "2026-08-31",
			PaymentStatus:  "Unpaid",
			ApprovalStatus: "Approved",
			Items: []billing.InvoiceLineItem{{
				Description:  "Usage",
				Volume:       "1000",
				RatePerUnit:  "0.04025",
				Amount:       "40.25",
				TaxAmount:    "4.025",
				Balance:      "44.275",
				ChargeType:   "Usage",
				PriceMethod:  "QUANTITY",
				PeriodStart:  "2026-08-01",
				PeriodEnd:    "2026-08-31",
				CurrencyCode: "USD",
			}},
		}))
	})

	It("applies offset and limit across provider pages", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.URL.Query().Get("page")).To(BeElementOf("0", "1"))
			page := request.URL.Query().Get("page")
			content := make([]map[string]any, 0)
			if page == "0" {
				for i := 0; i < invoicePageSize; i++ {
					content = append(content, map[string]any{"Id": fmt.Sprintf("native-%d", i), "InvoiceNo": fmt.Sprintf("INV-%d", i)})
				}
			} else {
				content = append(content,
					map[string]any{"Id": "native-20", "InvoiceNo": "INV-20"},
					map[string]any{"Id": "native-21", "InvoiceNo": "INV-21"},
				)
			}
			response := map[string]any{
				"success": true,
				"status":  200,
				"data": map[string]any{
					"content": content, "totalElements": 22, "totalPages": 2,
					"size": invoicePageSize, "number": page, "last": page == "1",
				},
			}
			writer.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(writer).Encode(response)).To(Succeed())
		}))
		DeferCleanup(server.Close)

		client, err := NewClient(Config{BaseURL: server.URL, AllowInsecureHTTP: true, APIKey: "test-token", APIVersion: "v1", HTTPClient: server.Client()})
		Expect(err).NotTo(HaveOccurred())

		page, err := client.ListInvoices(context.Background(), billing.InvoiceQuery{
			AccountReference: "provider-account-1",
			Offset:           19,
			Limit:            2,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Total).To(Equal(int32(22)))
		Expect(page.Items).To(Equal([]billing.Invoice{
			{ID: "INV-19", Number: "INV-19"},
			{ID: "INV-20", Number: "INV-20"},
		}))
	})
})
