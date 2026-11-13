/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Pricing list proto contract", func() {
	// Public and private messages have identical wire fields, but each descriptor must retain its validation
	// annotations after cleanapi generation. Response validation is not performed by the inbound interceptor.
	checkBoth := func(public, private proto.Message, valid bool) {
		data, err := proto.Marshal(public)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Unmarshal(data, private)).To(Succeed())
		for _, message := range []proto.Message{public, private} {
			err := protovalidate.Validate(message)
			if valid {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		}
	}

	DescribeTable("requires a supplied nonempty provider-owned currency on both request contracts",
		func(currency *string, valid bool) {
			public := publicv1.ProviderRateCardItemsListRequest_builder{CurrencyCode: currency}.Build()
			private := &privatev1.ProviderRateCardItemsListRequest{}
			checkBoth(public, private, valid)
			if currency != nil {
				Expect(public.GetCurrencyCode()).To(Equal(*currency))
				Expect(private.GetCurrencyCode()).To(Equal(*currency))
			}
		},
		Entry("omitted", nil, false),
		Entry("empty", proto.String(""), false),
		Entry("one character", proto.String("X"), true),
		Entry("short", proto.String("US"), true),
		Entry("long", proto.String("USDX"), true),
		Entry("provider currency name", proto.String("US Dollar"), true),
		Entry("three characters", proto.String("USD"), true),
		Entry("lowercase is not prohibited", proto.String("usd"), true),
		Entry("mixed case is not normalized", proto.String("uSd"), true),
		Entry("no OSAC allowlist", proto.String("XYZ"), true),
		Entry("no OSAC character policy", proto.String("12!"), true),
		Entry("no trimming", proto.String(" USD "), true),
	)

	DescribeTable("validates pagination without prohibiting clamped offsets",
		func(offset int32, limit *int32, valid bool) {
			checkBoth(publicv1.CatalogPricingInputsListRequest_builder{
				Offset: proto.Int32(offset), Limit: limit,
			}.Build(), &privatev1.CatalogPricingInputsListRequest{}, valid)
			checkBoth(publicv1.ProviderRateCardItemsListRequest_builder{
				Offset: proto.Int32(offset), Limit: limit, CurrencyCode: proto.String("USD"),
			}.Build(), &privatev1.ProviderRateCardItemsListRequest{}, valid)
		},
		Entry("omitted limit", int32(0), nil, true),
		Entry("count-only", int32(0), proto.Int32(0), true),
		Entry("negative limit", int32(0), proto.Int32(-1), false),
		Entry("negative offset intentionally clamps", int32(-1), proto.Int32(1), true),
		Entry("large limit intentionally caps", int32(0), proto.Int32(2147483647), true),
	)

	DescribeTable("validates provider response fields and enum values in both contracts",
		func(service, currency, dimension string, operator publicv1.BillingRateMatchOperator, valid bool) {
			value := ""
			if operator == publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS {
				value = "small"
			}
			public := publicv1.ProviderRateCardItemsListResponse_builder{
				Items: []*publicv1.ProviderRateCardItem{publicv1.ProviderRateCardItem_builder{
					Service: service, CurrencyCode: currency, Dimension: "compute_rate",
					ResourceConditions: []*publicv1.ProviderRateCardItemResourceCondition{
						publicv1.ProviderRateCardItemResourceCondition_builder{
							Dimension: dimension, Operator: operator, Value: value,
						}.Build(),
					},
					// Neither an ANY value nor the provider's unit has a nonempty/known-unit requirement.
					Rate: publicv1.ProviderRateCardItemRate_builder{Amount: "1", Unit: "provider-unit"}.Build(),
				}.Build()},
			}.Build()
			private := &privatev1.ProviderRateCardItemsListResponse{}
			checkBoth(public, private, valid)
			Expect(public.GetItems()[0].GetCurrencyCode()).To(Equal(currency))
			Expect(private.GetItems()[0].GetCurrencyCode()).To(Equal(currency))
		},
		Entry("ANY permits empty value", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("EQUALS is a supported operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS, true),
		Entry("missing service", "", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("missing condition dimension", "compute_instance", "USD", "",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("missing currency", "compute_instance", "", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("one-character currency", "compute_instance", "X", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("short currency", "compute_instance", "US", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("long currency", "compute_instance", "USDX", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("provider currency name", "compute_instance", "US Dollar", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("mixed case is not normalized", "compute_instance", "uSd", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("no trimming", "compute_instance", " USD ", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("no OSAC allowlist", "compute_instance", "XYZ", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("no OSAC character policy", "compute_instance", "12!", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("unspecified operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_UNSPECIFIED, false),
		Entry("undefined operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator(99), false),
	)

	DescribeTable("requires a priced dimension even when the provider row has no conditions",
		func(dimension string, valid bool) {
			checkBoth(publicv1.ProviderRateCardItem_builder{
				Service: "compute_instance", CurrencyCode: "USD", Dimension: dimension,
			}.Build(), &privatev1.ProviderRateCardItem{}, valid)
		},
		Entry("empty priced dimension is invalid", "", false),
		Entry("named priced dimension is valid without conditions", "compute_rate", true),
	)

	DescribeTable("validates catalog response names while allowing empty open values",
		func(service, dimension string, valid bool) {
			checkBoth(publicv1.CatalogPricingInputsListResponse_builder{
				Items: []*publicv1.CatalogPricingInput{publicv1.CatalogPricingInput_builder{
					Service: service, Dimension: dimension, AcceptsAnyValue: true,
				}.Build()},
			}.Build(), &privatev1.CatalogPricingInputsListResponse{}, valid)
		},
		Entry("empty value is valid", "compute_instance", "instance_type", true),
		Entry("missing service", "", "instance_type", false),
		Entry("missing dimension", "compute_instance", "", false),
	)
})
