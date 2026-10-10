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

var _ = Describe("volumes command", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("volumes"))
	})

	It("should have all expected subcommands", func() {
		cmd := Cmd()
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		Expect(names).To(ContainElements("list", "get", "create", "update", "delete"))
	})

	It("should have proper short help", func() {
		cmd := Cmd()
		Expect(cmd.Short).To(Equal("Manage volumes"))
	})

	It("should have proper long help with examples", func() {
		cmd := Cmd()
		Expect(cmd.Long).To(ContainSubstring("volumes list"))
		Expect(cmd.Long).To(ContainSubstring("volumes get"))
		Expect(cmd.Long).To(ContainSubstring("volumes create"))
		Expect(cmd.Long).To(ContainSubstring("volumes update"))
		Expect(cmd.Long).To(ContainSubstring("volumes delete"))
	})
})
