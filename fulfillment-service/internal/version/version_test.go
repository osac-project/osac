/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package version

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Get", func() {
	When("Version has 'v' prefix", func() {
		BeforeEach(func() {
			value := Get()
			Set("v1.2.3")
			DeferCleanup(func() {
				Set(value)
			})
		})

		It("Removes the prefix", func() {
			Expect(Get()).To(Equal("1.2.3"))
		})
	})

	When("Version has no 'v' prefix", func() {
		BeforeEach(func() {
			value := Get()
			Set("v1.2.3")
			DeferCleanup(func() {
				Set(value)
			})
		})

		It("Returns value without changes", func() {
			Expect(Get()).To(Equal("1.2.3"))
		})
	})

	When("Version is unknown", func() {
		BeforeEach(func() {
			value := Get()
			Set(Unknown)
			DeferCleanup(func() {
				Set(value)
			})
		})

		It("Returns git hash", func() {
			Expect(Get()).To(Equal(Unknown))
		})
	})
})
