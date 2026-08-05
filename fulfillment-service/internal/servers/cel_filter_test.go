/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CEL filter utilities", func() {
	Describe("validateCELSyntax", func() {
		DescribeTable("validates syntax",
			func(input string, shouldPass bool) {
				err := validateCELSyntax(input)
				if shouldPass {
					Expect(err).ToNot(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			},
			Entry("valid simple expression", "true", true),
			Entry("valid field reference", "this.published", true),
			Entry("valid comparison", "this.id == '123'", true),
			Entry("valid compound", "this.a && this.b || this.c", true),
			Entry("unbalanced closing paren", "true)", false),
			Entry("unbalanced opening paren", "(true", false),
			Entry("injection attempt", `true) || (true`, false),
			Entry("empty string is not valid CEL", "", false),
		)
	})

	Describe("filterReferencedFields", func() {
		It("returns all dotted field paths from a compound expression", func() {
			fields, err := filterReferencedFields(
				"this.metadata.name == 'x' && this.spec.state == 1 || this.spec.enabled")
			Expect(err).ToNot(HaveOccurred())
			// Intermediate selects (this.metadata, this.spec) are also returned because the
			// AST walker visits every select node in the chain, not just leaves.
			Expect(fields).To(Equal(map[string]bool{
				"this.metadata.name": true,
				"this.metadata":      true,
				"this.spec.state":    true,
				"this.spec.enabled":  true,
				"this.spec":          true,
			}))
		})

		It("returns empty map for expression with no field selects", func() {
			fields, err := filterReferencedFields("true")
			Expect(err).ToNot(HaveOccurred())
			Expect(fields).To(BeEmpty())
		})

		It("returns error for invalid syntax", func() {
			_, err := filterReferencedFields("true) || (true")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("filterReferencesAnyField", func() {
		DescribeTable("detects field references",
			func(filter string, expected bool, prefixes ...string) {
				found, err := filterReferencesAnyField(filter, prefixes...)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(Equal(expected))
			},
			Entry("exact field reference",
				"this.spec.state == 1", true, "this.spec.state"),
			Entry("child field via prefix",
				"this.spec.deprecation.deprecation_timestamp != null", true, "this.spec.deprecation"),
			Entry("sibling field with shared prefix does not match",
				"this.spec.state_machine == true", false, "this.spec.state"),
			Entry("unrelated field does not match",
				"this.spec.enabled == false", false, "this.spec.state"),
			Entry("one of multiple prefixes matches",
				"this.spec.enabled == false", true, "this.spec.state", "this.spec.enabled", "this.spec.is_default"),
			Entry("field in compound expression",
				"this.metadata.name == 'test' && this.spec.state == 1", true, "this.spec.state"),
			Entry("no prefix matches",
				"this.metadata.name == 'test'", false, "this.spec.state"),
			Entry("disjunction with repeated field",
				"this.spec.state == 1 || this.spec.state == 2", true, "this.spec.state"),
		)

		It("returns error for invalid syntax", func() {
			_, err := filterReferencesAnyField("true) || (true", "this.spec.state")
			Expect(err).To(HaveOccurred())
		})

	})

	Describe("composeFilterDefaults", func() {
		defaults := []filterDefault{
			{field: "this.spec.state", predicate: "(this.spec.state == 1 || this.spec.state == 2)"},
			{field: "this.spec.enabled", predicate: "this.spec.enabled == true"},
		}

		It("applies all defaults when filter is empty", func() {
			result, err := composeFilterDefaults("", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.state == 1 || this.spec.state == 2) && this.spec.enabled == true"))
		})

		It("applies all defaults when filter references unrelated field", func() {
			result, err := composeFilterDefaults("this.metadata.name == 'test'", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.metadata.name == 'test') && " +
					"(this.spec.state == 1 || this.spec.state == 2) && " +
					"this.spec.enabled == true"))
		})

		It("skips state default when filter references state", func() {
			result, err := composeFilterDefaults("this.spec.state == 3", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.state == 3) && this.spec.enabled == true"))
		})

		It("skips enabled default when filter references enabled", func() {
			result, err := composeFilterDefaults("this.spec.enabled == false", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.enabled == false) && (this.spec.state == 1 || this.spec.state == 2)"))
		})

		It("skips all defaults when filter references all fields", func() {
			result, err := composeFilterDefaults(
				"this.spec.state == 3 && this.spec.enabled == false", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("this.spec.state == 3 && this.spec.enabled == false"))
		})

		It("returns error for invalid filter", func() {
			_, err := composeFilterDefaults("true) || (true", defaults)
			Expect(err).To(HaveOccurred())
		})

		It("skips default when child field is referenced", func() {
			childDefaults := []filterDefault{
				{field: "this.spec.upgrades", predicate: "this.spec.upgrades.enabled == true"},
			}
			result, err := composeFilterDefaults(
				"this.spec.upgrades.count > 0", childDefaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("this.spec.upgrades.count > 0"))
		})
	})

})
