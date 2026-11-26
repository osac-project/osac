/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingfake_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

var ctx = context.Background()

// reasonOf asserts err is a *billing.Error and returns its Reason.
func reasonOf(err error) billing.Reason {
	var e *billing.Error
	ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue(), "error was %v", err)
	return e.Reason
}

var _ = Describe("Billing fake", func() {
	Describe("accounts", func() {
		accounts := billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a1", Name: "one", ExternalID: "ext", State: billing.AccountActive, RawStatus: "active"},
			{ID: "a2", Name: "two", ExternalID: "ext", State: billing.AccountInactive, RawStatus: "suspended"},
			{ID: "a3", Name: "three", State: billing.AccountUnknown, RawStatus: "weird"},
		}}

		It("reports active, inactive and unknown states with raw status", func() {
			f := billingfake.New(accounts)
			for id, state := range map[string]billing.AccountState{
				"a1": billing.AccountActive, "a2": billing.AccountInactive, "a3": billing.AccountUnknown} {
				a, err := f.GetAccount(ctx, billing.NewAccountRef(id))
				Expect(err).ToNot(HaveOccurred())
				Expect(a.State).To(Equal(state))
			}
			a, _ := f.GetAccount(ctx, billing.NewAccountRef("a3"))
			Expect(a.RawStatus).To(Equal("weird"))
		})

		DescribeTable("lists zero, one or two accounts carrying a correlation value",
			func(value string, want int) {
				page, err := billingfake.New(accounts).ListAccounts(ctx,
					billing.AccountFilter{ExternalID: value}, billing.Page{})
				Expect(err).ToNot(HaveOccurred())
				Expect(page.Accounts).To(HaveLen(want))
				Expect(page.Next).To(BeEmpty())
			},
			Entry("zero", "nobody", 0),
			Entry("two", "ext", 2),
		)

		It("lists exactly one account by name", func() {
			page, _ := billingfake.New(accounts).ListAccounts(ctx, billing.AccountFilter{Name: "two"}, billing.Page{})
			Expect(page.Accounts).To(HaveLen(1))
		})

		It("pages with a token until Next is empty", func() {
			f := billingfake.New(accounts)
			first, err := f.ListAccounts(ctx, billing.AccountFilter{}, billing.Page{Size: 2})
			Expect(err).ToNot(HaveOccurred())
			Expect(first.Accounts).To(HaveLen(2))
			Expect(first.Next).ToNot(BeEmpty())
			second, err := f.ListAccounts(ctx, billing.AccountFilter{}, billing.Page{Size: 2, Token: first.Next})
			Expect(err).ToNot(HaveOccurred())
			Expect(second.Accounts).To(HaveLen(1))
			Expect(second.Next).To(BeEmpty())
		})

		It("fails GetAccount with NOT_FOUND for an unknown account", func() {
			_, err := billingfake.New(accounts).GetAccount(ctx, billing.NewAccountRef("zzz"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))
		})

		It("fails a provider read with the scripted reason", func() {
			s := accounts
			s.Failures = map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable, Status: 503}}
			_, err := billingfake.New(s).ListAccounts(ctx, billing.AccountFilter{}, billing.Page{})
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
		})
	})

	Describe("correlation", func() {
		scenario := billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "has", ExternalID: "v", State: billing.AccountActive},
			{ID: "none", State: billing.AccountActive},
		}}

		It("returns present and absent provider values", func() {
			f := billingfake.New(scenario)
			v, ok, err := f.CorrelationValue(ctx, billing.NewAccountRef("has"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("v"))
			_, ok, err = f.CorrelationValue(ctx, billing.NewAccountRef("none"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse())
			_, _, err = f.CorrelationValue(ctx, billing.NewAccountRef("zzz"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))
		})

		It("records failed and successful reads without changing provider bytes", func() {
			s := scenario
			s.Failures = map[string]billingfake.Failure{
				"CorrelationValue": {Reason: billing.ReasonTimeout, Retryable: true, Times: 1}}
			f := billingfake.New(s)
			ref := billing.NewAccountRef("has")
			_, _, err := f.CorrelationValue(ctx, ref)
			Expect(reasonOf(err)).To(Equal(billing.ReasonTimeout))
			value, present, err := f.CorrelationValue(ctx, ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(present).To(BeTrue())
			Expect(value).To(Equal("v"))
			Expect(f.Count("CorrelationValue")).To(Equal(2))
			Expect(f.Succeeded("CorrelationValue")).To(Equal(1))
		})
	})

	Describe("services and rates", func() {
		scenario := billingfake.Scenario{Services: []billingfake.ServiceSpec{
			{ID: "s1", Name: "GPU Hours", Attributes: []billing.PriceableAttribute{{ID: "dim-1", Name: "gpu_hours"}},
				Rates: map[string]billingfake.RateCardSpec{
					"USD": {Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}}},
					"EUR": {Currency: "USD", Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.2"}}},
				}},
			{ID: "s2", Name: "GPU Hours Extra"},
			{ID: "d1", Name: "Dup"}, {ID: "d2", Name: "Dup"},
		}}

		It("finds by exact name only", func() {
			f := billingfake.New(scenario)
			ref, err := f.FindServiceByName(ctx, "GPU Hours")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref.ID()).To(Equal("s1"))
			_, err = f.FindServiceByName(ctx, "gpu hours")
			Expect(errors.Is(err, billing.ErrServiceNotFound)).To(BeTrue())
			_, err = f.FindServiceByName(ctx, "Dup")
			Expect(reasonOf(err)).To(Equal(billing.ReasonAmbiguous))
		})

		It("lists priceable attributes and serves scripted rate cards", func() {
			f := billingfake.New(scenario)
			ref := billing.NewServiceRef("s1")
			attrs, err := f.ListPriceableAttributes(ctx, ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(attrs).To(ConsistOf(billing.PriceableAttribute{ID: "dim-1", Name: "gpu_hours"}))
			card, err := f.GetServiceRateCard(ctx, ref, "USD")
			Expect(err).ToNot(HaveOccurred())
			Expect(card).To(Equal(billing.RateCard{Currency: "USD",
				Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}}}))
		})

		It("returns a card in another currency than requested for a currency mismatch", func() {
			card, err := billingfake.New(scenario).GetServiceRateCard(ctx, billing.NewServiceRef("s1"), "EUR")
			Expect(err).ToNot(HaveOccurred())
			Expect(card.Currency).To(Equal("USD"))
		})

		It("has no rate card for an unscripted currency", func() {
			_, err := billingfake.New(scenario).GetServiceRateCard(ctx, billing.NewServiceRef("s1"), "JPY")
			Expect(errors.Is(err, billing.ErrRateCardNotFound)).To(BeTrue())
		})

		DescribeTable("fails any operation with the scripted failure",
			func(op string, reason billing.Reason, call func(billing.Adapter) error) {
				s := scenario
				s.Failures = map[string]billingfake.Failure{op: {Reason: reason}}
				Expect(reasonOf(call(billingfake.New(s)))).To(Equal(reason))
			},
			Entry("timeout", "GetServiceRateCard", billing.ReasonTimeout, func(a billing.Adapter) error {
				_, err := a.GetServiceRateCard(ctx, billing.NewServiceRef("s1"), "USD")
				return err
			}),
			Entry("5xx", "FindServiceByName", billing.ReasonUnavailable, func(a billing.Adapter) error {
				_, err := a.FindServiceByName(ctx, "GPU Hours")
				return err
			}),
			Entry("attributes", "ListPriceableAttributes", billing.ReasonUnavailable, func(a billing.Adapter) error {
				_, err := a.ListPriceableAttributes(ctx, billing.NewServiceRef("s1"))
				return err
			}),
			Entry("health", "HealthCheck", billing.ReasonUnauthorized, func(a billing.Adapter) error {
				return a.HealthCheck(ctx)
			}),
		)
	})

	Describe("call recording", func() {
		It("records every call in order and nothing for an untouched fake", func() {
			f := billingfake.New(billingfake.Scenario{})
			Expect(f.Calls()).To(BeEmpty())
			_ = f.HealthCheck(ctx)
			_, _ = f.FindServiceByName(ctx, "x")
			Expect(f.Calls()).To(HaveLen(2))
			Expect(f.Calls()[0].Op).To(Equal("HealthCheck"))
			Expect(f.Count("FindServiceByName")).To(Equal(1))
		})

		It("makes no call when a disabled provider holds the fake", func() {
			f := billingfake.New(billingfake.Scenario{})
			p := f.Provider()
			Expect(p.IsConfigured()).To(BeTrue())
			Expect(p.Key()).To(Equal("fake"))
			Expect(f.Calls()).To(BeEmpty())
		})
	})

	Describe("isolation and fixtures", func() {
		It("keeps scenario state per instance under parallel use", func() {
			a := billingfake.New(billingfake.Scenario{
				Accounts: []billingfake.AccountSpec{{ID: "x", ExternalID: "A", State: billing.AccountActive}}})
			b := billingfake.New(billingfake.Scenario{
				Accounts: []billingfake.AccountSpec{{ID: "x", ExternalID: "B", State: billing.AccountActive}}})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				account, err := a.GetAccount(ctx, billing.NewAccountRef("x"))
				Expect(err).ToNot(HaveOccurred())
				Expect(account.ExternalID).To(Equal("A"))
				account.ExternalID = "changed returned copy"
			}()
			wg.Wait()
			value, ok, err := b.CorrelationValue(ctx, billing.NewAccountRef("x"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(value).To(Equal("B"))
			Expect(b.Calls()).To(HaveLen(1))
			value, _, err = a.CorrelationValue(ctx, billing.NewAccountRef("x"))
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("A"))
		})

		It("copies scenario accounts and returns independent list and call snapshots", func() {
			s := billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "x", ExternalID: "original"}}}
			f := billingfake.New(s)
			s.Accounts[0].ExternalID = "caller changed scenario"
			page, err := f.ListAccounts(ctx, billing.AccountFilter{}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Accounts).To(HaveLen(1))
			Expect(page.Accounts[0].ExternalID).To(Equal("original"))
			page.Accounts[0].ExternalID = "caller changed result"
			calls := f.Calls()
			calls[0].Op = "caller changed log"
			value, _, err := f.CorrelationValue(ctx, billing.NewAccountRef("x"))
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("original"))
			Expect(f.Calls()[0].Op).To(Equal("ListAccounts"))
			value, _, err = billingfake.New(s).CorrelationValue(ctx, billing.NewAccountRef("x"))
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("caller changed scenario"))
		})

		It("loads a JSON fixture and rejects unknown fields", func() {
			s, err := billingfake.Load([]byte(`{
				"accounts":[{"id":"a","externalId":"e","state":"ACTIVE","rawStatus":"active"}],
				"failures":{"HealthCheck":{"reason":"TIMEOUT","retryable":true,"times":1}}}`))
			Expect(err).ToNot(HaveOccurred())
			f := billingfake.New(s)
			v, _, _ := f.CorrelationValue(ctx, billing.NewAccountRef("a"))
			Expect(v).To(Equal("e"))
			Expect(reasonOf(f.HealthCheck(ctx))).To(Equal(billing.ReasonTimeout))
			Expect(f.HealthCheck(ctx)).To(Succeed())
			_, err = billingfake.Load([]byte(`{"bogus":1}`))
			Expect(err).To(HaveOccurred())
		})
	})
})
