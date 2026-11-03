/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package providerratecarditem

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/golden"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	items := func() []*publicv1.ProviderRateCardItem {
		return []*publicv1.ProviderRateCardItem{
			publicv1.ProviderRateCardItem_builder{
				Service:      "compute_instance",
				CurrencyCode: "USD",
				ResourceConditions: []*publicv1.ProviderRateCardItemResourceCondition{
					publicv1.ProviderRateCardItemResourceCondition_builder{
						Dimension: "instance_type",
						Operator:  publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS,
						Value:     "small",
					}.Build(),
				},
				Rate:               publicv1.ProviderRateCardItemRate_builder{Amount: "0.00125", Unit: "second"}.Build(),
				CatalogItemMatches: []string{"vm-fedora-small"},
			}.Build(),
			publicv1.ProviderRateCardItem_builder{
				Service:      "widget_service",
				CurrencyCode: "USD",
				ResourceConditions: []*publicv1.ProviderRateCardItemResourceCondition{
					publicv1.ProviderRateCardItemResourceCondition_builder{
						Dimension: "widget_size",
						Operator:  publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_ANY,
					}.Build(),
				},
				Rate: publicv1.ProviderRateCardItemRate_builder{Amount: "5", Unit: "second"}.Build(),
			}.Build(),
		}
	}

	run := func(format string, rows []*publicv1.ProviderRateCardItem) string {
		buffer := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, rows)).To(Succeed())
		return buffer.String()
	}

	It("renders a readable table with ANY shown and no enum prefixes", func() {
		output := run("table", items())
		Expect(output).To(ContainSubstring("compute_instance"))
		Expect(output).To(ContainSubstring("instance_type=small"))
		Expect(output).To(ContainSubstring("widget_size=ANY"))
		Expect(output).To(ContainSubstring("vm-fedora-small"))
		Expect(output).ToNot(ContainSubstring("BILLING_RATE_MATCH_OPERATOR"))
	})

	It("renders an empty table header with no rows", func() {
		output := run("table", nil)
		Expect(output).To(ContainSubstring("SERVICE"))
	})

	It("renders JSON with the API field names", func() {
		output := run("json", items()[:1])
		Expect(output).To(ContainSubstring(`"currency_code"`))
		Expect(output).To(ContainSubstring(`"catalog_item_matches"`))
	})

	// Golden files live in testdata/. Regenerate with UPDATE_GOLDEN=1 and review the diff.
	DescribeTable("matches the golden output",
		func(format string, name string) {
			golden.Assert(name, run(format, items()))
		},
		Entry("table", "table", "items.table.golden"),
		Entry("json", "json", "items.json.golden"),
	)
})
