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
	"errors"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/shopspring/decimal"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/pricing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

var _ = Describe("Resolver.ListServiceRateCardItems", func() {
	ctx := context.Background()

	It("normalizes a QUANTITY row with no attribute values as a single ANY condition (OSAC-6891 AC2/AC5)", func() {
		resolver := pricing.NewResolver(billingfake.New(baseScenario()))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(skipped).To(BeEmpty())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Service).To(Equal("compute_instance"))
		Expect(items[0].Currency).To(Equal("USD"))
		Expect(items[0].Unit).To(Equal("second"))
		Expect(items[0].Amount.Equal(decimal.RequireFromString("0.25"))).To(BeTrue())
		Expect(items[0].Conditions).To(Equal([]pricing.RateCondition{{Dimension: "cpu_hours", Operator: pricing.OperatorAny}}))
	})

	It("normalizes a row with attribute values as EQUALS conditions (AC5)", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Rows: []billing.RateRow{{
				DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.00125",
				AttributeValues: []billing.AttributeValue{{Name: "instance_type", Value: "small"}},
			}},
		}
		resolver := pricing.NewResolver(billingfake.New(scenario))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(skipped).To(BeEmpty())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Conditions).To(Equal(
			[]pricing.RateCondition{{Dimension: "instance_type", Operator: pricing.OperatorEquals, Value: "small"}}))
	})

	DescribeTable("omits an unsupported row shape and counts it (AC3)",
		func(row billing.RateRow) {
			scenario := baseScenario()
			scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{Rows: []billing.RateRow{row}}
			resolver := pricing.NewResolver(billingfake.New(scenario))
			items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
			Expect(err).ToNot(HaveOccurred())
			Expect(items).To(BeEmpty())
			Expect(skipped[pricing.SkipRateShapeUnsupported]).To(Equal(1))
		},
		Entry("FLAT_RATE", billing.RateRow{DimensionID: "dim-1", PriceMethod: "FLAT_RATE", UnitRate: "5"}),
		Entry("TIERED", billing.RateRow{DimensionID: "dim-1", PriceMethod: "TIERED", UnitRate: "5"}),
		Entry("NO_PRICE", billing.RateRow{DimensionID: "dim-1", PriceMethod: "NO_PRICE"}),
		Entry("QUANTITY with empty unitRate", billing.RateRow{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: ""}),
		Entry("QUANTITY with unparsable unitRate",
			billing.RateRow{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "not-a-number"}),
	)

	It("omits and counts a row whose priced dimension id is not one of the service's priceable attributes", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Rows: []billing.RateRow{{DimensionID: "dim-unknown", PriceMethod: "QUANTITY", UnitRate: "1"}},
		}
		resolver := pricing.NewResolver(billingfake.New(scenario))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(items).To(BeEmpty())
		Expect(skipped[pricing.SkipDimensionUnmapped]).To(Equal(1))
	})

	It("does not fail the whole list for one unsupported row among supported ones", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Rows: []billing.RateRow{
				{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.25"},
				{DimensionID: "dim-1", PriceMethod: "TIERED", UnitRate: "5"},
			},
		}
		resolver := pricing.NewResolver(billingfake.New(scenario))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(skipped[pricing.SkipRateShapeUnsupported]).To(Equal(1))
	})

	It("returns an empty, error-free result for a service with no matching provider name", func() {
		resolver := pricing.NewResolver(billingfake.New(baseScenario()))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "no_such_service", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(items).To(BeEmpty())
		Expect(skipped).To(BeEmpty())
	})

	It("returns an empty, error-free result for a currency with no rate card", func() {
		resolver := pricing.NewResolver(billingfake.New(baseScenario()))
		items, skipped, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "EUR")
		Expect(err).ToNot(HaveOccurred())
		Expect(items).To(BeEmpty())
		Expect(skipped).To(BeEmpty())
	})

	It("returns a classified provider error for an ambiguous service name match, never a guessed row", func() {
		scenario := baseScenario()
		scenario.Services = append(scenario.Services, scenario.Services[0])
		scenario.Services[1].ID = "svc-2"
		resolver := pricing.NewResolver(billingfake.New(scenario))
		items, _, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).To(HaveOccurred())
		Expect(items).To(BeEmpty())
		var be *billing.Error
		Expect(errors.As(err, &be)).To(BeTrue())
		Expect(be.Reason).To(Equal(billing.ReasonAmbiguous))
	})

	It("preserves the provider decimal amount exactly, without rounding", func() {
		scenario := baseScenario()
		scenario.Services[0].Rates["USD"] = billingfake.RateCardSpec{
			Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "0.001234567"}},
		}
		resolver := pricing.NewResolver(billingfake.New(scenario))
		items, _, err := resolver.ListServiceRateCardItems(ctx, "compute_instance", "USD")
		Expect(err).ToNot(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Amount.String()).To(Equal("0.001234567"))
	})
})
