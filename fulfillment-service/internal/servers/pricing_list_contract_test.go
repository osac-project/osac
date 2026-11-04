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

	DescribeTable("requires a supplied three-character currency on both request contracts",
		func(currency *string, valid bool) {
			checkBoth(publicv1.ProviderRateCardItemsListRequest_builder{CurrencyCode: currency}.Build(),
				&privatev1.ProviderRateCardItemsListRequest{}, valid)
		},
		Entry("omitted", nil, false),
		Entry("empty", proto.String(""), false),
		Entry("short", proto.String("US"), false),
		Entry("long", proto.String("USDX"), false),
		Entry("three characters", proto.String("USD"), true),
		Entry("lowercase is not prohibited", proto.String("usd"), true),
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
			checkBoth(publicv1.ProviderRateCardItemsListResponse_builder{
				Items: []*publicv1.ProviderRateCardItem{publicv1.ProviderRateCardItem_builder{
					Service: service, CurrencyCode: currency,
					ResourceConditions: []*publicv1.ProviderRateCardItemResourceCondition{
						publicv1.ProviderRateCardItemResourceCondition_builder{
							Dimension: dimension, Operator: operator, Value: value,
						}.Build(),
					},
					// Neither an ANY value nor the provider's unit has a nonempty/known-unit requirement.
					Rate: publicv1.ProviderRateCardItemRate_builder{Amount: "1", Unit: "provider-unit"}.Build(),
				}.Build()},
			}.Build(), &privatev1.ProviderRateCardItemsListResponse{}, valid)
		},
		Entry("ANY permits empty value", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, true),
		Entry("EQUALS is a supported operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS, true),
		Entry("missing service", "", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("missing dimension", "compute_instance", "USD", "",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("missing currency", "compute_instance", "", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("short currency", "compute_instance", "US", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("long currency", "compute_instance", "USDX", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY, false),
		Entry("unspecified operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_UNSPECIFIED, false),
		Entry("undefined operator", "compute_instance", "USD", "instance_type",
			publicv1.BillingRateMatchOperator(99), false),
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
