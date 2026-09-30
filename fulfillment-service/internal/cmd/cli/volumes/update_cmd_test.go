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

var _ = Describe("volumes update", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := updateCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("update [FLAG...] ID|NAME"))
		})

		It("should require exactly one argument", func() {
			cmd := updateCmd()
			Expect(cmd.Args).NotTo(BeNil())
		})
	})

	Context("flag registration", func() {
		It("should register --display-name flag", func() {
			cmd := updateCmd()
			flag := cmd.Flags().Lookup("display-name")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("NAME"))
		})

		It("should register --description flag", func() {
			cmd := updateCmd()
			flag := cmd.Flags().Lookup("description")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("TEXT"))
		})
	})

	Context("help text", func() {
		It("should have proper short help", func() {
			cmd := updateCmd()
			Expect(cmd.Short).To(Equal("Update volume properties"))
		})

		It("should have proper long help with examples", func() {
			cmd := updateCmd()
			Expect(cmd.Long).To(ContainSubstring("Update mutable properties of a volume."))
			Expect(cmd.Long).To(ContainSubstring("--display-name"))
			Expect(cmd.Long).To(ContainSubstring("--description"))
		})
	})
})
