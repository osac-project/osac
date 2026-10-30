/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
)

const testContractToken = "super-secret-token"

// m360Server is a minimal M360 API fake for the contract suite; the transport-level fake lives with the m360 package.
type m360Server struct {
	*httptest.Server
	methods chan string
}

// newM360Server starts an M360 fake that records every request method.
func newM360Server(h http.HandlerFunc) *m360Server {
	s := &m360Server{methods: make(chan string, 1000)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.methods <- r.Method
		h(w, r)
	}))
	return s
}

// contractReasonOf asserts err is a *billing.Error and returns its Reason.
func contractReasonOf(err error) billing.Reason {
	var e *billing.Error
	ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue(), "error was %v", err)
	return e.Reason
}

// Both adapters are seeded with the same logical data: one active account "acct-1" correlated to "ext-1", and a
// service "GPU Hours" priced at a 0.25 USD quantity rate for gpu_hours.
func seededFake() *fakeAdapter {
	return &fakeAdapter{
		accounts: map[string]*fakeNativeAccount{"acct-1": {uuid: "acct-1", state: "enabled", approval: "Approved", tag: "ext-1"}},
		order:    []string{"acct-1"},
		services: map[string]string{"svc-1": "GPU Hours", "svc-2": "GPU Hours Extra"},
		rates:    map[string]map[string]string{"svc-1": {"USD": "0.25"}},
	}
}

// seededM360 returns an M360 fake and adapter serving the shared contract data.
func seededM360() (*m360Server, billing.Adapter) {
	fake := newM360Server(func(w http.ResponseWriter, r *http.Request) {
		page := func(content string) string {
			return `{"success":true,"status":200,"errors":[],"data":{"content":` + content + `,"number":0,"size":20,"last":true}}`
		}
		const account = `{"Id":"acct-1","Name":"Acme","ExternalId":"ext-1","Status__r":{"Name":"Active"},"ApprovalStatus__r":{"Name":"Approved"}}`
		switch p := strings.TrimPrefix(r.URL.Path, "/api/v1/external"); p {
		case "/accounts":
			_, _ = w.Write([]byte(page(`[` + account + `]`)))
		case "/accounts/acct-1":
			_, _ = w.Write([]byte(`{"success":true,"status":200,"errors":[],"data":` + account + `}`))
		case "/services":
			_, _ = w.Write([]byte(page(`[{"Id":"svc-1","Name":"GPU Hours"},{"Id":"svc-2","Name":"GPU Hours Extra"}]`)))
		case "/services/svc-1/priceable-attributes":
			_, _ = w.Write([]byte(page(`[{"id":"dim-1","name":"gpu_hours","dimensionDataType":"NUMBER"}]`)))
		case "/services/svc-1/rate-cards/USD":
			_, _ = w.Write([]byte(`{"currency":"USD","rows":[{"priceDimId":"dim-1","priceMethod":"QUANTITY","unitRate":0.25}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	adapter, err := m360.New(billing.Config{BaseURL: fake.URL, APIVersion: "v1", Credential: testContractToken}).
		SetLogger(logger).Build()
	Expect(err).ToNot(HaveOccurred())
	return fake, adapter
}

var _ = Describe("Adapter contract", func() {
	type provider struct {
		name      string
		build     func() billing.Adapter
		canWrite  bool
		rawStatus string // the provider-native status text, kept raw for the caller to map
		afterEach func()
	}
	var m360Fake *m360Server

	providers := []provider{
		{
			name:      "M360",
			rawStatus: "Active",
			build: func() billing.Adapter {
				var a billing.Adapter
				m360Fake, a = seededM360()
				return a
			},
			afterEach: func() {
				m360Fake.Close()
				close(m360Fake.methods)
				for m := range m360Fake.methods {
					Expect(m).To(Equal(http.MethodGet), "M360 must never receive a write")
				}
			},
		},
		{name: "write-capable fake", build: func() billing.Adapter { return seededFake() }, canWrite: true, rawStatus: "enabled"},
	}

	for _, p := range providers {
		Describe(p.name, func() {
			var adapter billing.Adapter
			ctx := context.Background()

			BeforeEach(func() { adapter = p.build() })
			AfterEach(func() {
				if p.afterEach != nil {
					p.afterEach()
				}
			})

			It("lists accounts in the normalized shape", func() {
				page, err := adapter.ListAccounts(ctx, billing.AccountFilter{}, billing.Page{})
				Expect(err).ToNot(HaveOccurred())
				Expect(page.Accounts).To(HaveLen(1))
				a := page.Accounts[0]
				Expect(a.Ref.ID()).To(Equal("acct-1"))
				Expect(a.RawStatus).To(Equal(p.rawStatus))
				Expect(a.ExternalID).To(Equal("ext-1"))
				Expect(a.ApprovalStatus).To(Equal("Approved"))
			})

			It("reports a missing account as not-found", func() {
				_, err := adapter.GetAccount(ctx, billing.NewAccountRef("nope"))
				Expect(contractReasonOf(err)).To(Equal(billing.ReasonNotFound))
			})

			It("reads the correlation value of an account", func() {
				v, ok, err := adapter.CorrelationValue(ctx, billing.NewAccountRef("acct-1"))
				Expect(err).ToNot(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(v).To(Equal("ext-1"))
			})

			It("resolves a service by exact name and reads its rate card", func() {
				svc, err := adapter.FindServiceByName(ctx, "GPU Hours")
				Expect(err).ToNot(HaveOccurred())
				attrs, err := adapter.ListPriceableAttributes(ctx, svc)
				Expect(err).ToNot(HaveOccurred())
				Expect(attrs).To(Equal([]billing.PriceableAttribute{{ID: "dim-1", Name: "gpu_hours"}}))
				card, err := adapter.GetServiceRateCard(ctx, svc, "USD")
				Expect(err).ToNot(HaveOccurred())
				Expect(card).To(Equal(billing.RateCard{Currency: "USD",
					Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}}}))
			})

			It("reports an unknown service as not-found", func() {
				_, err := adapter.FindServiceByName(ctx, "Nope")
				Expect(err).To(MatchError(billing.ErrServiceNotFound))
			})

			It("reports a missing rate card as not-found", func() {
				_, err := adapter.GetServiceRateCard(ctx, billing.NewServiceRef("svc-1"), "EUR")
				Expect(err).To(MatchError(billing.ErrRateCardNotFound))
			})

			It("honours the correlation write capability it advertises", func() {
				Expect(adapter.SupportsCorrelationWrite()).To(Equal(p.canWrite))
				err := adapter.SetCorrelationValue(ctx, billing.NewAccountRef("acct-1"), "ext-2")
				if !p.canWrite {
					Expect(contractReasonOf(err)).To(Equal(billing.ReasonUnsupported))
					return
				}
				Expect(err).ToNot(HaveOccurred())
				v, _, err := adapter.CorrelationValue(ctx, billing.NewAccountRef("acct-1"))
				Expect(err).ToNot(HaveOccurred())
				Expect(v).To(Equal("ext-2"))
			})
		})
	}
})
