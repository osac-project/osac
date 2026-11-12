/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package rendering

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Output formats and encoding", func() {
	It("accepts the documented formats and rejects anything else", func() {
		Expect(ValidateFormat(Table)).To(Succeed())
		Expect(ValidateFormat(JSON)).To(Succeed())
		Expect(ValidateFormat(YAML)).To(Succeed())
		Expect(ValidateFormat("xml")).To(MatchError(
			"unknown output format 'xml', should be 'table', 'json' or 'yaml'"))
	})

	It("encodes a message with proto field names", func() {
		item := publicv1.CatalogPricingInput_builder{Service: "compute_instance", Dimension: "instance_type"}.Build()
		value, err := Encode(item)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(HaveKeyWithValue("service", "compute_instance"))
		Expect(value).To(HaveKeyWithValue("dimension", "instance_type"))
		Expect(value).ToNot(HaveKey("@type"))
	})

	It("encodes messages as a JSON array, empty rather than null when there are none", func() {
		items := []*publicv1.CatalogPricingInput{
			publicv1.CatalogPricingInput_builder{Service: "compute_instance", Dimension: "instance_type"}.Build(),
		}
		values, err := EncodeList(items)
		Expect(err).ToNot(HaveOccurred())
		Expect(values).To(HaveLen(1))
		Expect(values[0]).To(HaveKeyWithValue("service", "compute_instance"))
		Expect(values[0]).To(HaveKeyWithValue("dimension", "instance_type"))

		empty, err := EncodeList[*publicv1.CatalogPricingInput](nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).ToNot(BeNil())
		Expect(empty).To(BeEmpty())
	})

	It("encodes a message with its type URL", func() {
		item := publicv1.CatalogPricingInput_builder{Service: "compute_instance"}.Build()
		value, err := EncodeWithType(item)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(HaveKeyWithValue("@type", "type.googleapis.com/osac.public.v1.CatalogPricingInput"))
		Expect(value).To(HaveKeyWithValue("service", "compute_instance"))
	})

	It("encodes messages as a JSON array of typed values, empty rather than null when there are none", func() {
		items := []*publicv1.CatalogPricingInput{
			publicv1.CatalogPricingInput_builder{Service: "compute_instance"}.Build(),
		}
		values, err := EncodeListWithType(items)
		Expect(err).ToNot(HaveOccurred())
		Expect(values).To(HaveLen(1))
		Expect(values[0]).To(HaveKeyWithValue("@type", "type.googleapis.com/osac.public.v1.CatalogPricingInput"))

		empty, err := EncodeListWithType[*publicv1.CatalogPricingInput](nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).ToNot(BeNil())
		Expect(empty).To(BeEmpty())
	})

	It("replaces an empty table value with a dash", func() {
		Expect(Dash("")).To(Equal("-"))
		Expect(Dash("value")).To(Equal("value"))
	})
})
