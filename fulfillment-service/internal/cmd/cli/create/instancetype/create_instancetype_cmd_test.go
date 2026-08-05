/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package instancetype

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Create instancetype flag registration", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("instancetype"))
	})

	It("should register --name flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("name")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Name"))
	})

	It("should register --cores flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("cores")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("cores"))
	})

	It("should register --memory-gib flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("memory-gib")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("memory"))
	})

	It("should register --description flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("description")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("description"))
	})
})
