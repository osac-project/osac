/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("M360 services", func() {
	var (
		fake    *fakeServer
		adapter billing.Adapter
		mu      sync.Mutex
		paths   []string
	)

	build := func(h http.HandlerFunc) {
		paths = nil
		fake = newFakeServer(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.EscapedPath())
			mu.Unlock()
			h(w, r)
		})
		var err error
		adapter, err = New(billing.Config{BaseURL: fake.URL, APIVersion: "v1", Credential: testToken}).
			SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
	}

	AfterEach(func() {
		fake.expectOnlyGET()
		mu.Lock()
		defer mu.Unlock()
		for _, p := range paths {
			Expect(p).ToNot(ContainSubstring("contract"))
		}
	})

	servicesBody := func(names ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			items := make([]string, 0, len(names))
			for i, n := range names {
				items = append(items, `{"Id":"svc-`+string(rune('a'+i))+`","Name":"`+n+`"}`)
			}
			_, _ = w.Write([]byte(listBody(0, true, `[`+strings.Join(items, ",")+`]`)))
		}
	}

	Describe("FindServiceByName", func() {
		It("returns the one exact match and searches with q", func() {
			var q string
			build(func(w http.ResponseWriter, r *http.Request) {
				q = r.URL.Query().Get("q")
				servicesBody("GPU Hours Extra", "GPU Hours")(w, r)
			})
			ref, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref.ID()).To(Equal("svc-b"), "must pick the exact match, not the first hit")
			Expect(q).To(Equal("GPU Hours"))
		})

		It("reports not-found when the provider returns no hits", func() {
			build(servicesBody())
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(err).To(MatchError(billing.ErrServiceNotFound))
		})

		It("reports not-found when only near-matches exist", func() {
			build(servicesBody("GPU Hours Extra", "gpu hours"))
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(err).To(MatchError(billing.ErrServiceNotFound))
		})

		It("gives up as malformed when the provider never reports a last page", func() {
			build(func(w http.ResponseWriter, r *http.Request) {
				n, _ := strconv.Atoi(r.URL.Query().Get("page"))
				_, _ = w.Write([]byte(listBody(n, false, `[]`)))
			})
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("tolerates a trailing slash on the configured base URL", func() {
			var path string
			build(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				servicesBody("GPU Hours")(w, r)
			})
			a, err := New(billing.Config{BaseURL: fake.URL + "/", APIVersion: "v1", Credential: testToken}).
				SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = a.FindServiceByName(context.Background(), "GPU Hours")
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/api/v1/external/services"))
		})

		It("reports ambiguity when two services match exactly", func() {
			build(servicesBody("GPU Hours", "GPU Hours"))
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(reasonOf(err)).To(Equal(billing.ReasonAmbiguous))
		})

		It("finds an exact match on a later result page using zero-based page and size", func() {
			var sizes []string
			build(func(w http.ResponseWriter, r *http.Request) {
				sizes = append(sizes, r.URL.Query().Get("size"))
				if r.URL.Query().Get("page") == "" {
					_, _ = w.Write([]byte(listBody(0, false, `[{"Id":"s1","Name":"Other"}]`)))
					return
				}
				Expect(r.URL.Query().Get("page")).To(Equal("1"))
				_, _ = w.Write([]byte(listBody(1, true, `[{"Id":"s2","Name":"GPU Hours"}]`)))
			})
			ref, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref.ID()).To(Equal("s2"))
			Expect(sizes).To(Equal([]string{"20", "20"}))
		})

		It("fails instead of reporting not-found when the body is not an M360 page", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"Items":[{"Id":"s1","Name":"GPU Hours"}]}`))
			})
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("surfaces provider failure instead of not-found", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
			_, err := adapter.FindServiceByName(context.Background(), "GPU Hours")
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnauthorized))
		})
	})

	Describe("ListPriceableAttributes", func() {
		It("reports NOT_FOUND when the service does not exist", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
			_, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))
		})

		It("returns the attribute ids and names, which rate rows reference by id", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[
					{"id":"d1","name":"gpu_hours","dimensionDataType":"NUMBER"},
					{"id":"d2","name":"storage_gb","dimensionDataType":"NUMBER"}]`)))
			})
			attrs, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("svc 1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(attrs).To(Equal([]billing.PriceableAttribute{{ID: "d1", Name: "gpu_hours"}, {ID: "d2", Name: "storage_gb"}}))
			Expect(paths).To(ConsistOf("/api/v1/external/services/svc%201/priceable-attributes"))
		})

		It("reads every page so no dimension is hidden past the first 20", func() {
			build(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "" {
					_, _ = w.Write([]byte(listBody(0, false, `[{"id":"d1","name":"a"}]`)))
					return
				}
				_, _ = w.Write([]byte(listBody(1, true, `[{"id":"d2","name":"b"}]`)))
			})
			attrs, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(err).ToNot(HaveOccurred())
			Expect(attrs).To(Equal([]billing.PriceableAttribute{{ID: "d1", Name: "a"}, {ID: "d2", Name: "b"}}))
		})

		It("gives up as malformed when the provider never reports a last page", func() {
			build(func(w http.ResponseWriter, r *http.Request) {
				n, _ := strconv.Atoi(r.URL.Query().Get("page"))
				_, _ = w.Write([]byte(listBody(n, false, `[]`)))
			})
			_, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("fails instead of returning no attributes when the body is not an M360 page", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"Items":[{"Name":"gpu_hours"}]}`))
			})
			attrs, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
			Expect(attrs).To(BeNil())
		})

		It("rejects an attribute without an id", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[{"id":"d1","name":"gpu_hours"},{"id":"","name":"storage_gb"}]`)))
			})
			attrs, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
			Expect(attrs).To(BeNil())
		})

		It("surfaces provider failure instead of an empty list", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
			attrs, err := adapter.ListPriceableAttributes(context.Background(), billing.NewServiceRef("s"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnauthorized))
			Expect(attrs).To(BeNil())
		})
	})

	Describe("GetServiceRateCard", func() {
		// Rate cards are not in the ApiResponse envelope: the card sits at the top level.
		It("returns every row with its price method and the unit rate exactly as sent", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"currency":"USD","versionNo":3,"isCurrent":true,"fromDate":"2026-01-01","rows":[
					{"id":"r1","label":"GPU","usageType":"Charge","priceDimId":"d1","priceMethod":"QUANTITY","unitRate":9.9E-9,
					 "attributeValues":[{"attributeName":"gpu_model","attributeValue":"H100"}]},
					{"id":"r2","label":"Tiered","priceDimId":"d2","priceMethod":"TIERED","unitRate":null,
					 "rateCardTiers":[{"tierNum":1,"tierMinVal":0,"tierMaxVal":10,"tierUnitRate":0.1}]},
					{"id":"r3","label":"None","priceDimId":"d3","priceMethod":"NO_PRICE"},
					{"id":"r4","label":"Plain","priceDimId":"d4","priceMethod":"QUANTITY","unitRate":0.1000000000000000055}]}`))
			})
			card, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(err).ToNot(HaveOccurred())
			Expect(paths).To(ConsistOf("/api/v1/external/services/s1/rate-cards/USD"))
			Expect(card.Currency).To(Equal("USD"))
			Expect(card.Rows).To(Equal([]billing.RateRow{
				{DimensionID: "d1", PriceMethod: "QUANTITY", UnitRate: "9.9E-9",
					AttributeValues: []billing.AttributeValue{{Name: "gpu_model", Value: "H100"}}},
				{DimensionID: "d2", PriceMethod: "TIERED"},
				{DimensionID: "d3", PriceMethod: "NO_PRICE"},
				{DimensionID: "d4", PriceMethod: "QUANTITY", UnitRate: "0.1000000000000000055"},
			}))
		})

		It("reports a missing card as not-found, never as a zero amount", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
			card, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "EUR")
			Expect(err).To(MatchError(billing.ErrRateCardNotFound))
			Expect(card.Rows).To(BeNil())
		})

		It("treats a card without rows as not-found", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"currency":"USD","rows":[]}`)) })
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(err).To(MatchError(billing.ErrRateCardNotFound))
		})

		It("rejects a row without a price method", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"currency":"USD","rows":[{"priceDimId":"d1","unitRate":1}]}`))
			})
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("rejects a row without a price dimension id", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"currency":"USD","rows":[
					{"priceDimId":"d1","priceMethod":"QUANTITY","unitRate":1},
					{"priceMethod":"TIERED"}]}`))
			})
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("rejects a card in a different currency than requested", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"currency":"EUR","rows":[{"priceDimId":"d","priceMethod":"QUANTITY","unitRate":1}]}`))
			})
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("rejects the old Dimension/Amount shape instead of returning rows without prices", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"Currency":"USD","Rows":[{"Dimension":"gpu_hours","Amount":"0.25"}]}`))
			})
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(err).To(HaveOccurred())
			Expect(err).ToNot(MatchError(billing.ErrRateCardNotFound))
		})

		It("rejects an empty currency without contacting the provider", func() {
			build(func(http.ResponseWriter, *http.Request) {})
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "")
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
			Expect(fake.hits.Load()).To(BeZero())
		})

		It("surfaces provider outage as an error", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
			_, err := adapter.GetServiceRateCard(context.Background(), billing.NewServiceRef("s1"), "USD")
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
		})
	})
})
