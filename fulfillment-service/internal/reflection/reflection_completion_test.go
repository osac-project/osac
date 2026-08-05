/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/packages"
)

var _ = Describe("ObjectTypeNames", func() {
	It("returns names for public packages", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).ToNot(BeEmpty())
		Expect(names).To(ContainElement("cluster"))
		Expect(names).To(ContainElement("clusters"))
	})

	It("returns names for private packages", func() {
		names := ObjectTypeNames(packages.Private...)
		Expect(names).ToNot(BeEmpty())
		Expect(names).To(ContainElement("cluster"))
		Expect(names).To(ContainElement("clusters"))
	})

	It("includes both singular and plural forms", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).To(ContainElement("virtualnetwork"))
		Expect(names).To(ContainElement("virtualnetworks"))
		Expect(names).To(ContainElement("computeinstance"))
		Expect(names).To(ContainElement("computeinstances"))
	})

	It("returns sorted results", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).To(Equal(sortedCopy(names)))
	})

	It("returns empty for unknown package", func() {
		names := ObjectTypeNames("nonexistent.package.v1")
		Expect(names).To(BeEmpty())
	})
})

func sortedCopy(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	for i := range c {
		for j := i + 1; j < len(c); j++ {
			if c[i] > c[j] {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
	return c
}
