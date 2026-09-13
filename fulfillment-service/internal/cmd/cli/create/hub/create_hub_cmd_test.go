/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package hub

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Create hub command", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("hub"))
	})

	It("should register --name flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("name")).NotTo(BeNil())
	})

	It("should register --kubeconfig flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("kubeconfig")).NotTo(BeNil())
	})

	It("should register --namespace flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("namespace")).NotTo(BeNil())
	})

	It("should not register an --id flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("id")).To(BeNil())
	})
})
