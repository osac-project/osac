/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package output

import (
	"bytes"
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("output", func() {
	It("accepts the documented formats and rejects anything else", func() {
		Expect(Validate(Table)).To(Succeed())
		Expect(Validate(JSON)).To(Succeed())
		Expect(Validate(YAML)).To(Succeed())
		Expect(Validate("xml")).To(MatchError(
			"unknown output format 'xml', should be 'table', 'json' or 'yaml'"))
	})

	It("encodes messages with proto field names as a JSON array, empty rather than null when there are none", func() {
		items := []*publicv1.CatalogPricingInput{
			publicv1.CatalogPricingInput_builder{Service: "compute_instance", Dimension: "instance_type"}.Build(),
		}
		values, err := Messages(items)
		Expect(err).ToNot(HaveOccurred())
		Expect(values).To(HaveLen(1))
		Expect(values[0]).To(HaveKeyWithValue("service", "compute_instance"))
		Expect(values[0]).To(HaveKeyWithValue("dimension", "instance_type"))

		empty, err := Messages[*publicv1.CatalogPricingInput](nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).ToNot(BeNil())
		Expect(empty).To(BeEmpty())
	})

	It("renders JSON and YAML through the console", func() {
		logger, err := logging.NewLogger().
			SetLevel(slog.LevelDebug.String()).
			SetWriter(GinkgoWriter).
			Build()
		Expect(err).ToNot(HaveOccurred())
		for _, format := range []string{JSON, YAML} {
			buffer := &bytes.Buffer{}
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
			Expect(err).ToNot(HaveOccurred())
			Render(context.Background(), console, format, []any{map[string]any{"service": "compute_instance"}})
			Expect(buffer.String()).To(ContainSubstring("compute_instance"))
		}
	})

	It("replaces an empty table value with a dash", func() {
		Expect(OrDash("")).To(Equal("-"))
		Expect(OrDash("value")).To(Equal("value"))
	})
})
