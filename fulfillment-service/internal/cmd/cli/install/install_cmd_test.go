/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Install command", func() {
	It("has the expected use string", func() {
		Expect(Cmd().Use).To(Equal("install COMMAND [FLAG...]"))
	})

	It("has short and long help text", func() {
		cmd := Cmd()
		Expect(cmd.Short).ToNot(BeEmpty())
		Expect(cmd.Long).ToNot(BeEmpty())
	})

	It("registers discover and validate as subcommands", func() {
		names := map[string]bool{}
		for _, sub := range Cmd().Commands() {
			names[sub.Name()] = true
		}
		Expect(names).To(HaveKey("discover"))
		Expect(names).To(HaveKey("validate"))
	})
})
