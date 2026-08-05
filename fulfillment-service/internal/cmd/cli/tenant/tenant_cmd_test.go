/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tenant command", func() {
	It("Has the expected use string", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("tenant [FLAG...] [NAME]"))
	})

	It("Has a --clear flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("clear")
		Expect(flag).ToNot(BeNil())
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Has an --output flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("output")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("o"))
	})

})
