/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("volumes delete", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := deleteCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("delete [FLAG...] ID|NAME"))
		})

		It("should require exactly one argument", func() {
			cmd := deleteCmd()
			Expect(cmd.Args).NotTo(BeNil())
		})
	})

	Context("help text", func() {
		It("should have proper short help", func() {
			cmd := deleteCmd()
			Expect(cmd.Short).To(Equal("Delete a volume"))
		})

		It("should have proper long help", func() {
			cmd := deleteCmd()
			Expect(cmd.Long).To(ContainSubstring("Delete a volume."))
		})
	})
})
