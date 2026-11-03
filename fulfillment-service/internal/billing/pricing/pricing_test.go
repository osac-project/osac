/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package pricing_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

// baseScenario scripts one service ("compute_instance") with one priceable attribute ("cpu_hours", id "dim-1")
// and a QUANTITY rate of 0.25 USD for it. Tests copy and mutate it.
func baseScenario() billingfake.Scenario {
	return billingfake.Scenario{
		Services: []billingfake.ServiceSpec{{
			ID:         "svc-1",
			Name:       "compute_instance",
			Attributes: []billing.PriceableAttribute{{ID: "dim-1", Name: "cpu_hours"}},
			Rates: map[string]billingfake.RateCardSpec{
				"USD": {Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}}},
			},
		}},
	}
}

func baseComponent() pricing.ComponentRequest {
	return pricing.ComponentRequest{
		Service: "compute_instance", Dimension: "cpu_hours", Values: map[string]string{"cpu_hours": "4"},
	}
}

func resolveOne(scenario billingfake.Scenario, currency string, component pricing.ComponentRequest) pricing.ComponentResult {
	resolver := pricing.NewResolver(billingfake.New(scenario))
	req := pricing.Request{Account: billing.NewAccountRef("acct-1"), Currency: currency, Components: []pricing.ComponentRequest{component}}
	result, err := resolver.Resolve(context.Background(), req)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, result.Components).To(HaveLen(1))
	return result.Components[0]
}

var _ = Describe("Resolver", func() {
	ctx := context.Background()

	It("resolves a covered component with exact decimal derivations", func() {
		result := resolveOne(baseScenario(), "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateCovered))
		Expect(result.Reason).To(BeEmpty())
		Expect(result.Currency).To(Equal("USD"))
		Expect(result.Unit).To(Equal("second"))
		Expect(result.PerSecond.Equal(decimal.RequireFromString("0.25"))).To(BeTrue())
		Expect(result.PerHour.Equal(decimal.RequireFromString("900"))).To(BeTrue())
		Expect(result.PerMonth.Equal(decimal.RequireFromString("657000"))).To(BeTrue())
	})

	It("treats a zero rate as covered, not as a missing rate", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0"}},
		}
		result := resolveOne(scenario, "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateCovered))
		Expect(result.PerSecond.IsZero()).To(BeTrue())
	})

	It("reports a coverage gap when no provider service matches the OSAC name exactly", func() {
		result := resolveOne(baseScenario(), "USD",
			pricing.ComponentRequest{Service: "no_such_service", Dimension: "cpu_hours", Values: map[string]string{"cpu_hours": "4"}})
		Expect(result.State).To(Equal(pricing.StateGap))
		Expect(result.Reason).To(Equal(pricing.ReasonCoverageGap))
	})

	It("rejects a service name differing only by case as not an exact match", func() {
		result := resolveOne(baseScenario(), "USD",
			pricing.ComponentRequest{Service: "Compute_Instance", Dimension: "cpu_hours", Values: map[string]string{"cpu_hours": "4"}})
		Expect(result.State).To(Equal(pricing.StateGap))
		Expect(result.Reason).To(Equal(pricing.ReasonCoverageGap))
	})

	It("reports AMBIGUOUS when more than one service matches the exact name", func() {
		scenario := baseScenario()
		scenario.Services = append(scenario.Services, billingfake.ServiceSpec{ID: "svc-2", Name: "compute_instance"})
		result := resolveOne(scenario, "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateAmbiguous))
		Expect(result.Reason).To(Equal(pricing.ReasonRateAmbiguous))
	})

	It("reports a coverage gap when no priceable attribute matches the OSAC dimension name", func() {
		result := resolveOne(baseScenario(), "USD",
			pricing.ComponentRequest{Service: "compute_instance", Dimension: "no_such_dimension", Values: map[string]string{"no_such_dimension": "4"}})
		Expect(result.State).To(Equal(pricing.StateGap))
		Expect(result.Reason).To(Equal(pricing.ReasonCoverageGap))
	})

	It("reports a coverage gap when the rate card has no row for the matched dimension", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{}
		result := resolveOne(scenario, "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateGap))
		Expect(result.Reason).To(Equal(pricing.ReasonCoverageGap))
	})

	It("reports a coverage gap when the provider has no rate card at all for the currency", func() {
		result := resolveOne(baseScenario(), "EUR", baseComponent())
		Expect(result.State).To(Equal(pricing.StateGap))
		Expect(result.Reason).To(Equal(pricing.ReasonCoverageGap))
	})

	It("reports AMBIGUOUS when duplicate rules match at the same specificity", func() {
		scenario := baseScenario()
		row := billing.RateRow{
			DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.5",
			AttributeValues: []billing.AttributeValue{{Name: "region", Value: "us-east"}},
		}
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{row, row}}
		component := baseComponent()
		component.Values["region"] = "us-east"
		result := resolveOne(scenario, "USD", component)
		Expect(result.State).To(Equal(pricing.StateAmbiguous))
		Expect(result.Reason).To(Equal(pricing.ReasonRateAmbiguous))
	})

	It("prefers the more specific of two non-conflicting rows over the unconstrained one", func() {
		scenario := baseScenario()
		generic := billing.RateRow{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}
		specific := billing.RateRow{
			DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.5",
			AttributeValues: []billing.AttributeValue{{Name: "region", Value: "us-east"}},
		}
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{generic, specific}}
		component := baseComponent()
		component.Values["region"] = "us-east"
		result := resolveOne(scenario, "USD", component)
		Expect(result.State).To(Equal(pricing.StateCovered))
		Expect(result.PerSecond.Equal(decimal.RequireFromString("0.5"))).To(BeTrue())
	})

	DescribeTable("unsupported rate shapes yield UNAVAILABLE, never a partial price",
		func(method, unitRate string) {
			scenario := baseScenario()
			scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
				Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: method, UnitRate: unitRate}},
			}
			result := resolveOne(scenario, "USD", baseComponent())
			Expect(result.State).To(Equal(pricing.StateUnavailable))
			Expect(result.Reason).To(Equal(pricing.ReasonRateShapeUnsupported))
			Expect(result.PerSecond.IsZero()).To(BeTrue())
			Expect(result.Currency).To(BeEmpty())
		},
		Entry("FLAT_RATE", "FLAT_RATE", "10"),
		Entry("TIERED", "TIERED", "10"),
		Entry("AMOUNT", "AMOUNT", "10"),
		Entry("NO_PRICE", "NO_PRICE", ""),
		Entry("QUANTITY with no unit rate", "QUANTITY", ""),
	)

	It("reports a currency mismatch between the account currency and the returned rate card", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Currency: "EUR",
			Rows:     []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"}},
		}
		result := resolveOne(scenario, "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateUnavailable))
		Expect(result.Reason).To(Equal(pricing.ReasonCurrencyMismatch))
	})

	It("reports an unresolved currency without calling the provider", func() {
		fake := billingfake.New(baseScenario())
		resolver := pricing.NewResolver(fake)
		req := pricing.Request{Account: billing.NewAccountRef("acct-1"), Components: []pricing.ComponentRequest{baseComponent()}}
		result, err := resolver.Resolve(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Components[0].State).To(Equal(pricing.StateUnavailable))
		Expect(result.Components[0].Reason).To(Equal(pricing.ReasonCurrencyUnresolved))
		Expect(fake.Calls()).To(BeEmpty())
	})

	It("never substitutes another rate when the provider lookup fails", func() {
		scenario := baseScenario()
		scenario.Failures = map[string]billingfake.Failure{
			"FindServiceByName": {Reason: billing.ReasonTimeout, Retryable: true},
		}
		result := resolveOne(scenario, "USD", baseComponent())
		Expect(result.State).To(Equal(pricing.StateUnavailable))
		Expect(result.Reason).To(Equal(string(billing.ReasonTimeout)))
		Expect(result.PerSecond.IsZero()).To(BeTrue())
	})

	It("fails a component missing a configured value for its required dimension as a validation error, not a GAP", func() {
		resolver := pricing.NewResolver(billingfake.New(baseScenario()))
		req := pricing.Request{
			Account: billing.NewAccountRef("acct-1"), Currency: "USD",
			Components: []pricing.ComponentRequest{{Service: "compute_instance", Dimension: "cpu_hours", Values: map[string]string{}}},
		}
		_, err := resolver.Resolve(ctx, req)
		Expect(err).To(HaveOccurred())
	})

	It("requires a configured provider adapter", func() {
		resolver := pricing.NewResolver(nil)
		_, err := resolver.Resolve(ctx, pricing.Request{Components: []pricing.ComponentRequest{baseComponent()}})
		Expect(err).To(HaveOccurred())
	})

	It("aggregates CURRENT only when every component is covered", func() {
		scenario := baseScenario()
		resolver := pricing.NewResolver(billingfake.New(scenario))
		req := pricing.Request{Account: billing.NewAccountRef("acct-1"), Currency: "USD", Components: []pricing.ComponentRequest{baseComponent()}}
		result, err := resolver.Resolve(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Aggregate).To(Equal(pricing.AggregateCurrent))
	})

	It("aggregates GAPPED when a component is a gap and none is unavailable", func() {
		scenario := baseScenario()
		resolver := pricing.NewResolver(billingfake.New(scenario))
		gapComponent := pricing.ComponentRequest{Service: "no_such_service", Dimension: "cpu_hours", Values: map[string]string{"cpu_hours": "4"}}
		req := pricing.Request{Account: billing.NewAccountRef("acct-1"), Currency: "USD", Components: []pricing.ComponentRequest{baseComponent(), gapComponent}}
		result, err := resolver.Resolve(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Aggregate).To(Equal(pricing.AggregateGapped))
	})

	It("aggregates UNAVAILABLE even when another component is only a gap", func() {
		scenario := baseScenario()
		resolver := pricing.NewResolver(billingfake.New(scenario))
		gapComponent := pricing.ComponentRequest{Service: "no_such_service", Dimension: "cpu_hours", Values: map[string]string{"cpu_hours": "4"}}
		req := pricing.Request{Account: billing.NewAccountRef("acct-1"), Components: []pricing.ComponentRequest{baseComponent(), gapComponent}} // no currency: unavailable
		result, err := resolver.Resolve(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Aggregate).To(Equal(pricing.AggregateUnavailable))
	})
})
