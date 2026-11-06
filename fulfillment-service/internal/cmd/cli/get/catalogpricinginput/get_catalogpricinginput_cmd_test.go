/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogpricinginput

import (
	"bytes"
	"errors"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/golden"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	items := func() []*publicv1.CatalogPricingInput {
		return []*publicv1.CatalogPricingInput{
			publicv1.CatalogPricingInput_builder{
				Service:         "compute_instance",
				Dimension:       "instance_type",
				Value:           "m1.small",
				AcceptsAnyValue: false,
				CatalogItems: []*publicv1.CatalogPricingInputCatalogItem{
					publicv1.CatalogPricingInputCatalogItem_builder{
						Id: "c1", Name: "basic-vm", Title: "Basic VM", Published: true,
					}.Build(),
				},
			}.Build(),
			publicv1.CatalogPricingInput_builder{
				Service:         "cluster",
				Dimension:       "node_count",
				Value:           "",
				AcceptsAnyValue: true,
				CatalogItems: []*publicv1.CatalogPricingInputCatalogItem{
					publicv1.CatalogPricingInputCatalogItem_builder{
						Id: "c2", Name: "flex-cluster", Title: "Flex Cluster", Published: false,
					}.Build(),
				},
			}.Build(),
		}
	}
	run := func(format string, items []*publicv1.CatalogPricingInput) string {
		buffer := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, items)).To(Succeed())
		return buffer.String()
	}

	It("renders a table with one row per tuple", func() {
		output := run("table", items())
		Expect(output).To(MatchRegexp(`SERVICE\s+DIMENSION\s+VALUE\s+ACCEPTS ANY VALUE\s+CATALOG ITEMS`))
		Expect(output).To(MatchRegexp(`compute_instance\s+instance_type\s+m1\.small\s+false\s+basic-vm`))
		Expect(output).To(MatchRegexp(`cluster\s+node_count\s+<any>\s+true\s+flex-cluster`))
	})

	It("renders an empty table header with no rows when there are no items", func() {
		output := run("table", nil)
		Expect(output).To(MatchRegexp(`SERVICE\s+DIMENSION\s+VALUE\s+ACCEPTS ANY VALUE\s+CATALOG ITEMS`))
		Expect(output).ToNot(ContainSubstring("compute_instance"))
	})

	It("renders JSON as an array of items with API field names", func() {
		output := run("json", items())
		Expect(output).To(MatchJSON(`[
			{
				"service": "compute_instance",
				"dimension": "instance_type",
				"value": "m1.small",
				"catalog_items": [{"id": "c1", "name": "basic-vm", "title": "Basic VM", "published": true}]
			},
			{
				"service": "cluster",
				"dimension": "node_count",
				"accepts_any_value": true,
				"catalog_items": [{"id": "c2", "name": "flex-cluster", "title": "Flex Cluster"}]
			}
		]`))
	})

	It("renders an empty JSON array when there are no items", func() {
		output := run("json", nil)
		Expect(output).To(MatchJSON(`[]`))
	})

	It("renders YAML with the API field names", func() {
		output := run("yaml", items())
		Expect(output).To(ContainSubstring("service: compute_instance"))
		Expect(output).To(ContainSubstring("accepts_any_value: true"))
	})

	// Golden files live in testdata/. Regenerate with UPDATE_GOLDEN=1 and review the diff.
	DescribeTable("matches the golden output",
		func(format string, source func() []*publicv1.CatalogPricingInput, name string) {
			golden.Assert(name, run(format, source()))
		},
		Entry("table, with items", "table", items, "items.table.golden"),
		Entry("table, empty", "table", func() []*publicv1.CatalogPricingInput { return nil }, "empty.table.golden"),
		Entry("json, with items", "json", items, "items.json.golden"),
		Entry("json, empty", "json", func() []*publicv1.CatalogPricingInput { return nil }, "empty.json.golden"),
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
