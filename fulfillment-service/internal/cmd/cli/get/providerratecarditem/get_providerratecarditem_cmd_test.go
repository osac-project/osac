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
	"errors"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	It("documents the count-only output in limit flag help", func() {
		usage := Cmd().Flags().Lookup("limit").Usage
		Expect(usage).To(ContainSubstring("TOTAL"))
		Expect(usage).To(ContainSubstring("total"))
		Expect(usage).To(ContainSubstring("JSON/YAML (no items)"))
	})

	It("returns an error when writing the count-only table fails", func() {
		err := errors.New("write failed")
		console, buildErr := terminal.NewConsole().SetLogger(logger).SetStdout(failingWriter{err: err}).Build()
		Expect(buildErr).NotTo(HaveOccurred())
		Expect(renderTotal(ctx, console, "table", 42)).To(MatchError(err))
	})

	items := func() []*publicv1.ProviderRateCardItem {
		return []*publicv1.ProviderRateCardItem{
			publicv1.ProviderRateCardItem_builder{
				Service:      "compute_instance",
				CurrencyCode: "USD",
				Dimension:    "compute_rate",
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
		Expect(testing.TableRows(output)).To(Equal([][]string{
			{"SERVICE", "CURRENCY", "DIMENSION", "CONDITIONS", "AMOUNT", "UNIT", "CATALOG ITEMS"},
		}))
	})

	It("renders an unkeyed row's dimension without inventing conditions", func() {
		output := run("table", []*publicv1.ProviderRateCardItem{publicv1.ProviderRateCardItem_builder{
			Service: "compute_instance", CurrencyCode: "USD", Dimension: "compute_rate",
			Rate: publicv1.ProviderRateCardItemRate_builder{Amount: "1", Unit: "second"}.Build(),
		}.Build()})
		Expect(testing.TableRows(output)[1]).To(Equal(
			[]string{"compute_instance", "USD", "compute_rate", "-", "1", "second", "-"}))
	})

	It("renders JSON with the API field names", func() {
		output := run("json", items()[:1])
		Expect(output).To(ContainSubstring(`"currency_code"`))
		Expect(output).To(ContainSubstring(`"catalog_item_matches"`))
	})

	DescribeTable("renders the expected output",
		func(format string, assert func(string)) {
			assert(run(format, items()))
		},
		Entry("table", "table", func(output string) {
			Expect(testing.TableRows(output)).To(Equal([][]string{
				{"SERVICE", "CURRENCY", "DIMENSION", "CONDITIONS", "AMOUNT", "UNIT", "CATALOG ITEMS"},
				{"compute_instance", "USD", "compute_rate", "instance_type=small", "0.00125", "second", "vm-fedora-small"},
				{"widget_service", "USD", "-", "widget_size=ANY", "5", "second", "-"},
			}))
		}),
		Entry("json", "json", func(output string) {
			Expect(output).To(MatchJSON(`[
				{
					"service": "compute_instance",
					"currency_code": "USD",
					"dimension": "compute_rate",
					"resource_conditions": [
						{"dimension": "instance_type", "operator": "BILLING_RATE_MATCH_OPERATOR_EQUALS", "value": "small"}
					],
					"rate": {"amount": "0.00125", "unit": "second"},
					"catalog_item_matches": ["vm-fedora-small"]
				},
				{
					"service": "widget_service",
					"currency_code": "USD",
					"resource_conditions": [
						{"dimension": "widget_size", "operator": "BILLING_RATE_MATCH_OPERATOR_ANY"}
					],
					"rate": {"amount": "5", "unit": "second"}
				}
			]`))
		}),
	)

	It("returns an error when writing the table fails immediately", func() {
		err := errors.New("write failed")
		console, buildErr := terminal.NewConsole().SetLogger(logger).SetStdout(failingWriter{err: err}).Build()
		Expect(buildErr).ToNot(HaveOccurred())
		Expect(render(ctx, console, "table", items())).To(MatchError(err))
	})

	It("returns an error when writing the table fails after some output is buffered", func() {
		err := errors.New("write failed")
		writer := &delayedFailingWriter{err: err, limit: 16}
		console, buildErr := terminal.NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(buildErr).ToNot(HaveOccurred())
		Expect(render(ctx, console, "table", items())).To(MatchError(err))
	})
})

// failingWriter fails every write.
type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

// delayedFailingWriter accepts a limited number of bytes before failing every write, so part of the output is
// buffered before the failure surfaces while flushing the table.
type delayedFailingWriter struct {
	err   error
	limit int
}

func (w *delayedFailingWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		return 0, w.err
	}
	w.limit -= len(p)
	return len(p), nil
}
